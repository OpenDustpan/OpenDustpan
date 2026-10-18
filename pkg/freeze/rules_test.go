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

	utc := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, time.UTC) }

	weekend := freeze.Weekly{
		Days:   calendar.Weekdays(time.Friday),
		Start:  calendar.ClockOf(15, 0),
		Length: 66 * time.Hour,
		Zone:   "Europe/Berlin",
	}
	sunday := func(h, m int) freeze.Weekly {
		return freeze.Weekly{
			Days:   calendar.Weekdays(time.Sunday),
			Start:  calendar.ClockOf(h, m),
			Length: 2 * time.Hour,
			Zone:   "Europe/Berlin",
		}
	}

	cases := []struct {
		name     string
		rule     freeze.Weekly
		from, to time.Time
		want     []want
	}{
		{"two weekends", weekend, at(10, 12, 12), at(10, 26, 0), []want{
			{at(10, 16, 15), at(10, 19, 9)},
			// The end is Monday 09:00 on the local clock, also over the
			// change to winter time: 67 hours elapse.
			{at(10, 23, 15), at(10, 26, 9)},
		}},
		{
			"a weekend over the change to summer time ends Monday 09:00, after 65 hours",
			weekend, at(3, 26, 0), at(3, 28, 0),
			[]want{{utc(3, 27, 14, 0), utc(3, 30, 7, 0)}},
		},
		{
			"an occurrence that started before the range keeps its start",
			weekend, at(10, 18, 12), at(10, 19, 0),
			[]want{{at(10, 16, 15), at(10, 19, 9)}},
		},
		{"an empty range", weekend, at(10, 20, 0), at(10, 20, 0), nil},
		{
			"a start in the hour that the change to summer time skips moves forward by the gap",
			sunday(2, 30), at(3, 28, 0), at(3, 30, 0),
			// 02:30 does not exist on 29 March: the start is 03:30 summer
			// time, and the end 04:30 summer time.
			[]want{{utc(3, 29, 1, 30), utc(3, 29, 2, 30)}},
		},
		{
			"a start in the hour that occurs two times is the first instant",
			sunday(2, 30), at(10, 24, 0), at(10, 26, 0),
			// 02:30 occurs at 00:30 and at 01:30 UTC on 25 October. The
			// start is the first; the end 04:30 winter time is 03:30 UTC.
			[]want{{utc(10, 25, 0, 30), utc(10, 25, 3, 30)}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := schedule(freeze.KindWeekly)
			s.Weekly = tc.rule

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

// TestCalendarLongOccurrence checks that an occurrence that goes on after
// the end of the range keeps its whole end: the holidays from 24 December
// to 1 January, the weekend after them and the morning of Monday 4 January
// before the working hours are one freeze.
func TestCalendarLongOccurrence(t *testing.T) {
	t.Parallel()

	cal, loc := berlin(t)

	cal.Holidays = nil
	for d := (calendar.Date{Year: 2026, Month: 12, Day: 24}); d.Before(calendar.Date{Year: 2027, Month: 1, Day: 2}); d = d.AddDays(1) {
		cal.Holidays = append(cal.Holidays, calendar.Holiday{
			Date: d, Name: "Holidays", Source: calendar.SourceManual, Feed: "",
		})
	}

	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, loc) }

	s := schedule(freeze.KindCalendar)
	s.Calendar = freeze.CalendarRule{DaysOff: true, Holidays: true, OutsideHours: true, DaysBefore: 0}

	long := want{at(2026, 12, 23, 17), at(2027, 1, 4, 9)}

	cases := []struct {
		name string
		now  time.Time
		want []want
	}{
		{"a plan that ends inside the holidays", at(2026, 12, 18, 12), []want{
			{at(2026, 12, 18, 17), at(2026, 12, 21, 9)},
			{at(2026, 12, 21, 17), at(2026, 12, 22, 9)},
			{at(2026, 12, 22, 17), at(2026, 12, 23, 9)},
			long,
		}},
		{"a plan in the holidays", at(2026, 12, 28, 12), []want{
			long,
			{at(2027, 1, 4, 17), at(2027, 1, 5, 9)},
			{at(2027, 1, 5, 17), at(2027, 1, 6, 9)},
			{at(2027, 1, 6, 17), at(2027, 1, 7, 9)},
			{at(2027, 1, 7, 17), at(2027, 1, 8, 9)},
			{at(2027, 1, 8, 17), at(2027, 1, 11, 9)},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := s.Plan(cal, tc.now)
			if err != nil {
				t.Fatalf("Plan: %v", err)
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

func TestFreezeOf(t *testing.T) {
	t.Parallel()

	s := schedule(freeze.KindWindow)
	o := freeze.Occurrence{
		Schedule: s.ID,
		Start:    time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC),
	}

	f := s.FreezeOf(o)
	if f.ID != freeze.OccurrenceID(s.ID, o.Start) || f.Namespace != s.Namespace || f.Reason != s.Reason ||
		f.CreatedBy != "@schedule:fzs-weekend" || f.EndedBy != "" {
		t.Fatalf("FreezeOf = %+v", f)
	}

	if err := f.Validate(); err != nil {
		t.Fatalf("the freeze of an occurrence fails job.Freeze.Validate: %v", err)
	}

	cases := []struct {
		name      string
		createdBy string
		want      string
		ok        bool
	}{
		{"a freeze of a schedule", freeze.Subject("fzs-nights"), "fzs-nights", true},
		{"a freeze of a person", "alice", "", false},
		{"the prefix with no schedule", freeze.SubjectPrefix, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := f
			g.CreatedBy = tc.createdBy

			if got, ok := freeze.ScheduleOf(g); got != tc.want || ok != tc.ok {
				t.Fatalf("ScheduleOf = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
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
		{"the server scope", func(s *freeze.Schedule) { s.Namespace = "@server" }, false},
		{"a weekly rule in the zone Local", func(s *freeze.Schedule) {
			weekly(calendar.Weekdays(time.Friday), time.Hour)(s)
			s.Weekly.Zone = "Local"
		}, false},
		{"a weekly rule with no zone", func(s *freeze.Schedule) {
			weekly(calendar.Weekdays(time.Friday), time.Hour)(s)
			s.Weekly.Zone = ""
		}, false},
		{"a weekly rule in an unknown zone", func(s *freeze.Schedule) {
			weekly(calendar.Weekdays(time.Friday), time.Hour)(s)
			s.Weekly.Zone = "Europe/Atlantis"
		}, false},
		{"a weekly rule in an IANA zone", func(s *freeze.Schedule) {
			weekly(calendar.Weekdays(time.Friday), time.Hour)(s)
			s.Weekly.Zone = "Europe/Berlin"
		}, true},
		{
			"a weekly length that is not whole minutes",
			weekly(calendar.Weekdays(time.Friday), time.Hour+time.Second),
			false,
		},
		{"the longest notice", func(s *freeze.Schedule) { s.Notice = freeze.MaxNotice }, true},
		{
			"a notice longer than the plan of a run",
			func(s *freeze.Schedule) { s.Notice = freeze.MaxNotice + time.Minute },
			false,
		},
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
