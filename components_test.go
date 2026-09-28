package ical

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/teambition/rrule-go"
)

func TestRecurrenceSet(t *testing.T) {
	events := exampleCalendar.Events()
	if len(events) != 1 {
		t.Fatalf("len(Calendar.Events()) = %v, want 1", len(events))
	}
	event := events[0]

	wantRecurrenceSet := &rrule.Set{}
	rrule, err := rrule.NewRRule(rrule.ROption{
		Freq:      rrule.YEARLY,
		Bymonth:   []int{3},
		Byweekday: []rrule.Weekday{rrule.SU.Nth(3)},
	})
	if err != nil {
		t.Errorf("Could not build rrule: %v", err) // Should never really happen.
	}
	wantRecurrenceSet.DTStart(time.Date(1996, 9, 18, 14, 30, 0, 0, time.UTC))
	wantRecurrenceSet.RRule(rrule)

	if gotRecurrenceSet, err := event.RecurrenceSet(nil); err != nil {
		t.Errorf("Props.RecurrenceSet() = %v", err)
	} else if !reflect.DeepEqual(gotRecurrenceSet, wantRecurrenceSet) {
		t.Errorf("Props.RecurrenceSet() = %v, want %v", gotRecurrenceSet, wantRecurrenceSet)
	}
}

func TestRecurrenceSetIsAbsent(t *testing.T) {
	event := Component{}
	gotRecurrenceSet, err := event.RecurrenceSet(nil)

	if gotRecurrenceSet != nil || err != nil {
		t.Errorf("Component.RecurrenceSet() = %v, %v, want nil, nil", gotRecurrenceSet, err)
	}
}

func TestRecurrenceSetWithRDate(t *testing.T) {
	// It creates an event with a daily recurrence rule for 2 days, but also
	// adds a single, separate recurrence date (RDATE).
	event := &Component{
		Name: CompEvent,
		Props: Props{
			PropDateTimeStart: []Prop{{
				Name:  PropDateTimeStart,
				Value: "20230101T100000Z",
			}},
			PropRecurrenceRule: []Prop{{
				Name:  PropRecurrenceRule,
				Value: "FREQ=DAILY;COUNT=2",
			}},
			PropRecurrenceDates: []Prop{{
				Name:  PropRecurrenceDates,
				Value: "20230110T100000Z",
			}},
		},
	}

	// 1. Get the recurrence set from the component
	gotRecurrenceSet, err := event.RecurrenceSet(time.UTC)
	if err != nil {
		t.Fatalf("Component.RecurrenceSet() returned an unexpected error: %v", err)
	}
	if gotRecurrenceSet == nil {
		t.Fatal("Component.RecurrenceSet() returned nil, but a set was expected")
	}

	// 2. Define the expected occurrences
	// The RRULE generates Jan 1 and Jan 2. The RDATE adds Jan 10.
	wantOccurrences := []time.Time{
		time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2023, 1, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2023, 1, 10, 10, 0, 0, 0, time.UTC),
	}

	// 3. Get the actual occurrences from the generated set
	gotOccurrences := gotRecurrenceSet.All()

	// 4. Compare the results
	if !reflect.DeepEqual(gotOccurrences, wantOccurrences) {
		t.Errorf("RecurrenceSet did not process RDATE correctly.\n got: %v\nwant: %v", gotOccurrences, wantOccurrences)
	}
}

func TestNewTimezone(t *testing.T) {
	type observance struct {
		kind, start, from, to, name, rule string
	}

	tests := []struct {
		tzid string
		want []observance
	}{
		{
			tzid: "Europe/Berlin",
			want: []observance{
				{CompTimezoneStandard, "20251026T030000", "+0200", "+0100", "CET", ""},
				{CompTimezoneDaylight, "20260329T020000", "+0100", "+0200", "CEST", "FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU"},
				{CompTimezoneStandard, "20261025T030000", "+0200", "+0100", "CET", "FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU"},
			},
		},
		{
			tzid: "America/New_York",
			want: []observance{
				{CompTimezoneStandard, "20251102T020000", "-0400", "-0500", "EST", ""},
				{CompTimezoneDaylight, "20260308T020000", "-0500", "-0400", "EDT", "FREQ=YEARLY;BYMONTH=3;BYDAY=2SU"},
				{CompTimezoneStandard, "20261101T020000", "-0400", "-0500", "EST", "FREQ=YEARLY;BYMONTH=11;BYDAY=1SU"},
			},
		},
		{
			tzid: "Australia/Sydney",
			want: []observance{
				{CompTimezoneDaylight, "20251005T020000", "+1000", "+1100", "AEDT", ""},
				{CompTimezoneStandard, "20260405T030000", "+1100", "+1000", "AEST", "FREQ=YEARLY;BYMONTH=4;BYDAY=1SU"},
				{CompTimezoneDaylight, "20261004T020000", "+1000", "+1100", "AEDT", "FREQ=YEARLY;BYMONTH=10;BYDAY=1SU"},
			},
		},
		{
			// No DST since 2022
			tzid: "Asia/Tehran",
			want: []observance{
				{CompTimezoneStandard, "20220922T000000", "+0430", "+0330", "+0330", ""},
			},
		},
		{
			tzid: "UTC",
			want: []observance{
				{CompTimezoneStandard, "20260101T000000", "+0000", "+0000", "UTC", ""},
			},
		},
	}

	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)

	for _, test := range tests {
		loc, err := time.LoadLocation(test.tzid)
		if err != nil {
			t.Fatalf("time.LoadLocation(%q) = %v", test.tzid, err)
		}

		tz := NewTimezone(loc, start, end)
		if err := checkComponent(tz); err != nil {
			t.Errorf("checkComponent(NewTimezone(%q)) = %v", test.tzid, err)
		}
		if tzid, _ := tz.Props.Text(PropTimezoneID); tzid != test.tzid {
			t.Errorf("NewTimezone(%q): TZID = %q", test.tzid, tzid)
		}

		var got []observance
		for _, child := range tz.Children {
			if err := checkComponent(child); err != nil {
				t.Errorf("checkComponent(NewTimezone(%q) child) = %v", test.tzid, err)
			}
			name, _ := child.Props.Text(PropTimezoneName)
			var rule string
			if prop := child.Props.Get(PropRecurrenceRule); prop != nil {
				rule = prop.Value
			}
			got = append(got, observance{
				kind:  child.Name,
				start: child.Props.Get(PropDateTimeStart).Value,
				from:  child.Props.Get(PropTimezoneOffsetFrom).Value,
				to:    child.Props.Get(PropTimezoneOffsetTo).Value,
				name:  name,
				rule:  rule,
			})
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("NewTimezone(%q) = %v, want %v", test.tzid, got, test.want)
		}
	}
}

