package durabletest

// The rules that a broken store breaks, for the sensitivity tests.
const (
	BreakToken         = breakToken
	BreakExpiry        = breakExpiry
	BreakEndExpiry     = breakEndExpiry
	BreakTakeoverSteps = breakTakeoverSteps
	BreakClock         = breakClock
	BreakTokenStays    = breakTokenStays
	BreakLive          = breakLive
	BreakTxStart       = breakTxStart
	BreakExpiryCount   = breakExpiryCount
)

// BrokenHarness returns a harness on a store in memory that breaks rule.
func BrokenHarness(rule string) Harness {
	return newMemDB(rule).harness()
}
