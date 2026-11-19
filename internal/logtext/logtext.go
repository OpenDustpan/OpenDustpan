// Package logtext prepares a stored or path value for a log line.
package logtext

import (
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
)

// MaxBytes bounds a value that a log line repeats.
const MaxBytes = 200

// marker ends a value that Clean cut.
const marker = "..."

// Clean returns s for a log line: it removes each hidden rune, that is each
// rune for which ident.IsHiddenRune is true (control characters such as ESC,
// CR, LF and tab, format characters such as U+202E, and the line and paragraph
// separators U+2028 and U+2029). It then cuts the result
// to MaxBytes bytes on a UTF-8 boundary. A cut value ends with "...". A stored
// name, a unit ID or an error text can come from a damaged or hostile row, so
// a log line must not hold a hidden rune from it, whatever the handler or the
// sink does with quotes, and a damaged row must not make a log line of any
// size. The strip comes before the cut, so the strip does not hide a long value.
func Clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if ident.IsHiddenRune(r) {
			return -1
		}

		return r
	}, s)

	if len(s) <= MaxBytes {
		return s
	}

	cut := MaxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut] + marker
}