func TestCalendarAddTimezones(t *testing.T) {
	cal := NewCalendar()
	event := NewEvent()
	for _, tzid := range []string{"Europe/Berlin", "America/New_York", "Mars/Olympus"} {
		prop := NewProp(PropDateTimeStart)
		prop.Params.Set(ParamTimezoneID, tzid)
		prop.Value = "20260601T100000"
		event.Props.Add(prop)
	}
	// Nested as in RFC 7953
	availability := NewComponent("VAVAILABILITY")
	available := NewComponent("AVAILABLE")
	dtstart := NewProp(PropDateTimeStart)
	dtstart.Params.Set(ParamTimezoneID, "Asia/Tokyo")
	dtstart.Value = "20260601T090000"
	available.Props.Set(dtstart)
	availability.Children = append(availability.Children, available)
	own := NewComponent(CompTimezone)
	own.Props.SetText(PropTimezoneID, "Europe/Berlin")
	cal.Children = append(cal.Children, own, event.Component, availability)

	cal.AddTimezones(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC))

	var got []string
	for _, child := range cal.Children {
		if child.Name == CompTimezone {
			tzid, _ := child.Props.Text(PropTimezoneID)
			got = append(got, tzid)
		}
	}
	want := []string{"Europe/Berlin", "America/New_York", "Asia/Tokyo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Calendar.AddTimezones() gave VTIMEZONEs %v, want %v", got, want)
	}
	if len(own.Children) != 0 {
		t.Errorf("Calendar.AddTimezones() changed the VTIMEZONE the calendar had")
	}
}

func TestComponentTriggerTime(t *testing.T) {
	decode := func(s string) *Component {
		cal, err := NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
		if err != nil {
			t.Fatalf("Decode() = %v", err)
		}
		return cal.Children[0]
	}
	event := decode(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VEVENT
UID:a
DTSTAMP:20260101T000000Z
DTSTART:20260601T100000Z
DTEND:20260601T110000Z
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER:-PT15M
END:VALARM
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;RELATED=END:PT5M
END:VALARM
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;VALUE=DATE-TIME:20260531T080000Z
END:VALARM
END:VEVENT
END:VCALENDAR
`)
	todo := decode(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:test
BEGIN:VTODO
UID:b
DTSTAMP:20260101T000000Z
DTSTART:20260601T100000Z
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;RELATED=END:-PT1H
END:VALARM
END:VTODO
END:VCALENDAR
`)

	want := []time.Time{
		time.Date(2026, time.June, 1, 9, 45, 0, 0, time.UTC),
		time.Date(2026, time.June, 1, 11, 5, 0, 0, time.UTC),
		time.Date(2026, time.May, 31, 8, 0, 0, 0, time.UTC),
	}
	for i, alarm := range event.Children {
		got, err := event.TriggerTime(alarm, nil)
		if err != nil || !got.Equal(want[i]) {
			t.Errorf("VEVENT TriggerTime(alarm %d) = %v, %v, want %v", i, got, err, want[i])
		}
	}

	// A to-do without DUE has no end to be related to.
	if got, err := todo.TriggerTime(todo.Children[0], nil); err == nil {
		t.Errorf("VTODO TriggerTime() = %v, want an error", got)
	}
	todo.Props.SetDateTime(PropDue, time.Date(2026, time.June, 2, 10, 0, 0, 0, time.UTC))
	got, err := todo.TriggerTime(todo.Children[0], nil)
	if want := time.Date(2026, time.June, 2, 9, 0, 0, 0, time.UTC); err != nil || !got.Equal(want) {
		t.Errorf("VTODO TriggerTime() = %v, %v, want %v", got, err, want)
	}
}
