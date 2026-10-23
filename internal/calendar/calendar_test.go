package calendar_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/calendar"
	"opendustpan.org/opendustpan/pkg/egress"
)

// Errors with the text of a network error, as a source can return them.
var (
	errDial   = errors.New("dial tcp 10.0.0.1:443")
	errLookup = errors.New("lookup intranet.local on 10.0.0.2:53")
)

// unread is the status of a feed before its first read.
func unread() calendar.FeedStatus {
	var s calendar.FeedStatus

	return s
}

// everyDay is a calendar of Europe/Berlin with every day a working day and
// working hours from 02:30 to 17:00, so that the start of the working
// hours falls into the changes of summer time.
func everyDay() calendar.Calendar {
	c := calendar.Default()
	c.ID = "cal-night"
	c.Name = "Night shift"
	c.Zone = "Europe/Berlin"
	c.WorkingDays = calendar.Weekdays(time.Sunday, time.Monday, time.Tuesday, time.Wednesday,
		time.Thursday, time.Friday, time.Saturday)
	c.DayStart = calendar.ClockOf(2, 30)

	return c
}

func TestZoneChange(t *testing.T) {
	t.Parallel()

	loc := berlinZone(t)
	utc := func(m time.Month, d, h, mi int) time.Time { return time.Date(2027, m, d, h, mi, 0, 0, time.UTC) }

	cases := []struct {
		name string
		run  func(c calendar.Calendar) (time.Time, error)
		want time.Time
	}{
		{
			// On 2027-03-28 the clock goes from 02:00 to 03:00, so 02:30
			// does not exist and moves forward by the gap to 03:30 CEST.
			"a skipped start moves forward by the gap",
			func(c calendar.Calendar) (time.Time, error) {
				return c.NextBusinessMoment(time.Date(2027, 3, 28, 0, 0, 0, 0, loc))
			},
			utc(3, 28, 1, 30),
		},
		{
			"business time on the short day counts from the moved start",
			func(c calendar.Calendar) (time.Time, error) {
				return c.AddBusinessTime(time.Date(2027, 3, 28, 0, 0, 0, 0, loc), time.Hour)
			},
			utc(3, 28, 2, 30),
		},
		{
			// On 2027-10-31 the clock goes from 03:00 back to 02:00, so
			// 02:30 occurs twice. The first instant is 02:30 CEST.
			"a start that occurs twice is the first instant",
			func(c calendar.Calendar) (time.Time, error) {
				return c.NextBusinessMoment(time.Date(2027, 10, 31, 0, 0, 0, 0, loc))
			},
			utc(10, 31, 0, 30),
		},
		{
			"work start on the long day is the first instant",
			func(c calendar.Calendar) (time.Time, error) {
				return c.WorkStart(calendar.Date{Year: 2027, Month: 10, Day: 31})
			},
			utc(10, 31, 0, 30),
		},
		{
			"the end of the day after the change keeps the local hour",
			func(c calendar.Calendar) (time.Time, error) {
				return c.EndOfNextBusinessDay(time.Date(2027, 10, 30, 12, 0, 0, 0, loc))
			},
			utc(10, 31, 16, 0),
		},
		{
			"working hours that end at midnight end at the next day",
			func(c calendar.Calendar) (time.Time, error) {
				c.DayEnd = calendar.MinutesPerDay

				return c.EndOfNextBusinessDay(time.Date(2027, 3, 27, 12, 0, 0, 0, loc))
			},
			utc(3, 28, 22, 0),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.run(everyDay())
			if err != nil {
				t.Fatalf("err = %v", err)
			}

			if !got.Equal(tc.want) {
				t.Fatalf("got %s, want %s", got.UTC(), tc.want)
			}
		})
	}
}

