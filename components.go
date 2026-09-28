package ical

import (
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Calendar is the top-level iCalendar object.
type Calendar struct {
	*Component
}

// RecurrenceSet returns the Recurrence Set for this component.
func (comp *Component) RecurrenceSet(loc *time.Location) (*rrule.Set, error) {
	roption, err := comp.Props.RecurrenceRule()
	if err != nil {
		return nil, fmt.Errorf("ical: error parsing recurrence: %v", err)
	}
	if roption == nil {
		return nil, nil
	}
	dateTime, err := comp.Props.DateTime(PropDateTimeStart, loc)
	if err != nil {
		return nil, fmt.Errorf("ical: error parsing start time: %v", err)
	}

	rule, err := rrule.NewRRule(*roption)
	if err != nil {
		return nil, fmt.Errorf("ical: error buildling rrule: %v", err)
	}

	ruleSet := rrule.Set{}
	ruleSet.RRule(rule)
	ruleSet.DTStart(dateTime)

	for _, exdateProp := range comp.Props[PropExceptionDates] {
		exdate, err := exdateProp.DateTime(loc)
		if err != nil {
			return nil, fmt.Errorf("ical: error parsing exdate: %v", err)
		}
		ruleSet.ExDate(exdate)
	}
	for _, rdateProp := range comp.Props[PropRecurrenceDates] {
		rdate, err := rdateProp.DateTime(loc)
		if err != nil {
			return nil, fmt.Errorf("ical: error parsing rdate: %v", err)
		}
		ruleSet.RDate(rdate)
	}

	return &ruleSet, nil
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
