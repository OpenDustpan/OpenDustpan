package forge

import "time"

// AdmitWithLimits runs one admit of a limiter that holds limits, with no
// check of the limits by NewService. It lets a test send limits that
// NewService refuses straight to the limiter.
func AdmitWithLimits(limits CheckLimits, now time.Time) (time.Duration, bool) {
	return newCheckLimiter(limits).admit("acme/conn", "github/101", now)
}
