package server

import "time"

// The limits of the begin of a register ceremony, for the tests.
const (
	PasskeyBeginLimitForTest  = passkeyBeginLimit
	PasskeyBeginWindowForTest = passkeyBeginWindow
	PasskeyBeginPeopleForTest = passkeyBeginPeople
	MaxPasskeyBodyForTest     = maxPasskeyBody
)

// BeginLimiterForTest is the limiter of the begins of a register ceremony.
type BeginLimiterForTest struct {
	limiter *beginLimiter
}

// NewBeginLimiterForTest returns an empty limiter.
func NewBeginLimiterForTest() *BeginLimiterForTest {
	return &BeginLimiterForTest{limiter: newBeginLimiter()}
}

// Allow counts one begin of the person at now.
func (b *BeginLimiterForTest) Allow(person string, now time.Time) (time.Duration, bool) {
	return b.limiter.allow(person, now)
}
