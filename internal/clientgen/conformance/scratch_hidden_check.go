package conformance

import "unicode"

// scratchHiddenCheck is a temporary lint probe, removed before commit.
func scratchHiddenCheck(r rune) bool {
	return unicode.IsControl(r)
}