func TestLocation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		zone string
		want error
	}{
		{"UTC", nil},
		{"Europe/Berlin", nil},
		{"America/Argentina/Buenos_Aires", nil},
		{"America/Port-au-Prince", nil},
		{"Etc/GMT+5", nil},
		{"Local", calendar.ErrUnknownZone},
		{"", calendar.ErrUnknownZone},
		{"utc", calendar.ErrUnknownZone},
		{"Europe/../Berlin", calendar.ErrUnknownZone},
		{"/etc/localtime", calendar.ErrUnknownZone},
		{"localtime", calendar.ErrUnknownZone},
		{"posixrules", calendar.ErrUnknownZone},
		{"Factory", calendar.ErrUnknownZone},
		{"Europe/Berlin ", calendar.ErrUnknownZone},
		{"Mars/Olympus", calendar.ErrUnknownZone},
	}

	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()

			c := calendar.Default()
			c.Zone = tc.zone

			_, err := c.Location()
			if tc.want == nil && err != nil {
				t.Fatalf("Location(%q): %v", tc.zone, err)
			}

			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Location(%q): err = %v, want %v", tc.zone, err, tc.want)
			}

			_, err = calendar.LoadZone(tc.zone)
			if (tc.want == nil) != (err == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("LoadZone(%q): err = %v, want %v", tc.zone, err, tc.want)
			}
		})
	}
}

func TestValidateFeeds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		url     string
		refused bool
	}{
		{"a public https URL", "https://holidays.example.com/de.ics", false},
		{"a private URL with a token in the path", "https://calendar.example.com/ical/abc123/basic.ics", false},
		{"http is refused", "http://holidays.example.com/de.ics", true},
		{"the metadata address", "https://169.254.169.254/latest/meta-data/", true},
		{"loopback", "https://127.0.0.1/feed.ics", true},
		{"localhost by name", "https://localhost/feed.ics", true},
		{"IPv6 loopback", "https://[::1]/feed.ics", true},
		{"IPv4-mapped IPv6 metadata address", "https://[::ffff:169.254.169.254]/feed.ics", true},
		{"a private address", "https://10.0.0.5/feed.ics", true},
		{"user information", "https://user:" + "secret@holidays.example.com/de.ics", true},
		{"no host", "https:///de.ics", true},
		{"webcal is refused", "webcal://holidays.example.com/de.ics", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := calendar.Default()
			c.Feeds = []calendar.Feed{{ID: "f1", URL: tc.url, Status: unread()}}

			err := c.Validate()
			if !tc.refused && err != nil {
				t.Fatalf("Validate: %v", err)
			}

			if tc.refused && (!errors.Is(err, calendar.ErrInvalid) || !errors.Is(err, egress.ErrRefused)) {
				t.Fatalf("Validate: err = %v, want ErrInvalid and egress.ErrRefused", err)
			}
		})
	}
}

