package idempotency

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidRetention reports a retention that is shorter than RecordTTL.
var ErrInvalidRetention = errors.New("invalid idempotency retention")

// Retention is how long a store keeps a replay record. Make a value with
// NewRetention: a store cannot keep a record for less than RecordTTL, so a
// retry that the retention covers always finds its answer.
type Retention struct {
	period time.Duration
}

// NewRetention returns the retention of period, or ErrInvalidRetention when
// period is shorter than RecordTTL.
func NewRetention(period time.Duration) (Retention, error) {
	if period < RecordTTL {
		return Retention{}, fmt.Errorf(
			"%w: the retention is %s, the minimum is %s", ErrInvalidRetention, period, RecordTTL,
		)
	}

	return Retention{period: period}, nil
}

// JobStartRetention returns the retention of a job-start record: RecordTTL,
// which is 24 hours.
func JobStartRetention() Retention {
	return Retention{period: RecordTTL}
}

// Period returns the retention period.
func (r Retention) Period() time.Duration {
	return r.period
}

// Expired reports whether a record made at created is older than the
// retention at now. A record that is exactly as old as the retention is not
// expired: the client that sends the key again after more than the retention
// gets a new record.
func (r Retention) Expired(created, now time.Time) bool {
	return now.Sub(created) > r.period
}
