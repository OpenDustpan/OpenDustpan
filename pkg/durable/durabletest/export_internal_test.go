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
	BreakMaxVersions   = breakMaxVersions
	BreakTickBacklog   = breakTickBacklog
	BreakNoSchedule    = breakNoScheduleOnNoWork
)

// BrokenHarness returns a harness on a store in memory that breaks rule.
func BrokenHarness(rule string) Harness {
	return newMemDB(rule).harness()
}

// BrokenScheduleHarness returns a schedule harness on a store in memory
// that breaks rule.
func BrokenScheduleHarness(rule string) ScheduleHarness {
	return newMemDB(rule).scheduleHarness()
}
