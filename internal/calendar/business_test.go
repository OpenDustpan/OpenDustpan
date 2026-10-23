package calendar_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/calendar"
)

// berlin is a calendar of Europe/Berlin, Monday to Friday, 09:00 to 17:00,
// with two holidays: Friday 2026-12-25 and Monday 2026-12-28 (a holiday
// that a team adds by hand).
func berlin() calendar.Calendar {
	c := calendar.Default()
	c.ID = "cal-berlin"
	c.Name = "Berlin"
	c.Zone = "Europe/Berlin"
	c.Holidays = []calendar.Holiday{
		{
			Date:   calendar.Date{Year: 2026, Month: 12, Day: 25},
			Name:   "Christmas Day",
			Source: calendar.SourceFeed,
			Feed:   "f1",
		},
		{
			Date:   calendar.Date{Year: 2026, Month: 12, Day: 28},
			Name:   "Team day off",
			Source: calendar.SourceManual,
			Feed:   "",
		},
	}

	return c
}

func berlinZone(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("load Europe/Berlin: %v", err)
	}

	return loc
}

func TestEndOfNextBusinessDay(t *testing.T) {
	t.Parallel()

	loc := berlinZone(t)
	at := func(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, loc) }

	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"a Tuesday morning is due on Wednesday", at(2026, 10, 20, 10, 0), at(2026, 10, 21, 17, 0)},
		{"a Tuesday night is due on Wednesday", at(2026, 10, 20, 23, 30), at(2026, 10, 21, 17, 0)},
		{"a Friday is due on Monday", at(2026, 10, 23, 18, 0), at(2026, 10, 26, 17, 0)},
		{"a Saturday is due on Monday", at(2026, 10, 24, 3, 0), at(2026, 10, 26, 17, 0)},
		{"a holiday and a manual day off move the deadline", at(2026, 12, 24, 12, 0), at(2026, 12, 29, 17, 0)},
		{
			"the zone of the calendar decides the date, not UTC",
			time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC), // 00:30 on Wednesday in Berlin
			at(2026, 10, 22, 17, 0),
		},
		{
			"a deadline after the change to winter time keeps the local hour",
			at(2026, 10, 23, 12, 0),
			time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC),
		},
	}

	c := berlin()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.EndOfNextBusinessDay(tc.in)
			if err != nil {
				t.Fatalf("EndOfNextBusinessDay: %v", err)
			}

			if !got.Equal(tc.want) {
				t.Fatalf("EndOfNextBusinessDay(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestNextBusinessMoment(t *testing.T) {
	t.Parallel()

	loc := berlinZone(t)
	at := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, loc) }

	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"in business hours it is now", at(10, 20, 11, 15), at(10, 20, 11, 15)},
		{"before the start it is the start", at(10, 20, 7, 0), at(10, 20, 9, 0)},
		{"at the end it is the next start", at(10, 20, 17, 0), at(10, 21, 9, 0)},
		{"on a weekend it is Monday", at(10, 24, 12, 0), at(10, 26, 9, 0)},
		{"on a holiday it is the next business day", at(12, 24, 18, 0), at(12, 29, 9, 0)},
	}

	c := berlin()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.NextBusinessMoment(tc.in)
			if err != nil {
				t.Fatalf("NextBusinessMoment: %v", err)
			}

			if !got.Equal(tc.want) {
				t.Fatalf("NextBusinessMoment(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestAddBusinessTime(t *testing.T) {
	t.Parallel()

	loc := berlinZone(t)
	at := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, loc) }

	cases := []struct {
		name string
		in   time.Time
		add  time.Duration
		want time.Time
	}{
		{"inside one day", at(10, 20, 10, 0), 2 * time.Hour, at(10, 20, 12, 0)},
		{"over the end of a day", at(10, 20, 16, 0), 2 * time.Hour, at(10, 21, 10, 0)},
		{"over a weekend", at(10, 23, 16, 0), 2 * time.Hour, at(10, 26, 10, 0)},
		{"one full business day", at(10, 20, 9, 0), 8 * time.Hour, at(10, 20, 17, 0)},
		{"from outside business hours", at(10, 24, 12, 0), time.Hour, at(10, 26, 10, 0)},
		{"zero is the next business moment", at(10, 24, 12, 0), 0, at(10, 26, 9, 0)},
	}

	c := berlin()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := c.AddBusinessTime(tc.in, tc.add)
			if err != nil {
				t.Fatalf("AddBusinessTime: %v", err)
			}

			if !got.Equal(tc.want) {
				t.Fatalf("AddBusinessTime(%s, %s) = %s, want %s", tc.in, tc.add, got, tc.want)
			}
		})
	}

	if _, err := c.AddBusinessTime(at(10, 20, 10, 0), -time.Hour); !errors.Is(err, calendar.ErrInvalid) {
		t.Fatalf("negative duration: err = %v, want ErrInvalid", err)
	}
}

