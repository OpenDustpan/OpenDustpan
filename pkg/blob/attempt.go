package blob

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrAttemptExpired reports the commit of a write attempt whose start time is
// outside the attempt window, by the clock of the database: the attempt started
// more than AttemptDeadline before the commit, or more than AttemptSkew after
// it. The attempt does not count, no row names its blob, and the blob is
// garbage.
var ErrAttemptExpired = errors.New("write attempt is outside its time window")

// AttemptDeadline is the time that one write attempt has for the write of its
// blob and the fenced commit of the row that names the blob. The store that
// commits the row refuses an attempt that is older, by the clock of the
// database, with ErrAttemptExpired.
const AttemptDeadline = 30 * time.Minute

// AttemptSkew is how far the start time of an attempt may be ahead of the clock
// of the database. The writer takes the start time from its own clock. A writer
// with a clock that is further ahead could commit later than the deadline
// allows, so its commit is refused.
const AttemptSkew = 2 * time.Minute

// attemptLease is the lease of an update or a claim, which the grace period of
// an unnamed blob adds to the attempt deadline.
const attemptLease = 30 * time.Second

// UnnamedGracePeriod is the age that a blob that no row names must have before
// a sweep deletes it. It is the attempt deadline, plus the skew, plus the
// lease, plus a margin for the difference between the clock of the blob store,
// the clock of the sweep and the clock of the database. A blob of an attempt that can
// still commit is younger than this, because the commit of an older attempt
// fails with ErrAttemptExpired.
const UnnamedGracePeriod = time.Hour

// UnnamedMargin is the part of UnnamedGracePeriod that is left after the attempt
// deadline, the skew and the lease. It is the largest difference between the
// clocks that a sweep tolerates.
const UnnamedMargin = UnnamedGracePeriod - AttemptDeadline - AttemptSkew - attemptLease

// CheckAttempt returns ErrAttemptExpired when an attempt that started at
// started cannot commit at now: started is more than AttemptDeadline before
// now, or more than AttemptSkew after it. A zero started is always refused, so
// a writer that sets no start time can never commit. The store calls it inside
// the transaction of the commit, with now from the clock of the database.
func CheckAttempt(started, now time.Time) error {
	switch {
	case started.IsZero():
		return fmt.Errorf("%w: the attempt has no start time", ErrAttemptExpired)
	case now.Sub(started) > AttemptDeadline:
		return fmt.Errorf("%w: the attempt started %s before the commit, the limit is %s",
			ErrAttemptExpired, now.Sub(started).Round(time.Second), AttemptDeadline)
	case started.Sub(now) > AttemptSkew:
		return fmt.Errorf("%w: the attempt starts %s after the clock of the database, the limit is %s",
			ErrAttemptExpired, started.Sub(now).Round(time.Second), AttemptSkew)
	default:
		return nil
	}
}

// ErrClockSkew reports a sweep that deletes nothing in its run, because the
// clock of the instance and the clock of the database differ by more than
// AttemptSkew, or because the sweep cannot read the clock of the database. The
// grace period holds only when the clocks agree, so the sweep fails closed.
var ErrClockSkew = errors.New("the clock of the instance and the clock of the database do not agree")

// DatabaseClock reads the clock of the database. A sweep takes "now" from it,
// never from the clock of an instance, so a fast clock on one instance cannot
// make a young blob look old.
type DatabaseClock interface {
	// DatabaseNow returns the time on the clock of the database.
	DatabaseNow(ctx context.Context) (time.Time, error)
}

// SweepNow returns the time that one run of a sweep uses as now: the clock of
// the database. It returns ErrClockSkew when c is nil, when the read fails, or
// when instance, the clock of the instance, differs from the clock of the
// database by more than AttemptSkew. The run then deletes nothing.
func SweepNow(ctx context.Context, c DatabaseClock, instance time.Time) (time.Time, error) {
	if c == nil {
		return time.Time{}, fmt.Errorf("%w: the sweep has no database clock", ErrClockSkew)
	}

	now, err := c.DatabaseNow(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: read the clock of the database: %w", ErrClockSkew, err)
	}

	if diff := instance.Sub(now).Abs(); diff > AttemptSkew {
		return time.Time{}, fmt.Errorf("%w: they differ by %s, the limit is %s",
			ErrClockSkew, diff.Round(time.Second), AttemptSkew)
	}

	return now, nil
}

// OldEnough reports whether a blob that the store last modified at modified
// is older than UnnamedGracePeriod at now. A zero modified is never old: the
// store did not say when it wrote the blob, so a sweep keeps it.
func OldEnough(modified, now time.Time) bool {
	return !modified.IsZero() && now.Sub(modified) >= UnnamedGracePeriod
}
