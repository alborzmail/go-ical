package ical

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alborzmail/go-recur"
)

// Calendar is the top-level iCalendar object.
type Calendar struct {
	*Component
}

// RecurrenceSet returns the recurrence set of comp, or nil if it has neither
// RRULE nor RDATE. Floating times and dates are read in loc.
func (comp *Component) RecurrenceSet(loc *time.Location) (*recur.Set, error) {
	if comp.Props.Get(PropRecurrenceRule) == nil && comp.Props.Get(PropRecurrenceDates) == nil {
		return nil, nil
	}
	set, err := comp.recurrenceSet(loc)
	if err != nil {
		return nil, err
	}
	return &set, nil
}

// recurrenceSet returns the recurrence set of comp, which for a component
// without RRULE or RDATE is its DTSTART alone, and empty without DTSTART.
func (comp *Component) recurrenceSet(loc *time.Location) (recur.Set, error) {
	var set recur.Set
	rule, err := comp.Props.RecurrenceRule()
	if err != nil {
		return set, err
	}
	dtstart := comp.Props.Get(PropDateTimeStart)
	if dtstart == nil {
		if rule != nil || comp.Props.Get(PropRecurrenceDates) != nil {
			return set, fmt.Errorf("ical: %s recurs without DTSTART", comp.Name)
		}
		return set, nil
	}
	set.Rule = rule
	if set.Start, err = dtstart.value(loc); err != nil {
		return set, fmt.Errorf("ical: error parsing start time: %v", err)
	}
	for _, prop := range comp.Props[PropRecurrenceDates] {
		for _, v := range prop.list() {
			var r recur.Period
			if prop.ValueType() == ValuePeriod {
				r, err = v.period(loc)
			} else {
				r.Start, err = v.value(loc)
			}
			if err != nil {
				return set, fmt.Errorf("ical: error parsing rdate: %v", err)
			}
			set.RDate = append(set.RDate, r)
		}
	}
	for _, prop := range comp.Props[PropExceptionDates] {
		for _, v := range prop.list() {
			exdate, err := v.value(loc)
			if err != nil {
				return set, fmt.Errorf("ical: error parsing exdate: %v", err)
			}
			set.ExDate = append(set.ExDate, exdate)
		}
	}
	return set, nil
}

// list splits a property holding several values, as RDATE and EXDATE may,
// into one property per value.
func (prop *Prop) list() []*Prop {
	var l []*Prop
	for _, v := range strings.Split(prop.Value, ",") {
		l = append(l, &Prop{Name: prop.Name, Params: prop.Params, Value: v})
	}
	return l
}

// value reads a DATE or DATE-TIME as the recurrence engine takes it.
func (prop *Prop) value(loc *time.Location) (recur.Value, error) {
	t, err := prop.DateTime(loc)
	kind := recur.Floating
	switch {
	case prop.ValueType() == ValueDate:
		kind = recur.Date
	case strings.HasSuffix(prop.Value, "Z"), prop.Params.Get(ParamTimezoneID) != "":
		kind = recur.DateTime
	}
	return recur.Value{Time: t, Kind: kind}, err
}

// period reads a PERIOD: a start and its end or duration (RFC 5545
// section 3.3.9).
func (prop *Prop) period(loc *time.Location) (recur.Period, error) {
	var r recur.Period
	start, end, ok := strings.Cut(prop.Value, "/")
	if !ok {
		return r, fmt.Errorf("ical: invalid period: %q", prop.Value)
	}
	at := &Prop{Name: prop.Name, Params: Params{}, Value: start}
	if tzid := prop.Params.Get(ParamTimezoneID); tzid != "" {
		at.Params.Set(ParamTimezoneID, tzid)
	}
	var err error
	if r.Start, err = at.value(loc); err != nil {
		return r, err
	}
	if strings.HasPrefix(strings.TrimLeft(end, "+-"), "P") {
		p := durationParser{strings.ToUpper(end)}
		d, err := p.parseDuration()
		r.End = recur.Value{Time: r.Start.Add(d), Kind: r.Start.Kind}
		return r, err
	}
	at.Value = end
	r.End, err = at.value(loc)
	return r, err
}

// TriggerTime returns when alarm, a VALARM of the VEVENT or VTODO comp, is
// triggered (RFC 5545 section 3.8.6.3). A trigger related to the end uses
// the event's end or the to-do's due time.
func (comp *Component) TriggerTime(alarm *Component, loc *time.Location) (time.Time, error) {
	trigger := alarm.Props.Get(PropTrigger)
	if trigger == nil {
		return time.Time{}, fmt.Errorf("ical: missing TRIGGER")
	}
	if trigger.ValueType() == ValueDateTime {
		return trigger.DateTime(loc)
	}
	offset, err := trigger.Duration()
	if err != nil {
		return time.Time{}, err
	}

	related := strings.ToUpper(trigger.Params.Get(ParamRelated))
	var base time.Time
	if related == "END" {
		base, err = comp.end(loc)
	} else {
		base, err = comp.Props.DateTime(PropDateTimeStart, loc)
	}
	if err != nil {
		return time.Time{}, err
	}
	if base.IsZero() {
		return time.Time{}, fmt.Errorf("ical: TRIGGER related to a missing %s", comp.Name)
	}
	return base.Add(offset), nil
}