// TestAddBusinessTimeSkippedHours checks a date on which a zone change
// skips all working hours: on 2027-03-28 Berlin moves from 02:00 to 03:00,
// so working hours from 02:30 to 03:00 hold no time on that date.
func TestAddBusinessTimeSkippedHours(t *testing.T) {
	t.Parallel()

	loc := berlinZone(t)

	c := berlin()
	c.WorkingDays = calendar.Weekdays(time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
		time.Friday, time.Saturday, time.Sunday)
	c.DayStart = calendar.ClockOf(2, 30)
	c.DayEnd = calendar.ClockOf(3, 0)

	got, err := c.AddBusinessTime(time.Date(2027, 3, 28, 0, 0, 0, 0, loc), 30*time.Minute)
	if err != nil {
		t.Fatalf("AddBusinessTime: %v", err)
	}

	want := time.Date(2027, 3, 29, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("AddBusinessTime = %s, want %s", got, want)
	}
}

func TestNoBusinessDay(t *testing.T) {
	t.Parallel()

	c := calendar.Default()
	c.WorkingDays = calendar.Weekdays(time.Monday)

	start := calendar.Date{Year: 2027, Month: 1, Day: 1}
	for d := start; d.Before(start.AddDays(calendar.MaxSearchDays + 1)); d = d.AddDays(1) {
		c.Holidays = append(
			c.Holidays,
			calendar.Holiday{Date: d, Name: "closed", Source: calendar.SourceManual, Feed: ""},
		)
	}

	_, err := c.NextBusinessDay(time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, calendar.ErrNoBusinessDay) {
		t.Fatalf("err = %v, want ErrNoBusinessDay", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(c *calendar.Calendar)
		want   error
	}{
		{"the default is valid", func(*calendar.Calendar) {}, nil},
		{"an ID needs the prefix", func(c *calendar.Calendar) { c.ID = "berlin" }, calendar.ErrInvalid},
		{"a name is required", func(c *calendar.Calendar) { c.Name = "" }, calendar.ErrInvalid},
		{"no working day", func(c *calendar.Calendar) { c.WorkingDays = 0 }, calendar.ErrInvalid},
		{"hours end before they start", func(c *calendar.Calendar) { c.DayEnd = c.DayStart }, calendar.ErrInvalid},
		{
			"hours past midnight",
			func(c *calendar.Calendar) { c.DayEnd = calendar.MinutesPerDay + 1 },
			calendar.ErrInvalid,
		},
		{"an unknown zone", func(c *calendar.Calendar) { c.Zone = "Mars/Olympus" }, calendar.ErrUnknownZone},
		{"the zone of the host", func(c *calendar.Calendar) { c.Zone = "Local" }, calendar.ErrUnknownZone},
		{
			"an owner with a namespace and a group",
			func(c *calendar.Calendar) { c.Owner = calendar.Scope{Namespace: "shop", Group: "network"} },
			calendar.ErrInvalid,
		},
		{
			"a holiday that is not a date",
			func(c *calendar.Calendar) {
				c.Holidays = []calendar.Holiday{
					{
						Date:   calendar.Date{Year: 2026, Month: 2, Day: 30},
						Name:   "x",
						Source: calendar.SourceManual,
						Feed:   "",
					},
				}
			},
			calendar.ErrInvalid,
		},
		{
			"too many feeds",
			func(c *calendar.Calendar) { c.Feeds = make([]calendar.Feed, calendar.MaxFeeds+1) },
			calendar.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := calendar.Default()
			tc.change(&c)

			err := c.Validate()
			if tc.want == nil && err != nil {
				t.Fatalf("Validate: %v", err)
			}

			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Validate: err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestScopeValidate(t *testing.T) {
	t.Parallel()

	if err := (calendar.Scope{Namespace: "shop", Group: "network"}).Validate(); !errors.Is(err, calendar.ErrInvalid) {
		t.Fatalf("both set: err = %v, want ErrInvalid", err)
	}

	for _, s := range []calendar.Scope{calendar.Installation(), {Namespace: "shop", Group: ""}, {Namespace: "", Group: "network"}} {
		if err := s.Validate(); err != nil {
			t.Fatalf("Validate(%s): %v", s, err)
		}
	}
}
