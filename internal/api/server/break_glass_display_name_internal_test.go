// This file exports two helpers to the black-box tests of the package. The
// break-glass routes decode the request body as JSON, and JSON decoding
// replaces each invalid UTF-8 byte with U+FFFD. So no request can bring an
// invalid UTF-8 display name to the UTF-8 check, and only a call from inside
// the package can test that check alone.

package server

// CheckBreakGlassDisplayNameForTest calls the display name check of the
// break-glass routes alone, so a black-box test reaches the UTF-8 branch that
// JSON decoding never yields.
func CheckBreakGlassDisplayNameForTest(s string) error {
	return checkBreakGlassDisplayName(s)
}

// BreakGlassDisplayNameRuneForTest reports whether the allowlist of the
// break-glass display name accepts r.
func BreakGlassDisplayNameRuneForTest(r rune) bool {
	return breakGlassDisplayNameRune(r)
}
