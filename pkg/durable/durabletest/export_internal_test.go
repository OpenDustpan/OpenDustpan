package durabletest

// The rules that a broken store breaks, for the sensitivity tests.
const (
	BreakToken = breakToken
	BreakLive  = breakLive
)

// BrokenHarness returns a harness on a store in memory that breaks rule.
func BrokenHarness(rule string) Harness {
	return newMemDB(rule).harness()
}
