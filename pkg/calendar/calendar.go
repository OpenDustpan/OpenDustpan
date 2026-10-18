// Package calendar is the business calendar of OpenDustpan: the time zone,
// the working days, the working hours and the holidays of a namespace or a
// team. Deadlines, reminders, escalations and freeze schedules read it, so
// that "the next business day" means the same thing in every feature.
//
// A Calendar is a value. Its methods are pure: they read the calendar and a
// time, and never the clock or a store. The Store port keeps calendars and
// their assignments. The HolidaySource port reads holidays from an iCal
// feed. Adapters of both ports live outside this package.
package calendar

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store and HolidaySource
// implementations.
var (
	// ErrInvalid reports a calendar, a scope or a holiday that fails
	// validation.
	ErrInvalid = errors.New("invalid calendar")
	// ErrNotFound reports an unknown calendar ID.
	ErrNotFound = errors.New("calendar not found")
	// ErrExists reports a create with an ID that a calendar already has.
	ErrExists = errors.New("calendar already exists")
	// ErrConflict reports a write that names an older version of the
	// calendar.
	ErrConflict = errors.New("calendar changed since it was read")
	// ErrInUse reports a delete of a calendar that a scope still uses.
	ErrInUse = errors.New("calendar is in use")
	// ErrNoBusinessDay reports that no business day exists in the search
	// window: every day of MaxSearchDays is a holiday or a day off.
	ErrNoBusinessDay = errors.New("no business day in the search window")
	// ErrUnknownZone reports a time zone name that the time zone database
	// of the server does not hold.
	ErrUnknownZone = errors.New("unknown time zone")
)

// Bounds of a calendar.
const (
	// MaxNameLen caps the name of a calendar, in bytes.
	MaxNameLen = 100
	// MaxHolidays caps the holidays of a calendar that a person enters,
	// and separately the holidays of each feed.
	MaxHolidays = 1000
	// MaxFeeds caps the iCal feeds of a calendar.
	MaxFeeds = 5
	// MaxHolidayNameLen caps the name of a holiday, in bytes.
	MaxHolidayNameLen = 200
	// MaxFeedIDLen caps the ID of a feed, in bytes.
	MaxFeedIDLen = 64
	// MaxSearchDays bounds each search for a business day. A calendar with
	// no business day in this many days answers ErrNoBusinessDay.
	MaxSearchDays = 400
)

// IDPrefix starts every calendar ID.
const IDPrefix = "cal-"

// DefaultID names the installation default calendar. It always exists: UTC,
// Monday to Friday, 09:00 to 17:00, and no holidays. An administrator can
// change it, but not delete it.
const DefaultID = ID(IDPrefix + "default")

// ID names a calendar.
type ID string

// Date is a day in the time zone of a calendar, with no time of day.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the date of t in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()

	return Date{Year: y, Month: m, Day: d}
}

// String returns the date as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// AddDays returns the date n days after d. A negative n goes back.
func (d Date) AddDays(n int) Date {
	t := time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC)

	return DateOf(t, time.UTC)
}

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Weekday()
}

// Valid reports whether d names a real day.
func (d Date) Valid() bool {
	t := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)

	return t.Year() == d.Year && t.Month() == d.Month && t.Day() == d.Day
}

// Before reports whether d is before o.
func (d Date) Before(o Date) bool {
	if d.Year != o.Year {
		return d.Year < o.Year
	}

	if d.Month != o.Month {
		return d.Month < o.Month
	}

	return d.Day < o.Day
}

// Clock is a time of day, in minutes after midnight, from 0 to 1440. The
// value 1440 is the end of the day, so a working day can end at midnight.
type Clock int

// MinutesPerDay is the number of minutes in a day without a time zone
// change, and the largest valid Clock.
const MinutesPerDay = 24 * minutesPerHour

// minutesPerHour is the number of minutes in an hour.
const minutesPerHour = 60

// ClockOf returns the Clock of hour and minute.
func ClockOf(hour, minute int) Clock {
	return Clock(hour*minutesPerHour + minute)
}

