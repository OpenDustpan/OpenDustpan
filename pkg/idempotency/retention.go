package idempotency

import (
	"fmt"
	"time"
)

// MinRetention is the shortest retention of a job-start key: RecordTTL,
// which is 24 hours.
const MinRetention = RecordTTL

// Retention is how long a job-start key names its job. Make a value with
// NewRetention: a shorter retention is refused. The zero value is not valid,
// so a store that holds it fails each expiry check instead of skipping it.
type Retention struct {
	d time.Duration
}

// NewRetention returns the retention of d, or ErrInvalid when d is shorter
// than MinRetention.
func NewRetention(d time.Duration) (Retention, error) {
	if d < MinRetention {
		var none Retention

		return none, fmt.Errorf("%w: the retention is %s, the minimum is %s", ErrInvalid, d, MinRetention)
	}

	return Retention{d: d}, nil
}

// JobStartRetention returns the retention of a job-start key: 24 hours.
func JobStartRetention() Retention {
	r, err := NewRetention(MinRetention)
	if err != nil {
		var none Retention

		return none
	}

	return r
}

// Expired reports whether a job created at created is older than the
// retention at now. A job of exactly the retention age is not expired. It
// returns ErrInvalid for the zero value.
func (r Retention) Expired(created, now time.Time) (bool, error) {
	if r.d < MinRetention {
		return false, fmt.Errorf("%w: the retention is not set", ErrInvalid)
	}

	return now.Sub(created) > r.d, nil
}
