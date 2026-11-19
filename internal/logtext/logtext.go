// Package logtext prepares a stored or path value for a log line.
package logtext

import (
	"strings"
	"unicode/utf8"
)

// MaxBytes bounds a value that a log line repeats.
const MaxBytes = 200

// marker ends a value that Clean cut.
const marker = "..."

// Clean returns s for a log line: it removes each CR and each LF, and it cuts
// the result to MaxBytes bytes on a UTF-8 boundary. A cut value ends with
// "...". A stored name, a unit ID or an error text can come from a damaged or
// hostile row, so a log line must not hold a line break from it, and a damaged
// row must not make a log line of any size.
func Clean(s string) string {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)

	if len(s) <= MaxBytes {
		return s
	}

	cut := MaxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut] + marker
}
