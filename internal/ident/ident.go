// Package ident validates and generates the identifiers shared by the domain
// packages.
package ident

import (
	"crypto/rand"
	"strings"
)

const idLength = 20

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
