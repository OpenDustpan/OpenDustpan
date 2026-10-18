package freeze_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/freeze"
)

func berlin(t *testing.T) (calendar.Calendar, *time.Location) {
	t.Helper()

	c := calendar.Default()
	c.ID = "cal-berlin"
	c.Name = "Berlin"
	c.Zone = "Europe/Berlin"
	c.Holidays = []calendar.Holiday{
		{
			Date:   calendar.Date{Year: 2026, Month: 12, Day: 25},
			Name:   "Christmas Day",
			Source: calendar.SourceManual,
			Feed:   "",
		},
	}

	loc, err := time.LoadLocation(c.Zone)
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}

	return c, loc
}

func schedule(kind freeze.Kind) freeze.Schedule {
	return freeze.Schedule{
		ID:        "fzs-weekend",
		Namespace: "shop",
		Name:      "Weekend",
		Reason:    "No changes at the weekend",
		Kind:      kind,
		Window:    freeze.Window{Start: time.Time{}, End: time.Time{}},
		Weekly:    freeze.Weekly{Days: 0, Start: 0, Length: 0, Zone: ""},
		Calendar:  freeze.CalendarRule{DaysOff: false, Holidays: false, OutsideHours: false, DaysBefore: 0},
		From:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Until:     time.Time{},
		Enabled:   true,
		Notice:    0,
		CreatedBy: "alice",
		Version:   1,
	}
}

type want struct{ start, end time.Time }

func check(t *testing.T, got []freeze.Occurrence, wants []want) {
	t.Helper()

	if len(got) != len(wants) {
		t.Fatalf("occurrences = %d (%v), want %d", len(got), got, len(wants))
	}

	for i, w := range wants {
		if !got[i].Start.Equal(w.start) || !got[i].End.Equal(w.end) {
			t.Fatalf("occurrence %d = %s to %s, want %s to %s", i, got[i].Start, got[i].End, w.start, w.end)
		}
	}
}

func TestWeeklyOccurrences(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, loc) }

	s := schedule(freeze.KindWeekly)
	s.Weekly = freeze.Weekly{
		Days:   calendar.Weekdays(time.Friday),
		Start:  calendar.ClockOf(15, 0),
		Length: 66 * time.Hour,
		Zone:   "Europe/Berlin",
	}

	cases := []struct {
		name     string
		from, to time.Time
		want     []want
	}{
		{"two weekends", at(10, 12, 12), at(10, 26, 0), []want{
			{at(10, 16, 15), at(10, 19, 9)},
			{at(10, 23, 15), at(10, 26, 8)}, // 66 hours over the change to winter time
		}},
		{
			"an occurrence that started before the range keeps its start",
			at(10, 18, 12), at(10, 19, 0),
			[]want{{at(10, 16, 15), at(10, 19, 9)}},
		},
		{"an empty range", at(10, 20, 0), at(10, 20, 0), nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := s.Occurrences(cal, tc.from, tc.to)
			if err != nil {
				t.Fatalf("Occurrences: %v", err)
			}

			check(t, got, tc.want)
		})
	}
}

func TestCalendarOccurrences(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, loc) }

	cases := []struct {
		name     string
		rule     freeze.CalendarRule
		from, to time.Time
		want     []want
	}{
		{
			"days off join into one weekend",
			freeze.CalendarRule{DaysOff: true, Holidays: false, OutsideHours: false, DaysBefore: 0},
			at(10, 19, 0), at(10, 26, 0),
			[]want{{at(10, 24, 0), at(10, 26, 0)}},
		},
		{
			"outside hours and days off give one span from Friday evening to Monday morning",
			freeze.CalendarRule{DaysOff: true, Holidays: false, OutsideHours: true, DaysBefore: 0},
			at(10, 23, 12), at(10, 26, 12),
			[]want{{at(10, 23, 17), at(10, 26, 9)}},
		},
		{
			"a holiday, the day before it and the weekend after it",
			freeze.CalendarRule{DaysOff: true, Holidays: true, OutsideHours: false, DaysBefore: 1},
			at(12, 21, 0), at(12, 28, 0),
			[]want{{at(12, 24, 0), at(12, 28, 0)}},
		},
		{
			"a range inside a long span keeps the start of the span",
			freeze.CalendarRule{DaysOff: true, Holidays: true, OutsideHours: false, DaysBefore: 1},
			at(12, 27, 12), at(12, 27, 13),
			[]want{{at(12, 24, 0), at(12, 28, 0)}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := schedule(freeze.KindCalendar)
			s.Calendar = tc.rule

			got, err := s.Occurrences(cal, tc.from, tc.to)
			if err != nil {
				t.Fatalf("Occurrences: %v", err)
			}

			check(t, got, tc.want)
		})
	}
}