// String returns the clock as HH:MM.
func (c Clock) String() string {
	return fmt.Sprintf("%02d:%02d", int(c)/minutesPerHour, int(c)%minutesPerHour)
}

// zoneChangeWindow is the time before and after a wall clock time in which
// at looks for the offsets of a zone. No zone changes its offset twice in
// this window.
const zoneChangeWindow = 12 * time.Hour

// at returns the time of c on the date d in loc, with these rules for a
// change of the offset of the zone (for example summer time):
//
//   - A wall clock time that occurs twice (the clock goes back) gives the
//     first instant, in the offset before the change.
//   - A wall clock time that the change skips (the clock goes forward)
//     moves forward by the length of the gap: 02:30 on a day that goes
//     from 02:00 to 03:00 gives 03:30.
func (c Clock) at(d Date, loc *time.Location) time.Time {
	wall := time.Date(d.Year, d.Month, d.Day, int(c)/minutesPerHour, int(c)%minutesPerHour, 0, 0, time.UTC)

	// near is within a few hours of the instant of the wall clock time.
	_, guess := wall.In(loc).Zone()
	near := wall.Add(-time.Duration(guess) * time.Second)

	_, before := near.Add(-zoneChangeWindow).In(loc).Zone()
	_, after := near.Add(zoneChangeWindow).In(loc).Zone()

	for _, offset := range []int{before, after} {
		t := wall.Add(-time.Duration(offset) * time.Second).In(loc)
		if sameWallClock(t, wall) {
			return t
		}
	}

	// The change skips the wall clock time: read it in the offset before
	// the gap, which moves it forward by the length of the gap.
	return wall.Add(-time.Duration(before) * time.Second).In(loc)
}

// sameWallClock reports whether t shows the date, hour and minute of wall,
// each in its own location.
func sameWallClock(t, wall time.Time) bool {
	ty, tm, td := t.Date()
	wy, wm, wd := wall.Date()
	th, tmin, _ := t.Clock()
	wh, wmin, _ := wall.Clock()

	return ty == wy && tm == wm && td == wd && th == wh && tmin == wmin
}

// WeekdaySet is a set of days of the week. Bit n is time.Weekday(n).
type WeekdaySet uint8

// MondayToFriday is the default set of working days.
const MondayToFriday = WeekdaySet(1<<time.Monday | 1<<time.Tuesday | 1<<time.Wednesday |
	1<<time.Thursday | 1<<time.Friday)

// Weekdays returns the set of days.
func Weekdays(days ...time.Weekday) WeekdaySet {
	var s WeekdaySet
	for _, d := range days {
		s |= 1 << d
	}

	return s
}

// Has reports whether d is in s.
func (s WeekdaySet) Has(d time.Weekday) bool {
	return s&(1<<d) != 0
}

// Empty reports whether s holds no day.
func (s WeekdaySet) Empty() bool {
	return s&(1<<7-1) == 0
}

// Source tells where a holiday comes from.
type Source string

// Sources of a holiday.
const (
	// SourceManual is a holiday that a person entered through the API.
	SourceManual Source = "manual"
	// SourceFeed is a holiday that the server read from an iCal feed.
	SourceFeed Source = "feed"
)

// Holiday is a day with no business in a calendar. A holiday removes the
// whole day.
type Holiday struct {
	Date Date
	Name string
	// Source is SourceManual, or SourceFeed with the ID of the feed in
	// Feed.
	Source Source
	Feed   string
}

// Feed is an iCal feed that the server reads holidays from on a schedule.
// URL is an https URL that passes the egress rules (egress.CheckURL with
// egress.HTTPSOnly). The URL of a private feed often holds a token, so the
// URL is a credential: the store and the HolidaySource adapter get the full
// URL, and every read through the API gets MaskURL of it (see
// Calendar.Masked).
type Feed struct {
	ID  string
	URL string
	// Status is what the last read of the feed gave. The store sets it; a
	// write through the API cannot.
	Status FeedStatus
}

// FeedError is the class of the error of the last read of a feed. The
// store holds the class only, never the text of a network error, because
// that text can hold an address of the network of the server.
type FeedError string

