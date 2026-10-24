package inventory

import (
	"strings"
	"unicode"
)

// Limits of the label and reference filters of a unit query. The unit list and
// a saved view read the same query, so both use these values.
const (
	// MaxLabelFilters is the largest number of label filters in one query.
	MaxLabelFilters = 10
	// MaxReferenceFilters is the largest number of reference filters in one
	// query.
	MaxReferenceFilters = 20
	// MaxLabelKeyLen is the largest length of a label key, in bytes.
	MaxLabelKeyLen = 128
	// MaxLabelValueLen is the largest length of a label value, in characters.
	// A value holds ASCII characters only, so a character is one byte.
	MaxLabelValueLen = 256
)

// ValidLabelKey reports whether s is a label key: 1 to MaxLabelKeyLen letters,
// digits and the characters . _ : / -.
func ValidLabelKey(s string) bool {
	if s == "" || len(s) > MaxLabelKeyLen {
		return false
	}

	return labelChars(s)
}

// ValidLabelValue reports whether s is a label value: 0 to MaxLabelValueLen
// letters, digits and the characters . _ : / -, the characters of a key. A
// value holds no space and no character outside ASCII. It refuses any other
// value and never changes it.
func ValidLabelValue(s string) bool {
	return len(s) <= MaxLabelValueLen && labelChars(s)
}

// labelChars reports whether each byte of s is an ASCII letter, a digit or
// one of . _ : / -. It reads bytes, so a byte of a multi-byte character or of
// invalid UTF-8 fails.
func labelChars(s string) bool {
	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}

	return true
}

// HasControl reports whether s holds a control character.
func HasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}