// end returns the end of a VEVENT or the due time of a VTODO, or the zero
// time if it has none.
func (comp *Component) end(loc *time.Location) (time.Time, error) {
	switch comp.Name {
	case CompEvent:
		return (&Event{comp}).DateTimeEnd(loc)
	case CompToDo:
		if comp.Props.Get(PropDue) != nil {
			return comp.Props.DateTime(PropDue, loc)
		}
		duration := comp.Props.Get(PropDuration)
		if duration == nil {
			return time.Time{}, nil
		}
		start, err := comp.Props.DateTime(PropDateTimeStart, loc)
		if err != nil || start.IsZero() {
			return time.Time{}, err
		}
		d, err := duration.Duration()
		if err != nil {
			return time.Time{}, err
		}
		return start.Add(d), nil
	}
	return time.Time{}, fmt.Errorf("ical: %s has no end", comp.Name)
}

// NewCalendar creates a new calendar object.
func NewCalendar() *Calendar {
	return &Calendar{NewComponent(CompCalendar)}
}

// Events extracts the list of events contained in the calendar.
func (cal *Calendar) Events() []Event {
	l := make([]Event, 0, len(cal.Children))
	for _, child := range cal.Children {
		if child.Name == CompEvent {
			l = append(l, Event{child})
		}
	}
	return l
}

// AddTimezones adds a VTIMEZONE built by NewTimezone from start to end for
// each TZID the calendar refers to but doesn't define, as RFC 5545 section
// 3.2.19 requires. A TZID unknown to time.LoadLocation is left alone.
func (cal *Calendar) AddTimezones(start, end time.Time) {
	defined := make(map[string]bool)
	used := make(map[string]bool)
	for _, child := range cal.Children {
		if child.Name == CompTimezone {
			tzid, _ := child.Props.Text(PropTimezoneID)
			defined[tzid] = true
		} else {
			addTimezoneIDs(used, child)
		}
	}

	var missing []string
	for tzid := range used {
		if !defined[tzid] {
			missing = append(missing, tzid)
		}
	}
	sort.Strings(missing)

	for _, tzid := range missing {
		loc, err := time.LoadLocation(tzid)
		if err != nil {
			continue
		}
		cal.Children = append(cal.Children, NewTimezone(loc, start, end))
	}
}

// SplitByUID returns one calendar per UID, so that a recurring component
// stays with its RECURRENCE-ID overrides (RFC 5545 section 3.8.4.4). Each
// carries the calendar's properties and the VTIMEZONE components it refers
// to. Every component but VTIMEZONE must have a UID.
func (cal *Calendar) SplitByUID() (map[string]*Calendar, error) {
	zones := make(map[string]*Component)
	objects := make(map[string]*Calendar)
	for _, child := range cal.Children {
		if child.Name == CompTimezone {
			tzid, _ := child.Props.Text(PropTimezoneID)
			zones[tzid] = child
			continue
		}
		uid, err := child.Props.Text(PropUID)
		if err != nil {
			return nil, err
		}
		if uid == "" {
			return nil, fmt.Errorf("ical: %s without UID", child.Name)
		}
		object, ok := objects[uid]
		if !ok {
			object = NewCalendar()
			for name, props := range cal.Props {
				object.Props[name] = append([]Prop(nil), props...)
			}
			objects[uid] = object
		}
		object.Children = append(object.Children, child)
	}

	for _, object := range objects {
		used := make(map[string]bool)
		for _, child := range object.Children {
			addTimezoneIDs(used, child)
		}
		tzids := make([]string, 0, len(used))
		for tzid := range used {
			tzids = append(tzids, tzid)
		}
		sort.Strings(tzids)

		var children []*Component
		for _, tzid := range tzids {
			if zone, ok := zones[tzid]; ok {
				children = append(children, zone)
			}
		}
		object.Children = append(children, object.Children...)
	}
	return objects, nil
}

// RemoveDuplicateTimezones keeps the first VTIMEZONE of each TZID, which
// must be unique within a calendar (RFC 5545 section 3.8.3.1), as when the
// components of several calendars are joined into one.
func (cal *Calendar) RemoveDuplicateTimezones() {
	seen := make(map[string]bool)
	children := cal.Children[:0]
	for _, child := range cal.Children {
		if child.Name == CompTimezone {
			tzid, _ := child.Props.Text(PropTimezoneID)
			if seen[tzid] {
				continue
			}
			seen[tzid] = true
		}
		children = append(children, child)
	}
	cal.Children = children
}

// addTimezoneIDs adds the TZID parameters used in comp and its children to ids.
func addTimezoneIDs(ids map[string]bool, comp *Component) {
	for _, props := range comp.Props {
		for _, prop := range props {
			if tzid := prop.Params.Get(ParamTimezoneID); tzid != "" {
				ids[tzid] = true
			}
		}
	}
	for _, child := range comp.Children {
		addTimezoneIDs(ids, child)
	}
}

