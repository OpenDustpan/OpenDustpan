package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/history"
)

// jobRetention holds the retention periods that the job recorder gives to
// each new plan and each new version of a job log.
type jobRetention = history.RetentionSettings

// defaultJobRetention returns the periods before the options: the history
// defaults.
func defaultJobRetention() jobRetention {
	return history.DefaultRetentionSettings()
}

// WithRetention sets the retention periods of stored plans and of job log
// versions. The recorder stores the end of the period with each item when it
// creates the item, so a change applies to new items only. Without this
// option the periods are history.DefaultRetention. A period outside the range
// from history.MinRetention to history.MaxRetention makes NewServer fail with
// ErrInvalidOption and history.ErrInvalidRetention.
func WithRetention(r history.RetentionSettings) Option {
	return func(o *settings) { o.jobRetention = r }
}

// checkJobRetention refuses a retention period outside the range that
// history.RetentionSettings.Validate allows. Defense in depth: the recorder
// checks the same range when it starts, and the binary checks the flags
// before it calls NewServer. This check stays so that a caller of the SDK
// that skips both gets an error from NewServer and never an item with a
// wrong expiry.
func checkJobRetention(r jobRetention) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: WithRetention: %w", ErrInvalidOption, err)
	}

	return nil
}
