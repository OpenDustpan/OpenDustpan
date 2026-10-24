// Package ident validates and generates the identifiers shared by the domain
// packages.
package ident

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const idLength = 20

// MaxEcho is the most characters of a refused value that Echo repeats.
const MaxEcho = 40

// RuleError reports a value that fails Valid. Its text is the echo of the
// value and the rule of Valid, so each domain sentinel that wraps it states
// the same rule.
type RuleError struct {
	// Echo is the value as Echo returns it.
	Echo string
	// MaxLen is the most characters that the rule allows.
	MaxLen int
}

// Error returns the echo of the value and the rule.
func (e *RuleError) Error() string {
	return fmt.Sprintf("%s: use 1 to %d ASCII letters, digits, -, _ and ., not starting with - or .", e.Echo, e.MaxLen)
}

// Check returns nil when Valid(s, maxLen) is true. Else it returns a
// *RuleError with the echo of s. An empty s gets the same text as an invalid
// one.
func Check(s string, maxLen int) error {
	if Valid(s, maxLen) {
		return nil
	}

	return &RuleError{Echo: Echo(s), MaxLen: maxLen}
}

// Echo returns s quoted, for an error text. A value of more than MaxEcho
// characters is cut after MaxEcho characters, and the text says that it is
// cut and gives the count of characters of s. Quoting escapes line breaks
// and control characters, so the echo cannot add a line to a log.
func Echo(s string) string {
	count := 0

	for i := range s {
		if count == MaxEcho {
			return fmt.Sprintf("%s... (cut, %d characters)", strconv.Quote(s[:i]), utf8.RuneCountInString(s))
		}

		count++
	}

	return strconv.Quote(s)
}

// Valid reports whether s is 1..maxLen characters of ASCII letters, digits,
// '-', '_' and '.', and does not start with '.' or '-'.
func Valid(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen || s[0] == '.' || s[0] == '-' {
		return false
	}

	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}

// ValidSlug reports whether s is 1..maxLen bytes of lowercase ASCII letters,
// digits and '-', with a letter first.
func ValidSlug(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen || s[0] < 'a' || s[0] > 'z' {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// NewID returns prefix followed by 20 random lowercase letters and digits, for
// opaque identifiers such as update and workspace IDs. IDs are not secrets.
func NewID(prefix string) string {
	return prefix + strings.ToLower(rand.Text()[:idLength])
}
