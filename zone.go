package ical

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alborzmail/go-recur"
)

// zones are the VTIMEZONEs of one calendar by TZID, each read into a
// location the first time a property asks for it.
type zones map[string]func() (*time.Location, error)

// bindZones gives every property of cal that names a TZID the VTIMEZONEs
// cal defines, where RFC 5545 section 3.2.19 says the TZID is looked up.
func bindZones(cal *Component) {
	z := make(zones)
	for _, child := range cal.Children {
		if child.Name != CompTimezone {
			continue
		}
		tzid, _ := child.Props.Text(PropTimezoneID)
		// The first of each TZID, as RemoveDuplicateTimezones keeps.
		if _, ok := z[tzid]; !ok {
			z[tzid] = sync.OnceValues(func() (*time.Location, error) { return timezoneLocation(tzid, child) })
		}
	}
	if len(z) > 0 {
		z.bind(cal)
	}
}

func (z zones) bind(comp *Component) {
	for _, props := range comp.Props {
		for i := range props {
			if props[i].Params.Get(ParamTimezoneID) != "" {
				props[i].zones = z
			}
		}
	}
	for _, child := range comp.Children {
		z.bind(child)
	}
}

// location is the zone tzid names: the one the time zone database knows by
// that name, else the VTIMEZONE of the property's calendar. It is nil for a
// TZID neither defines, whose time is then read as floating: the wall clock
// is all the value says, and UTC would claim an offset it does not.
func (prop *Prop) location(tzid string) (*time.Location, error) {
	if loc, err := time.LoadLocation(tzid); err == nil {
		return loc, nil
	}
	if zone, ok := prop.zones[tzid]; ok {
		return zone()
	}
	return nil, nil
}

// zoneHorizon is how far a VTIMEZONE whose rules have no POSIX TZ form is
// spelled out; later times keep its last offset. It lies past any date a
// calendar is kept for.
var zoneHorizon = time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC)

// maxZoneTransitions bounds the onsets a VTIMEZONE is spelled out to. A
// real zone changes a few times a year; far more is a malformed rule,
// which must not exhaust memory.
const maxZoneTransitions = 4096

type zoneType struct {
	offset int
	dst    bool
	name   string
}

type transition struct {
	at   time.Time
	zone zoneType
}

// observance is a STANDARD or DAYLIGHT component read: the offsets it
// changes between and its onsets as instants.
type observance struct {
	zone zoneType
	from int
	set  recur.Set
}

// timezoneLocation reads tz, a VTIMEZONE, into a location named tzid
// (RFC 5545 section 3.6.5). Its onsets are written as TZif transitions
// (RFC 8536); where the rules still in force have a POSIX TZ form, that
// covers the times after them.
func timezoneLocation(tzid string, tz *Component) (*time.Location, error) {
	var observances []observance
	for _, comp := range tz.Children {
		if comp.Name != CompTimezoneStandard && comp.Name != CompTimezoneDaylight {
			continue
		}
		o, err := readObservance(comp)
		if err != nil {
			return nil, fmt.Errorf("ical: VTIMEZONE %q: %v", tzid, err)
		}
		observances = append(observances, o)
	}
	if len(observances) == 0 {
		return nil, fmt.Errorf("ical: VTIMEZONE %q has no observance", tzid)
	}

	footer, ok := posixZone(observances)
	end := zoneHorizon
	if ok {
		// Every observance has begun, and those in force again once.
		var last time.Time
		for _, o := range observances {
			if o.set.Start.After(last) {
				last = o.set.Start.Time
			}
		}
		end = last.AddDate(1, 0, 1)
	}

	var ts []transition
	for _, o := range observances {
		onsets, err := o.set.Between(o.set.Start.Time, end)
		if err != nil {
			return nil, fmt.Errorf("ical: VTIMEZONE %q: %v", tzid, err)
		}
		for at := range onsets {
			if len(ts) == maxZoneTransitions {
				return nil, fmt.Errorf("ical: VTIMEZONE %q changes more than %d times", tzid, maxZoneTransitions)
			}
			ts = append(ts, transition{at, o.zone})
		}
	}
	if len(ts) == 0 {
		return nil, fmt.Errorf("ical: VTIMEZONE %q has no onset before %d", tzid, end.Year())
	}
	slices.SortStableFunc(ts, func(a, b transition) int { return a.at.Compare(b.at) })

	first := ts[0].at
	var before zoneType
	for _, o := range observances {
		if o.set.Start.Time.Equal(first) {
			before = zoneType{offset: o.from, name: formatUTCOffset(o.from)}
		}
	}
	data, err := tzif(before, ts, footer)
	if err != nil {
		return nil, fmt.Errorf("ical: VTIMEZONE %q: %v", tzid, err)
	}
	return time.LoadLocationFromTZData(tzid, data)
}

func readObservance(comp *Component) (observance, error) {
	from, err := parseUTCOffset(comp.Props.Get(PropTimezoneOffsetFrom))
	if err != nil {
		return observance{}, err
	}
	to, err := parseUTCOffset(comp.Props.Get(PropTimezoneOffsetTo))
	if err != nil {
		return observance{}, err
	}
	name, _ := comp.Props.Text(PropTimezoneName)
	if name == "" {
		name = formatUTCOffset(to)
	}
	// The onsets are local times in the offset before them.
	set, err := comp.recurrenceSet(time.FixedZone("", from))
	if err != nil {
		return observance{}, err
	}
	if set.Start.IsZero() {
		return observance{}, fmt.Errorf("%s without DTSTART", comp.Name)
	}
	return observance{zone: zoneType{to, comp.Name == CompTimezoneDaylight, name}, from: from, set: set}, nil
}

