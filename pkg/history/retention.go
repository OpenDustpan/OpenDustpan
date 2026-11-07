package history

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidRetention reports a retention period that is shorter than
// MinRetention or longer than MaxRetention.
var ErrInvalidRetention = errors.New("invalid retention period")

// DefaultRetention is the retention period of a stored plan and of a
// version of a job log when the installation sets none: 400 days from the
// creation of the item.
const DefaultRetention = 400 * 24 * time.Hour

// MinRetention is the shortest retention period that a setting accepts, so
// that a typing error cannot shred every plan and job log at the next sweep.
const MinRetention = 24 * time.Hour

// MaxRetention is the longest retention period that a setting accepts: ten
// years. A longer period is almost always a typing error, and a huge value
// could overflow the expiry time of an item.
const MaxRetention = 10 * 365 * 24 * time.Hour

// The names of the retention settings, as a flag and as an environment
// variable.
const (
	// PlanRetentionFlag is the flag of the retention period of stored plans.
	PlanRetentionFlag = "plan-retention"
	// PlanRetentionEnv is the environment variable of PlanRetentionFlag.
	PlanRetentionEnv = "DUSTPAN_PLAN_RETENTION"
	// JobLogRetentionFlag is the flag of the retention period of job log
	// versions.
	JobLogRetentionFlag = "job-log-retention"
	// JobLogRetentionEnv is the environment variable of JobLogRetentionFlag.
	JobLogRetentionEnv = "DUSTPAN_JOB_LOG_RETENTION"
)

// RetentionSettings holds the retention periods of an installation. The
// history package owns the defaults and the rule. The job model reads the
// settings when it creates an item, and stores the expiry time with the
// item, so a change of a setting applies to new items only.
type RetentionSettings struct {
	// Plan is the retention period of a stored plan.
	Plan time.Duration
	// JobLog is the retention period of a version of a job log.
	JobLog time.Duration
}

// DefaultRetentionSettings returns the settings of an installation that
// sets no period: DefaultRetention for each kind.
func DefaultRetentionSettings() RetentionSettings {
	return RetentionSettings{Plan: DefaultRetention, JobLog: DefaultRetention}
}

// Validate reports whether each period is from MinRetention to
// MaxRetention. It returns ErrInvalidRetention with the name of the flag
// otherwise.
func (s RetentionSettings) Validate() error {
	if err := ValidateRetention(PlanRetentionFlag, s.Plan); err != nil {
		return err
	}

	return ValidateRetention(JobLogRetentionFlag, s.JobLog)
}

// ValidateRetention reports whether period is from MinRetention to
// MaxRetention. name is the flag of the setting, for the error text. It
// returns ErrInvalidRetention otherwise.
func ValidateRetention(name string, period time.Duration) error {
	if period < MinRetention {
		return fmt.Errorf("%w: --%s is %s, the minimum is %s", ErrInvalidRetention, name, period, MinRetention)
	}

	if period > MaxRetention {
		return fmt.Errorf("%w: --%s is %s, the maximum is %s", ErrInvalidRetention, name, period, MaxRetention)
	}

	return nil
}

// ExpiresAt returns the expiry time of an item made at createdAt with the
// retention period period. The period starts at the creation of the item,
// not at the end of its job.
func ExpiresAt(createdAt time.Time, period time.Duration) time.Time {
	return createdAt.Add(period)
}