func TestScheduleRange(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, loc) }

	s := schedule(freeze.KindCalendar)
	s.Calendar.DaysOff = true
	s.From = at(10, 25, 0)
	s.Until = at(11, 1, 0)

	got, err := s.Occurrences(cal, at(10, 1, 0), at(11, 30, 0))
	if err != nil {
		t.Fatalf("Occurrences: %v", err)
	}

	// The weekend of 24 and 25 October ends after From, so it stays. The
	// weekend of 31 October starts before Until, so it stays. The later
	// weekends start after Until.
	check(t, got, []want{{at(10, 24, 0), at(10, 26, 0)}, {at(10, 31, 0), at(11, 2, 0)}})

	s.Enabled = false

	got, err = s.Occurrences(cal, at(10, 1, 0), at(11, 30, 0))
	if err != nil || len(got) != 0 {
		t.Fatalf("disabled: occurrences = %v (%v), want none", got, err)
	}
}

func TestOccurrenceIDIsStable(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC)
	if got := freeze.OccurrenceID("fzs-weekend", start); got != "frz-weekend-1792155600000" {
		t.Fatalf("OccurrenceID = %q", got)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	weekly := func(days calendar.WeekdaySet, length time.Duration) func(s *freeze.Schedule) {
		return func(s *freeze.Schedule) {
			s.Kind = freeze.KindWeekly
			s.Weekly = freeze.Weekly{Days: days, Start: calendar.ClockOf(18, 0), Length: length, Zone: "UTC"}
		}
	}

	cases := []struct {
		name   string
		change func(s *freeze.Schedule)
		valid  bool
	}{
		{"a window", func(s *freeze.Schedule) {
			s.Kind = freeze.KindWindow
			s.Window = freeze.Window{Start: s.From, End: s.From.Add(time.Hour)}
		}, true},
		{"a window with no end", func(s *freeze.Schedule) {
			s.Kind = freeze.KindWindow
			s.Window = freeze.Window{Start: s.From, End: time.Time{}}
		}, false},
		{"a weekend", weekly(calendar.Weekdays(time.Friday), 63*time.Hour), true},
		{"each night", weekly(calendar.MondayToFriday, 14*time.Hour), true},
		{"nights that overlap", weekly(calendar.MondayToFriday, 24*time.Hour), false},
		{"a weekly rule with no day", weekly(0, time.Hour), false},
		{"a calendar rule with no period", func(s *freeze.Schedule) { s.Calendar.DaysOff = false }, false},
		{"too many days before a holiday", func(s *freeze.Schedule) {
			s.Kind = freeze.KindCalendar
			s.Calendar.DaysBefore = freeze.MaxDaysBefore + 1
		}, false},
		{"an unknown kind", func(s *freeze.Schedule) { s.Kind = "monthly" }, false},
		{"no reason", func(s *freeze.Schedule) { s.Reason = "" }, false},
		{"an end before the start", func(s *freeze.Schedule) { s.Until = s.From.Add(-time.Hour) }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := schedule(freeze.KindCalendar)
			s.Calendar.DaysOff = true
			tc.change(&s)

			err := s.Validate()
			if tc.valid && err != nil {
				t.Fatalf("Validate: %v", err)
			}

			if !tc.valid && !errors.Is(err, freeze.ErrInvalid) {
				t.Fatalf("Validate: err = %v, want ErrInvalid", err)
			}
		})
	}
}
