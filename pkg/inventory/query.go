package inventory

import (
	"strings"
	"unicode"
	"unicode/utf8"
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
	MaxLabelValueLen = 256
)

// ValidLabelKey reports whether s is a label key: 1 to MaxLabelKeyLen letters,
// digits and the characters . _ : / -.
func ValidLabelKey(s string) bool {
	if s == "" || len(s) > MaxLabelKeyLen {
		return false
	}

	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}

	return true
}

// ValidLabelValue reports whether s is a label value: 0 to MaxLabelValueLen
// characters of valid UTF-8, with no control character.
func ValidLabelValue(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= MaxLabelValueLen && !HasControl(s)
}

// HasControl reports whether s holds a control character.
func HasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}
