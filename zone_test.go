package ical

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

// Outlook names zones as Windows does, which no time zone database knows,
// and defines them by a VTIMEZONE.
const outlookCentral = `BEGIN:VTIMEZONE
TZID:Central Standard Time
BEGIN:STANDARD
DTSTART:16010101T020000
TZOFFSETFROM:-0500
TZOFFSETTO:-0600
RRULE:FREQ=YEARLY;INTERVAL=1;BYDAY=1SU;BYMONTH=11
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:16010101T020000
TZOFFSETFROM:-0600
TZOFFSETTO:-0500
RRULE:FREQ=YEARLY;INTERVAL=1;BYDAY=2SU;BYMONTH=3
END:DAYLIGHT
END:VTIMEZONE`

// The same rules as some servers write them, a week as a range of days,
// which POSIX TZ cannot say.
const centralByMonthDay = `BEGIN:VTIMEZONE
TZID:Central Standard Time
BEGIN:STANDARD
DTSTART:20071104T020000
TZOFFSETFROM:-0500
TZOFFSETTO:-0600
RRULE:FREQ=YEARLY;BYMONTH=11;BYMONTHDAY=1,2,3,4,5,6,7;BYDAY=SU
END:STANDARD
BEGIN:DAYLIGHT
DTSTART:20070311T020000
TZOFFSETFROM:-0600
TZOFFSETTO:-0500
RRULE:FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=8,9,10,11,12,13,14;BYDAY=SU
END:DAYLIGHT
END:VTIMEZONE`

func decodeCalendar(t *testing.T, body string) *Calendar {
	t.Helper()
	s := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//test//test//EN\n" + body + "\nEND:VCALENDAR\n"
	cal, err := NewDecoder(strings.NewReader(strings.ReplaceAll(s, "\n", "\r\n"))).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

// sameOffsets compares got with the time zone database's name hour by hour.
func sameOffsets(t *testing.T, got *time.Location, name string, from, to int) {
	t.Helper()
	want, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(to, time.January, 1, 0, 0, 0, 0, time.UTC)
	for at := time.Date(from, time.January, 1, 0, 0, 0, 0, time.UTC); at.Before(end); at = at.Add(time.Hour) {
		_, g := at.In(got).Zone()
		_, w := at.In(want).Zone()
		if g != w || at.In(got).IsDST() != at.In(want).IsDST() {
			t.Fatalf("at %v the offset is %d, dst %v; %s has %d, dst %v", at, g, at.In(got).IsDST(), name, w, at.In(want).IsDST())
		}
	}
}

func timezoneOf(t *testing.T, cal *Calendar) *time.Location {
	t.Helper()
	for _, child := range cal.Children {
		if child.Name == CompTimezone {
			tzid, _ := child.Props.Text(PropTimezoneID)
			loc, err := timezoneLocation(tzid, child)
			if err != nil {
				t.Fatal(err)
			}
			return loc
		}
	}
	t.Fatal("no VTIMEZONE")
	return nil
}

func TestTimezoneLocation(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	generated := func(loc *time.Location, from, to int) *Calendar {
		cal := NewCalendar()
		cal.Children = append(cal.Children, NewTimezone(loc, time.Date(from, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(to, 1, 1, 0, 0, 0, 0, time.UTC)))
		return cal
	}
	for _, tc := range []struct {
		name     string
		cal      *Calendar
		iana     string
		from, to int
	}{
		{"yearly rules with a POSIX form", decodeCalendar(t, outlookCentral), "America/Chicago", 2008, 2040},
		{"yearly rules without one", decodeCalendar(t, centralByMonthDay), "America/Chicago", 2008, 2040},
		{"rules changed over the years", generated(chicago, 1990, 2030), "America/Chicago", 1990, 2060},
		{"rules east of UTC", generated(berlin, 1990, 2030), "Europe/Berlin", 1990, 2060},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sameOffsets(t, timezoneOf(t, tc.cal), tc.iana, tc.from, tc.to)
		})
	}
}

func TestTimezoneDefinedInTheCalendar(t *testing.T) {
	cal := decodeCalendar(t, outlookCentral+`
BEGIN:VEVENT
UID:standup
DTSTAMP:20260101T000000Z
DTSTART;TZID=Central Standard Time:20260302T100000
DURATION:PT15M
RRULE:FREQ=WEEKLY;COUNT=3
SUMMARY:Standup
END:VEVENT`)
	series, err := cal.Series("standup", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	instances, err := series.Between(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for inst := range instances {
		got = append(got, inst.Start.UTC().Format("0102T1504"))
	}
	if want := "0302T1600 0309T1500 0316T1500"; strings.Join(got, " ") != want {
		t.Errorf("the standup falls at %v, want %s: 10:00 CST, then CDT from 8 March", got, want)
	}

	start, err := cal.Events()[0].DateTimeStart(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	moved := NewEvent()
	moved.Props.SetDateTime(PropDateTimeStart, start.AddDate(0, 0, 7))
	if got, err := moved.DateTimeStart(time.UTC); err != nil || !got.Equal(time.Date(2026, 3, 9, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("a time set in the calendar's zone reads back as %v, %v", got, err)
	}
}

func TestTimezoneNamedNowhere(t *testing.T) {
	cal := decodeCalendar(t, `BEGIN:VEVENT
UID:olympus
DTSTAMP:20260101T000000Z
DTSTART;TZID=Mars/Olympus:20260916T100000
END:VEVENT`)
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Fatal(err)
	}
	got, err := cal.Events()[0].DateTimeStart(tehran)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 16, 10, 0, 0, 0, tehran); !got.Equal(want) {
		t.Errorf("a TZID nothing defines reads as %v, want the floating %v", got, want)
	}
}

func TestTimezoneLegacyName(t *testing.T) {
	cal := decodeCalendar(t, `BEGIN:VEVENT
UID:legacy
DTSTAMP:20260101T000000Z
DTSTART;TZID=US/Central:20260710T100000
END:VEVENT`)
	got, err := cal.Events()[0].DateTimeStart(time.UTC)
	if err != nil || !got.Equal(time.Date(2026, 7, 10, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("US/Central reads as %v, %v", got, err)
	}
}

func TestTimezoneMalformed(t *testing.T) {
	for name, observance := range map[string]string{
		"a rule changing every minute": "DTSTART:20260101T000000\nRRULE:FREQ=MINUTELY",
		"an onset past the horizon":    "DTSTART:25000101T000000",
	} {
		t.Run(name, func(t *testing.T) {
			cal := decodeCalendar(t, `BEGIN:VTIMEZONE
TZID:Broken
BEGIN:STANDARD
`+observance+`
TZOFFSETFROM:+0100
TZOFFSETTO:+0000
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
UID:broken
DTSTAMP:20260101T000000Z
DTSTART;TZID=Broken:20260710T100000
END:VEVENT`)
			if _, err := cal.Events()[0].DateTimeStart(time.UTC); err == nil {
				t.Error("a malformed VTIMEZONE reads without an error")
			}
		})
	}
}
