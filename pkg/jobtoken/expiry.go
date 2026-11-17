package jobtoken

import "time"

// Expiry returns the expiry of the job token of one step: the earlier of the
// end of the claim and the end of the step timeout plus the grace period.
// stepStart is the start of the step, which is the time of the credentials
// request: the launcher asks for the token just before it runs the step. The
// caller refuses an issue whose expiry is not after its clock, so a past time
// here ends in a refusal and no token.
func Expiry(claimEnd, stepStart time.Time, stepTimeout, grace time.Duration) time.Time {
	stepEnd := stepStart.Add(stepTimeout).Add(grace)

	if claimEnd.Before(stepEnd) {
		return claimEnd
	}

	return stepEnd
}
