// Package freeze holds the schedules of change freezes: a one-time window,
// a weekly recurring freeze, or a freeze that follows the business calendar
// of the namespace. A schedule makes freezes. The job model keeps each
// freeze and checks it at each dispatch, so a scheduled freeze and a freeze
// that a person creates by hand stop the same work in the same way.
//
// A durable operation runs Plan for each schedule on a schedule of its own,
// and writes the occurrences of the next Horizon to the Sink. The
// dispatch check never computes a recurrence: it reads stored freezes only.
package freeze

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store and Sink
// implementations.
var (
	// ErrInvalid reports a schedule that fails validation.
	ErrInvalid = errors.New("invalid freeze schedule")
	// ErrNotFound reports an unknown schedule ID.
	ErrNotFound = errors.New("freeze schedule not found")
	// ErrExists reports a create with an ID that a schedule already has.
	ErrExists = errors.New("freeze schedule already exists")
	// ErrConflict reports a write that names an older version of the
	// schedule.
	ErrConflict = errors.New("freeze schedule changed since it was read")
)

// Bounds and defaults of a schedule.
const (
	// IDPrefix starts every schedule ID.
	IDPrefix = "fzs-"
	// FreezeIDPrefix starts every freeze ID, as the job model writes it.
	FreezeIDPrefix = "frz-"
	// MaxNameLen caps the name of a schedule, in bytes.
	MaxNameLen = 100
	// MaxReasonLen caps the reason of a schedule, in bytes.
	MaxReasonLen = 500
	// MaxWeeklyLength caps the length of one weekly occurrence. A longer
	// freeze is a window. The occurrences of a weekly rule never overlap,
	// so the days of the rule can make the bound shorter.
	MaxWeeklyLength = 6 * 24 * time.Hour
	// MaxDaysBefore caps the business days before a holiday that a
	// calendar rule freezes.
	MaxDaysBefore = 5
	// Horizon is how far ahead Plan writes occurrences.
	Horizon = 14 * 24 * time.Hour
	// DefaultNotice is how long before the start of an occurrence the
	// server sends the event that a freeze comes.
	DefaultNotice = 24 * time.Hour
)

// Kind is the kind of a schedule.
type Kind string

// Kinds of a schedule.
const (
	// KindWindow is one freeze from a start to an end.
	KindWindow Kind = "window"
	// KindWeekly repeats on days of the week, at a time of day, for a
	// length.
	KindWeekly Kind = "weekly"
	// KindCalendar follows the business calendar of the namespace.
	KindCalendar Kind = "calendar"
)

// Window is the rule of KindWindow. End is after Start.
type Window struct {
	Start time.Time
	End   time.Time
}

// Weekly is the rule of KindWeekly. An occurrence starts at Start on each
// day of Days, in the time zone Zone, and lasts Length. For example Days
// Friday, Start 15:00 and Length 66 hours freeze each weekend until Monday
// 09:00. A start that a daylight saving change skips moves forward.
type Weekly struct {
	Days   calendar.WeekdaySet
	Start  calendar.Clock
	Length time.Duration
	Zone   string
}

// CalendarRule is the rule of KindCalendar. It reads the calendar that
// applies to the namespace. Each field that is true adds whole periods,
// in the zone of the calendar:
//   - DaysOff: each day that is not a working day.
//   - Holidays: each holiday.
//   - OutsideHours: on each business day, the time before the working
//     hours start and after they end.
//   - DaysBefore: the last DaysBefore business days before each holiday,
//     for example 1 to freeze the business day before a long weekend.
type CalendarRule struct {
	DaysOff      bool
	Holidays     bool
	OutsideHours bool
	DaysBefore   int
}

// Schedule makes freezes in a namespace. Exactly one rule applies, the one
// of Kind. A schedule makes occurrences only from From, and until Until
// when Until is set. A schedule that is not Enabled makes none. Notice is
// how long before each occurrence the server sends the event that a freeze
// comes, and 0 means DefaultNotice.
type Schedule struct {
	ID        string
	Namespace namespace.Name
	Name      string
	Reason    string
	Kind      Kind
	Window    Window
	Weekly    Weekly
	Calendar  CalendarRule
	From      time.Time
	Until     time.Time
	Enabled   bool
	Notice    time.Duration
	CreatedBy string
	Version   int64
}

// Occurrence is one freeze that a schedule makes. Start and End are in
// UTC. The job model stores it as a freeze with the ID OccurrenceID(Schedule,
// Start) and the reason of the schedule.
type Occurrence struct {
	Schedule string
	Start    time.Time
	End      time.Time
}

// OccurrenceID returns the ID of the freeze of the occurrence of schedule that
// starts at start. The ID is stable, so that a second run of Plan never
// makes a second freeze for one occurrence.
func OccurrenceID(schedule string, start time.Time) string {
	return FreezeIDPrefix + strings.TrimPrefix(schedule, IDPrefix) + "-" +
		strconv.FormatInt(start.UnixMilli(), 10)
}

// Validate returns ErrInvalid for a schedule that breaks a bound, or whose
// rule does not fit its kind.
func (s Schedule) Validate() error {
	switch {
	case !strings.HasPrefix(s.ID, IDPrefix):
		return fmt.Errorf("%w: a schedule ID starts with %q", ErrInvalid, IDPrefix)
	case s.Namespace == "":
		return fmt.Errorf("%w: a schedule needs a namespace", ErrInvalid)
	case s.Name == "" || len(s.Name) > MaxNameLen:
		return fmt.Errorf("%w: a schedule name has 1 to %d bytes", ErrInvalid, MaxNameLen)
	case s.Reason == "" || len(s.Reason) > MaxReasonLen:
		return fmt.Errorf("%w: a schedule reason has 1 to %d bytes", ErrInvalid, MaxReasonLen)
	case !s.Until.IsZero() && !s.Until.After(s.From):
		return fmt.Errorf("%w: the end of a schedule must be after its start", ErrInvalid)
	case s.Notice < 0:
		return fmt.Errorf("%w: a notice cannot be negative", ErrInvalid)
	}

	return s.validateRule()
}
