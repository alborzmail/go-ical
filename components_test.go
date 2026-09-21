package ical

import (
	"reflect"
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
		kind, start, from, to, name string
	}

	tests := []struct {
		tzid string
		want []observance
	}{
		{
			tzid: "Europe/Berlin",
			want: []observance{
				{CompTimezoneStandard, "20251026T030000", "+0200", "+0100", "CET"},
				{CompTimezoneDaylight, "20260329T020000", "+0100", "+0200", "CEST"},
				{CompTimezoneStandard, "20261025T030000", "+0200", "+0100", "CET"},
			},
		},
		{
			tzid: "Australia/Sydney",
			want: []observance{
				{CompTimezoneDaylight, "20251005T020000", "+1000", "+1100", "AEDT"},
				{CompTimezoneStandard, "20260405T030000", "+1100", "+1000", "AEST"},
				{CompTimezoneDaylight, "20261004T020000", "+1000", "+1100", "AEDT"},
			},
		},
		{
			// No DST since 2022
			tzid: "Asia/Tehran",
			want: []observance{
				{CompTimezoneStandard, "20220922T000000", "+0430", "+0330", "+0330"},
			},
		},
		{
			tzid: "UTC",
			want: []observance{
				{CompTimezoneStandard, "20260101T000000", "+0000", "+0000", "UTC"},
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
			got = append(got, observance{
				kind:  child.Name,
				start: child.Props.Get(PropDateTimeStart).Value,
				from:  child.Props.Get(PropTimezoneOffsetFrom).Value,
				to:    child.Props.Get(PropTimezoneOffsetTo).Value,
				name:  name,
			})
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("NewTimezone(%q) = %v, want %v", test.tzid, got, test.want)
		}
	}
}
