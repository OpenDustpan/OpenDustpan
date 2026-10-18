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
	"slices"
	"strings"
	"time"

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

// at returns the time of c on the date d in loc. A time that a daylight
// saving change skips moves forward, as time.Date does.
func (c Clock) at(d Date, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, int(c)/minutesPerHour, int(c)%minutesPerHour, 0, 0, loc)
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
// whole day, also when the feed event covers some hours only.
type Holiday struct {
	Date Date
	Name string
	// Source is SourceManual, or SourceFeed with the ID of the feed in
	// Feed.
	Source Source
	Feed   string
}

// Feed is an iCal feed that the server reads holidays from on a schedule.
// URL is an https URL. The server reads it through the egress rules of the
// installation, refuses a private or loopback address, and never sends a
// credential with it.
type Feed struct {
	ID  string
	URL string
	// Status is what the last read of the feed gave.
	Status FeedStatus
}

// FeedStatus is the outcome of the last read of a feed.
type FeedStatus struct {
	// ReadAt is the time of the last read, or the zero time before the
	// first read.
	ReadAt time.Time
	// Holidays is the number of holidays that the last good read gave.
	Holidays int
	// Skipped is the number of events of the last good read that were not
	// whole days, or that were outside the import window.
	Skipped int
	// Error is the text of the error of the last read, or empty. A failed
	// read keeps the holidays of the last good read.
	Error string
	// ETag is the validator of the last good read, for a conditional read.
	ETag string
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

// Calendar is a business calendar. Zone is an IANA time zone name.
// WorkingDays and the working hours from DayStart to DayEnd say when
// business happens. Holidays remove whole days. Version grows on each
// change, and a write names the version that it read.
type Calendar struct {
	ID          ID
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

// Validate returns ErrInvalid for a calendar that breaks a bound, and
// ErrUnknownZone for a zone that the server cannot load.
func (c Calendar) Validate() error {
	if err := c.validateFields(); err != nil {
		return err
	}

	if _, err := c.Location(); err != nil {
		return err
	}

	return c.validateHolidays()
}

// Location loads the time zone of the calendar. It returns ErrUnknownZone
// for a name that the time zone database does not hold.
func (c Calendar) Location() (*time.Location, error) {
	if c.Zone == "" {
		return nil, fmt.Errorf("%w: empty zone", ErrUnknownZone)
	}

	loc, err := time.LoadLocation(c.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, c.Zone)
	}

	return loc, nil
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

func (c Calendar) validateHolidays() error {
	manual := 0

	for _, h := range c.Holidays {
		if !h.Date.Valid() {
			return fmt.Errorf("%w: holiday %s is not a real date", ErrInvalid, h.Date)
		}

		if len(h.Name) > MaxHolidayNameLen {
			return fmt.Errorf("%w: a holiday name has at most %d bytes", ErrInvalid, MaxHolidayNameLen)
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