// Classes of the error of a feed read. The empty class is a read with no
// error.
const (
	// FeedRefused is a URL or an address that the egress rules refuse.
	FeedRefused FeedError = "refused"
	// FeedUnreachable is a host that the source cannot resolve or reach.
	FeedUnreachable FeedError = "unreachable"
	// FeedTimeout is a read that took longer than egress.Timeout.
	FeedTimeout FeedError = "timeout"
	// FeedTooLarge is a body larger than egress.MaxBodyBytes.
	FeedTooLarge FeedError = "too_large"
	// FeedParse is a body that is not iCal, or a feed with more than
	// MaxHolidays holidays in the import window.
	FeedParse FeedError = "parse"
	// FeedHTTPStatus is an answer with a status other than 200 or 304.
	FeedHTTPStatus FeedError = "http_status"
)

// Valid reports whether e is the empty class or one of the classes above.
func (e FeedError) Valid() bool {
	switch e {
	case "", FeedRefused, FeedUnreachable, FeedTimeout, FeedTooLarge, FeedParse, FeedHTTPStatus:
		return true
	default:
		return false
	}
}

// FeedErrorOf returns the class of err, an error of HolidaySource.Fetch.
// It returns the empty class for nil and for ErrNotModified, and
// FeedUnreachable for an error that it does not know.
func FeedErrorOf(err error) FeedError {
	switch {
	case err == nil, errors.Is(err, ErrNotModified):
		return ""
	case errors.Is(err, ErrFeedRefused), errors.Is(err, egress.ErrRefused):
		return FeedRefused
	case errors.Is(err, ErrFeedTimeout):
		return FeedTimeout
	case errors.Is(err, ErrFeedTooLarge):
		return FeedTooLarge
	case errors.Is(err, ErrFeedInvalid):
		return FeedParse
	case errors.Is(err, ErrFeedHTTPStatus):
		return FeedHTTPStatus
	default:
		return FeedUnreachable
	}
}

// FeedStatus is the outcome of the reads of a feed.
type FeedStatus struct {
	// ReadAt is the time of the last read, or the zero time before the
	// first read.
	ReadAt time.Time
	// Holidays is the number of holidays that the last good read gave.
	Holidays int
	// Skipped is the number of events of the last good read that the
	// import did not take (see HolidaySource).
	Skipped int
	// Error is the class of the error of the last read, or empty. A failed
	// read keeps the holidays of the last good read.
	Error FeedError
	// ETag is the validator of the last good read, for a conditional read.
	ETag string
}

// maskedPath replaces the path, the query and the fragment of a masked URL.
const maskedPath = "/…"

// MaskURL returns the scheme and the host (with the port) of the URL raw,
// with no user information, followed by
// "/…", for example "https://calendar.example.com/…". The path and the
// query of a feed URL can hold a token, so a read through the API returns
// this form. It returns "" for a URL that does not parse or has no host.
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}

	return u.Scheme + "://" + u.Host + maskedPath
}

// MergeFeeds returns the feeds of next, an update of a calendar whose
// stored feeds are stored. A feed of next with the ID of a stored feed and
// its URL, or MaskURL of its URL, keeps the stored URL and status; keep
// holds its ID. Every other feed gets a zero status. The store keeps the
// holidays of a feed only when keep holds its ID, and drops the holidays
// of every other feed.
func MergeFeeds(stored, next []Feed) ([]Feed, map[string]bool) {
	merged := make([]Feed, 0, len(next))
	keep := map[string]bool{}

	for _, f := range next {
		var zero FeedStatus

		f.Status = zero

		i := slices.IndexFunc(stored, func(s Feed) bool { return s.ID == f.ID })
		if i >= 0 && (f.URL == stored[i].URL || f.URL == MaskURL(stored[i].URL)) {
			f = stored[i]
			keep[f.ID] = true
		}

		merged = append(merged, f)
	}

	return merged, keep
}

// Scope names who a calendar applies to: a namespace, a group of
// OpenDustpan (a team), or the installation. Exactly one of Namespace and
// Group is set, or neither for the installation.
type Scope struct {
	Namespace namespace.Name
	Group     string
}

