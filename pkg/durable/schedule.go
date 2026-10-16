package durable

import (
	"context"
	"fmt"
	"time"
)

// ScheduleSpec defines a periodic job: every Interval, one instance starts
// one operation of Kind at Version with Input.
type ScheduleSpec struct {
	Name     string
	Kind     Kind
	Version  int
	Interval time.Duration
	Input    []byte
}

// Validate returns ErrInvalid when spec has an empty name or kind, a
// negative version or an interval that is not positive.
func (spec ScheduleSpec) Validate() error {
	if spec.Name == "" || spec.Kind == "" || spec.Version < 0 || spec.Interval <= 0 {
		return fmt.Errorf("%w: schedule %q needs a name, a kind, a version of 0 or more and a positive interval",
			ErrInvalid, spec.Name)
	}

	return nil
}

// Schedule is the stored state of a schedule. NextRunAt and Now are on the
// clock of the store, so their difference is the wait until the next tick.
type Schedule struct {
	Name     string
	Interval time.Duration
	// NextRunAt is the time from which the next tick is due.
	NextRunAt time.Time
	// LastClaimedAt is the time of the last tick that an instance claimed,
	// or the zero time.
	LastClaimedAt time.Time
	// Now is the clock of the store when it read the schedule.
	Now time.Time
}

// Wait returns the time until the next tick is due, or 0 when it is due.
func (s Schedule) Wait() time.Duration {
	return max(s.NextRunAt.Sub(s.Now), 0)
}

// Tick is the result of a claim of a schedule: the operation that the claim
// started, and the schedule after the claim.
type Tick struct {
	Operation Operation
	Schedule  Schedule
}

// TickKey returns the start key of the operation of the tick of the schedule
// called name that was due at dueAt. Two claims of one tick give one key.
func TickKey(name string, dueAt time.Time) string {
	return fmt.Sprintf("schedule:%s@%d", name, dueAt.UnixMilli())
}

// ScheduleStore is the persistence port for schedules. A store adapter that
// implements Store implements it too, in its own SQL, and runs the suite
// durabletest.RunSchedules. Every time comes from the clock of the store.
type ScheduleStore interface {
	// EnsureSchedule creates the schedule of spec, due at once, when it does
	// not exist. When it exists, it keeps NextRunAt, and sets the interval of
	// spec. A shorter interval moves NextRunAt to now plus the interval when
	// that is earlier. It returns ErrInvalid for a spec that fails Validate.
	EnsureSchedule(ctx context.Context, spec ScheduleSpec) (Schedule, error)

	// GetSchedule returns the schedule called name, or ErrNotFound.
	GetSchedule(ctx context.Context, name string) (Schedule, error)

	// ClaimTick claims the tick of the schedule of spec when it is due, and
	// starts its operation, in one transaction. One conditional update
	// claims the tick: it requires NextRunAt to be now or earlier, and sets
	// NextRunAt to now plus the interval and LastClaimedAt to now. So the
	// ticks that no instance ran collapse into one, and of several
	// concurrent claims at most one succeeds. The operation has the key
	// TickKey(spec.Name, the NextRunAt before the claim), and spec.Kind,
	// spec.Version and spec.Input, with the rules of Store.Start. It returns
	// ErrNoWork, with the schedule, when the tick is not due, ErrNotFound for
	// a schedule that does not exist, and ErrInvalid for a spec that fails
	// Validate.
	ClaimTick(ctx context.Context, spec ScheduleSpec) (Tick, error)
}