// Event represents a scheduled amount of time on a calendar.
type Event struct {
	*Component
}

// NewEvent creates a new event.
func NewEvent() *Event {
	return &Event{NewComponent(CompEvent)}
}

// DateTimeStart returns the inclusive start of the event.
func (e *Event) DateTimeStart(loc *time.Location) (time.Time, error) {
	return e.Props.DateTime(PropDateTimeStart, loc)
}

// DateTimeEnd returns the non-inclusive end of the event.
func (e *Event) DateTimeEnd(loc *time.Location) (time.Time, error) {
	if prop := e.Props.Get(PropDateTimeEnd); prop != nil {
		return prop.DateTime(loc)
	}

	startProp := e.Props.Get(PropDateTimeStart)
	if startProp == nil {
		return time.Time{}, nil
	}

	start, err := startProp.DateTime(loc)
	if err != nil {
		return time.Time{}, err
	}

	var dur time.Duration
	if durProp := e.Props.Get(PropDuration); durProp != nil {
		dur, err = durProp.Duration()
		if err != nil {
			return time.Time{}, err
		}
	} else if startProp.ValueType() == ValueDate {
		dur = 24 * time.Hour
	}

	return start.Add(dur), nil
}

func (e *Event) Status() (EventStatus, error) {
	s, err := e.Props.Text(PropStatus)
	if err != nil {
		return "", err
	}

	switch status := EventStatus(strings.ToUpper(s)); status {
	case "", EventTentative, EventConfirmed, EventCancelled:
		return status, nil
	default:
		return "", fmt.Errorf("ical: invalid VEVENT STATUS: %q", status)
	}
}

func (e *Event) SetStatus(status EventStatus) {
	if status == "" {
		e.Props.Del(PropStatus)
	} else {
		e.Props.SetText(PropStatus, string(status))
	}
}

// NewTimezone creates a VTIMEZONE component describing loc from start to end,
// as defined in RFC 5545 section 3.6.5. The time zone identifier is
// loc.String().
//
// The component contains the observance in effect at start, followed by one
// STANDARD or DAYLIGHT observance per zone transition up to end. If the span
// holds both kinds and the zone keeps changing after end, the last observance
// of each kind gets a yearly RRULE inferred from its onset, such as the last
// Sunday of March, so that later times are described too.
func NewTimezone(loc *time.Location, start, end time.Time) *Component {
	tz := NewComponent(CompTimezone)
	tz.Props.SetText(PropTimezoneID, loc.String())

	last := make(map[string]*Component)
	t := start.In(loc)
	for {
		onset, next := t.ZoneBounds()
		observance := newObservance(t, onset)
		tz.Children = append(tz.Children, observance)
		if !onset.IsZero() {
			last[observance.Name] = observance
		}
		if next.IsZero() {
			return tz
		}
		if next.After(end) {
			break
		}
		t = next
	}

	if len(last) == 2 {
		for _, observance := range last {
			observance.Props.Set(yearlyRule(observance))
		}
	}
	return tz
}

// yearlyRule returns the RRULE repeating the onset of observance on the same
// weekday of the same week of its month: the last Sunday of March rather than
// the 29th.
func yearlyRule(observance *Component) *Prop {
	onset, err := observance.Props.DateTime(PropDateTimeStart, time.UTC)
	if err != nil {
		panic(err)
	}

	week := (onset.Day()-1)/7 + 1
	daysInMonth := time.Date(onset.Year(), onset.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if onset.Day()+7 > daysInMonth {
		week = -1
	}
	weekday := strings.ToUpper(onset.Weekday().String()[:2])

	rule := NewProp(PropRecurrenceRule)
	rule.Value = fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", onset.Month(), week, weekday)
	return rule
}

// newObservance creates the STANDARD or DAYLIGHT component in effect at t,
// which began at onset. A zero onset means the observance has no beginning,
// it's then described from t on.
func newObservance(t, onset time.Time) *Component {
	name, to := t.Zone()
	from := to
	if onset.IsZero() {
		onset = t
	} else {
		_, from = onset.Add(-time.Second).Zone()
	}

	kind := CompTimezoneStandard
	if t.IsDST() {
		kind = CompTimezoneDaylight
	}
	comp := NewComponent(kind)

	// The onset is a local time using the offset in effect before the change
	dtstart := NewProp(PropDateTimeStart)
	dtstart.Value = onset.In(time.FixedZone("", from)).Format(datetimeFormat)
	comp.Props.Set(dtstart)

	offsetFrom := NewProp(PropTimezoneOffsetFrom)
	offsetFrom.Value = formatUTCOffset(from)
	comp.Props.Set(offsetFrom)

	offsetTo := NewProp(PropTimezoneOffsetTo)
	offsetTo.Value = formatUTCOffset(to)
	comp.Props.Set(offsetTo)

	comp.Props.SetText(PropTimezoneName, name)

	return comp
}

func formatUTCOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign = "-"
		sec = -sec
	}
	s := fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
	if sec%60 != 0 {
		s += fmt.Sprintf("%02d", sec%60)
	}
	return s
}