// Installation returns the scope of the installation default calendar.
func Installation() Scope {
	return Scope{Namespace: "", Group: ""}
}

// Validate returns ErrInvalid for a scope that names both a namespace and a
// group.
func (s Scope) Validate() error {
	if s.Namespace != "" && s.Group != "" {
		return fmt.Errorf("%w: a scope names a namespace or a group, not both", ErrInvalid)
	}

	return nil
}

// WriteScope returns the namespace whose fence a write in s carries: the
// namespace of s, or audit.ServerScope for a group or the installation. A
// write in audit.ServerScope carries the zero fence.
func (s Scope) WriteScope() namespace.Name {
	if s.Namespace != "" {
		return s.Namespace
	}

	return audit.ServerScope
}

// String returns "namespace:<name>", "group:<name>" or "installation".
func (s Scope) String() string {
	switch {
	case s.Namespace != "":
		return "namespace:" + s.Namespace.String()
	case s.Group != "":
		return "group:" + s.Group
	default:
		return "installation"
	}
}

// Calendar is a business calendar. Owner is the scope that the calendar
// belongs to; it does not change after the create. Zone is an IANA time
// zone name. WorkingDays and the working hours from DayStart to DayEnd say
// when business happens. Holidays remove whole days. Version grows on each
// change, and a write names the version that it read.
type Calendar struct {
	ID          ID
	Owner       Scope
	Name        string
	Zone        string
	WorkingDays WeekdaySet
	DayStart    Clock
	DayEnd      Clock
	Holidays    []Holiday
	Feeds       []Feed
	Version     int64
}

// Working hours of the installation default calendar.
const (
	defaultStartHour = 9
	defaultEndHour   = 17
)

// Default returns the installation default calendar, as it is before an
// administrator changes it.
func Default() Calendar {
	return Calendar{
		ID:          DefaultID,
		Owner:       Installation(),
		Name:        "Default",
		Zone:        "UTC",
		WorkingDays: MondayToFriday,
		DayStart:    ClockOf(defaultStartHour, 0),
		DayEnd:      ClockOf(defaultEndHour, 0),
		Holidays:    nil,
		Feeds:       nil,
		Version:     0,
	}
}

// Masked returns c with MaskURL of the URL of each feed. A read through the
// API returns this form.
func (c Calendar) Masked() Calendar {
	c.Feeds = slices.Clone(c.Feeds)
	for i := range c.Feeds {
		c.Feeds[i].URL = MaskURL(c.Feeds[i].URL)
	}

	return c
}

// Validate returns ErrInvalid for a calendar that breaks a bound, or whose
// feed URL the egress rules refuse (the error also matches
// egress.ErrRefused), and ErrUnknownZone for a zone that is not a valid
// IANA name.
func (c Calendar) Validate() error {
	if err := c.validateFields(); err != nil {
		return err
	}

	if _, err := c.Location(); err != nil {
		return err
	}

	if err := c.validateFeeds(); err != nil {
		return err
	}

	return c.validateHolidays()
}

// utc is the one zone name with no area.
const utc = "UTC"

// Location loads the time zone of the calendar. It returns ErrUnknownZone
// for a name that is not "UTC" or of the IANA form Area/Location (for
// example "Europe/Berlin" or "America/Argentina/Buenos_Aires"), and for a
// name that the time zone database does not hold. So "Local", which is the
// zone of the host, is refused.
func (c Calendar) Location() (*time.Location, error) {
	if !validZoneName(c.Zone) {
		return nil, fmt.Errorf("%w: %q is not an IANA name of the form Area/Location", ErrUnknownZone, c.Zone)
	}

	loc, err := time.LoadLocation(c.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, c.Zone)
	}

	return loc, nil
}

// Bounds of the parts of a zone name.
const (
	minZoneParts = 2
	maxZoneParts = 3
)

// validZoneName reports whether name is "UTC", or has 2 or 3 parts split
// by "/", each part starting with a letter A to Z and holding letters,
// digits, "_", "+" and "-" only.
func validZoneName(name string) bool {
	if name == utc {
		return true
	}

	parts := strings.Split(name, "/")
	if len(parts) < minZoneParts || len(parts) > maxZoneParts {
		return false
	}

	return !slices.ContainsFunc(parts, func(p string) bool { return !validZonePart(p) })
}