func parseUTCOffset(prop *Prop) (int, error) {
	if prop == nil {
		return 0, fmt.Errorf("missing UTC offset")
	}
	v := prop.Value
	if (len(v) != 5 && len(v) != 7) || (v[0] != '+' && v[0] != '-') {
		return 0, fmt.Errorf("invalid UTC offset %q", v)
	}
	sec := 0
	for i, unit := range []int{3600, 60, 1} {
		if 1+2*i >= len(v) {
			break
		}
		n, err := strconv.Atoi(v[1+2*i : 3+2*i])
		if err != nil {
			return 0, fmt.Errorf("invalid UTC offset %q", v)
		}
		sec += n * unit
	}
	if v[0] == '-' {
		sec = -sec
	}
	return sec, nil
}

// posixZone is the POSIX TZ string (IEEE 1003.1 section 8.3) for the rules
// of observances still in force: one STANDARD and one DAYLIGHT, each
// yearly on a weekday of a week of a month.
func posixZone(observances []observance) (string, bool) {
	var std, dst *observance
	for i, o := range observances {
		if o.set.Rule == nil || o.set.Rule.Count > 0 || o.set.Rule.Until != nil {
			continue
		}
		switch {
		case o.zone.dst && dst == nil:
			dst = &observances[i]
		case !o.zone.dst && std == nil:
			std = &observances[i]
		default:
			return "", false
		}
	}
	if std == nil || dst == nil {
		return "", false
	}
	start, ok := posixDate(dst)
	if !ok {
		return "", false
	}
	end, ok := posixDate(std)
	if !ok {
		return "", false
	}
	return posixName(std.zone) + posixOffset(std.zone.offset) + posixName(dst.zone) + posixOffset(dst.zone.offset) +
		"," + start + "," + end, true
}

func posixDate(o *observance) (string, bool) {
	r := o.set.Rule
	if r.Freq != recur.Yearly || r.Interval > 1 || r.Scale != "" && r.Scale != "GREGORIAN" ||
		len(r.ByMonth) != 1 || r.ByMonth[0].Leap || len(r.ByDay) != 1 ||
		len(r.BySecond)+len(r.ByMinute)+len(r.ByHour)+len(r.ByMonthDay)+len(r.ByYearDay)+len(r.ByWeekNo)+len(r.BySetPos) > 0 {
		return "", false
	}
	week := r.ByDay[0].N
	switch {
	case week == -1:
		week = 5
	case week < 1 || week > 4:
		return "", false
	}
	h, m, s := o.set.Start.Clock()
	return fmt.Sprintf("M%d.%d.%d/%d:%02d:%02d", r.ByMonth[0].N, week, int(r.ByDay[0].Day), h, m, s), true
}

var posixPlainName = regexp.MustCompile(`^[A-Za-z]{3,}$`)
var posixQuotedName = regexp.MustCompile(`^[A-Za-z0-9+-]{3,}$`)

func posixName(z zoneType) string {
	switch {
	case posixPlainName.MatchString(z.name):
		return z.name
	case posixQuotedName.MatchString(z.name):
		return "<" + z.name + ">"
	}
	return "<" + formatUTCOffset(z.offset) + ">"
}

// posixOffset is an offset as POSIX writes it: the time to add to local
// time to reach UTC.
func posixOffset(sec int) string {
	sign := ""
	if sec > 0 {
		sign = "-"
	} else {
		sec = -sec
	}
	return fmt.Sprintf("%s%d:%02d:%02d", sign, sec/3600, sec%3600/60, sec%60)
}

// tzif writes a version 2 TZif file (RFC 8536): the zone before the
// first transition, the transitions, and the footer for times after.
func tzif(before zoneType, ts []transition, footer string) ([]byte, error) {
	types := []zoneType{before}
	var names strings.Builder
	desig := map[string]int{}
	index := func(z zoneType) byte {
		i := slices.Index(types[1:], z)
		if i < 0 {
			types = append(types, z)
			return byte(len(types) - 1)
		}
		return byte(i + 1)
	}
	var times []int64
	var idx []byte
	for _, t := range ts {
		i := index(t.zone)
		if n := len(times); n > 0 && times[n-1] == t.at.Unix() {
			idx[n-1] = i
			continue
		}
		times = append(times, t.at.Unix())
		idx = append(idx, i)
	}
	// A transition names its type in one byte.
	if len(types) > 256 {
		return nil, fmt.Errorf("%d kinds of time, more than TZif holds", len(types))
	}
	for _, z := range types {
		if _, ok := desig[z.name]; !ok {
			desig[z.name] = names.Len()
			names.WriteString(z.name + "\x00")
		}
	}
	// A type names its abbreviation by a one-byte index.
	if names.Len() > 256 {
		return nil, fmt.Errorf("%d bytes of zone names, more than TZif indexes", names.Len())
	}

	header := func(b []byte, timecnt, typecnt, charcnt int) []byte {
		b = append(b, "TZif2"...)
		b = append(b, make([]byte, 15)...)
		for _, n := range []int{0, 0, 0, timecnt, typecnt, charcnt} {
			b = binary.BigEndian.AppendUint32(b, uint32(n))
		}
		return b
	}
	// An empty first part, as version 2 readers skip it.
	b := header(nil, 0, 1, 1)
	b = append(b, make([]byte, 6+1)...)
	b = header(b, len(times), len(types), names.Len())
	for _, t := range times {
		b = binary.BigEndian.AppendUint64(b, uint64(t))
	}
	b = append(b, idx...)
	for _, z := range types {
		b = binary.BigEndian.AppendUint32(b, uint32(int32(z.offset)))
		dst := byte(0)
		if z.dst {
			dst = 1
		}
		b = append(b, dst, byte(desig[z.name]))
	}
	b = append(b, names.String()...)
	return append(b, "\n"+footer+"\n"...), nil
}