func TestValidateFeedIDs(t *testing.T) {
	t.Parallel()

	feed := func(id string) calendar.Feed {
		return calendar.Feed{ID: id, URL: "https://holidays.example.com/" + id + ".ics", Status: unread()}
	}

	cases := []struct {
		name  string
		feeds []calendar.Feed
		want  error
	}{
		{"distinct IDs", []calendar.Feed{feed("a"), feed("b")}, nil},
		{"an empty ID", []calendar.Feed{feed("")}, calendar.ErrInvalid},
		{"one ID twice", []calendar.Feed{feed("a"), feed("a")}, calendar.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := calendar.Default()
			c.Feeds = tc.feeds

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

func TestMaskURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"https://calendar.example.com/ical/abc123/private.ics", "https://calendar.example.com/…"},
		{"https://calendar.example.com/feed.ics?token=abc", "https://calendar.example.com/…"},
		{"https://calendar.example.com:8443/feed.ics", "https://calendar.example.com:8443/…"},
		{"https://user:" + "secret@calendar.example.com/feed.ics", "https://calendar.example.com/…"},
		{"https://calendar.example.com", "https://calendar.example.com/…"},
		{"not a url", ""},
		{"", ""},
		{"://bad", ""},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := calendar.MaskURL(tc.in); got != tc.want {
				t.Fatalf("MaskURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMasked(t *testing.T) {
	t.Parallel()

	c := calendar.Default()
	c.Feeds = []calendar.Feed{{ID: "f1", URL: "https://calendar.example.com/t0ken.ics", Status: unread()}}

	m := c.Masked()
	if m.Feeds[0].URL != "https://calendar.example.com/…" {
		t.Fatalf("Masked URL = %q", m.Feeds[0].URL)
	}

	if c.Feeds[0].URL != "https://calendar.example.com/t0ken.ics" {
		t.Fatalf("Masked changed the calendar it read: %q", c.Feeds[0].URL)
	}
}

func TestMergeFeeds(t *testing.T) {
	t.Parallel()

	read := calendar.FeedStatus{
		ReadAt:   time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC),
		Holidays: 9,
		Skipped:  0,
		Error:    "",
		ETag:     "v1",
	}
	stored := []calendar.Feed{{ID: "f1", URL: "https://calendar.example.com/t0ken.ics", Status: read}}

	cases := []struct {
		name     string
		next     calendar.Feed
		wantURL  string
		wantKeep bool
	}{
		{
			"the full URL keeps the feed",
			calendar.Feed{ID: "f1", URL: "https://calendar.example.com/t0ken.ics", Status: unread()},
			"https://calendar.example.com/t0ken.ics",
			true,
		},
		{
			"the masked URL keeps the stored URL",
			calendar.Feed{ID: "f1", URL: "https://calendar.example.com/…", Status: unread()},
			"https://calendar.example.com/t0ken.ics",
			true,
		},
		{
			"a new URL starts again",
			calendar.Feed{ID: "f1", URL: "https://calendar.example.com/other.ics", Status: read},
			"https://calendar.example.com/other.ics",
			false,
		},
		{
			"a new ID starts again",
			calendar.Feed{ID: "f2", URL: "https://calendar.example.com/t0ken.ics", Status: read},
			"https://calendar.example.com/t0ken.ics",
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, keep := calendar.MergeFeeds(stored, []calendar.Feed{tc.next})
			if got[0].URL != tc.wantURL || keep[tc.next.ID] != tc.wantKeep {
				t.Fatalf(
					"MergeFeeds = %q keep %v, want %q keep %v",
					got[0].URL,
					keep[tc.next.ID],
					tc.wantURL,
					tc.wantKeep,
				)
			}

			if tc.wantKeep != (got[0].Status == read) {
				t.Fatalf("status = %+v, want the stored status only for a kept feed", got[0].Status)
			}
		})
	}
}

func TestFeedErrorOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want calendar.FeedError
	}{
		{"no error", nil, ""},
		{"not modified", calendar.ErrNotModified, ""},
		{"refused", calendar.ErrFeedRefused, calendar.FeedRefused},
		{"egress refused", errors.Join(errDial, egress.ErrRefused), calendar.FeedRefused},
		{"unreachable", calendar.ErrFeedUnreachable, calendar.FeedUnreachable},
		{"timeout", calendar.ErrFeedTimeout, calendar.FeedTimeout},
		{"too large", calendar.ErrFeedTooLarge, calendar.FeedTooLarge},
		{"parse", calendar.ErrFeedInvalid, calendar.FeedParse},
		{"status", calendar.ErrFeedHTTPStatus, calendar.FeedHTTPStatus},
		{"unknown text", errLookup, calendar.FeedUnreachable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := calendar.FeedErrorOf(tc.err)
			if got != tc.want || !got.Valid() {
				t.Fatalf("FeedErrorOf(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}

	if calendar.FeedError("dial tcp 10.0.0.1:443: refused").Valid() {
		t.Fatal("a raw error text is a valid class")
	}
}

func TestHolidayValidate(t *testing.T) {
	t.Parallel()

	day := calendar.Date{Year: 2026, Month: 12, Day: 24}

	cases := []struct {
		name string
		h    calendar.Holiday
		ok   bool
	}{
		{"manual", calendar.Holiday{Date: day, Name: "x", Source: calendar.SourceManual, Feed: ""}, true},
		{"from a feed", calendar.Holiday{Date: day, Name: "x", Source: calendar.SourceFeed, Feed: "f1"}, true},
		{
			"manual with a feed",
			calendar.Holiday{Date: day, Name: "x", Source: calendar.SourceManual, Feed: "f1"},
			false,
		},
		{"from no feed", calendar.Holiday{Date: day, Name: "x", Source: calendar.SourceFeed, Feed: ""}, false},
		{"unknown source", calendar.Holiday{Date: day, Name: "x", Source: "import", Feed: ""}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.h.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, calendar.ErrInvalid)) {
				t.Fatalf("Validate = %v, want ok %v", err, tc.ok)
			}
		})
	}
}