// validZonePart reports whether p starts with a letter A to Z and holds
// letters, digits, "_", "+" and "-" only.
func validZonePart(p string) bool {
	if p == "" || p[0] < 'A' || p[0] > 'Z' {
		return false
	}

	for _, r := range p {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '+', r == '-':
		default:
			return false
		}
	}

	return true
}

// IsHoliday reports whether d is a holiday of the calendar, and its name.
func (c Calendar) IsHoliday(d Date) (string, bool) {
	i := slices.IndexFunc(c.Holidays, func(h Holiday) bool { return h.Date == d })
	if i < 0 {
		return "", false
	}

	return c.Holidays[i].Name, true
}

// IsBusinessDay reports whether d is a working day that is not a holiday.
func (c Calendar) IsBusinessDay(d Date) bool {
	if !c.WorkingDays.Has(d.Weekday()) {
		return false
	}

	_, holiday := c.IsHoliday(d)

	return !holiday
}

func (c Calendar) validateFields() error {
	switch {
	case !strings.HasPrefix(string(c.ID), IDPrefix):
		return fmt.Errorf("%w: a calendar ID starts with %q", ErrInvalid, IDPrefix)
	case c.Owner.Validate() != nil:
		return c.Owner.Validate()
	case c.Name == "" || len(c.Name) > MaxNameLen:
		return fmt.Errorf("%w: a calendar name has 1 to %d bytes", ErrInvalid, MaxNameLen)
	case c.WorkingDays.Empty():
		return fmt.Errorf("%w: a calendar needs at least one working day", ErrInvalid)
	case c.DayStart < 0 || c.DayEnd > MinutesPerDay || c.DayStart >= c.DayEnd:
		return fmt.Errorf("%w: working hours must start before they end, within one day", ErrInvalid)
	case len(c.Feeds) > MaxFeeds:
		return fmt.Errorf("%w: a calendar has at most %d feeds", ErrInvalid, MaxFeeds)
	default:
		return nil
	}
}

func (c Calendar) validateFeeds() error {
	seen := map[string]bool{}

	for _, f := range c.Feeds {
		if f.ID == "" || len(f.ID) > MaxFeedIDLen {
			return fmt.Errorf("%w: a feed ID has 1 to %d bytes", ErrInvalid, MaxFeedIDLen)
		}

		if seen[f.ID] {
			return fmt.Errorf("%w: the feed ID %q is used twice", ErrInvalid, f.ID)
		}

		seen[f.ID] = true

		if err := egress.CheckURL(f.URL, egress.HTTPSOnly); err != nil {
			return fmt.Errorf("%w: feed %q: %w", ErrInvalid, f.ID, err)
		}
	}

	return nil
}

func (c Calendar) validateHolidays() error {
	manual := 0

	for _, h := range c.Holidays {
		if err := h.Validate(); err != nil {
			return err
		}

		if h.Source == SourceManual {
			manual++
		}
	}

	if manual > MaxHolidays {
		return fmt.Errorf("%w: a calendar has at most %d manual holidays", ErrInvalid, MaxHolidays)
	}

	return nil
}

// Validate returns ErrInvalid for a holiday that is not a real date, whose
// name is too long, or whose source is unknown. A holiday of SourceFeed
// names its feed; a manual holiday names none.
func (h Holiday) Validate() error {
	switch {
	case !h.Date.Valid():
		return fmt.Errorf("%w: holiday %s is not a real date", ErrInvalid, h.Date)
	case len(h.Name) > MaxHolidayNameLen:
		return fmt.Errorf("%w: a holiday name has at most %d bytes", ErrInvalid, MaxHolidayNameLen)
	case h.Source == SourceManual && h.Feed == "":
		return nil
	case h.Source == SourceFeed && h.Feed != "":
		return nil
	default:
		return fmt.Errorf("%w: a holiday is manual with no feed, or from a feed that it names", ErrInvalid)
	}
}
