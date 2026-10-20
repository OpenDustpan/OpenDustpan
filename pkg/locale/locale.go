// Package locale holds the language rules of the server: the checks for the
// language tag, the formatting locale tag and the digits preference of a
// person, and the number format and plural rules that follow them.
//
// The package is tool-neutral and has no I/O. The code that stores or serves
// a preference calls the checks here at its boundary. The server checks only
// the shape of a language tag: the console owns the list of languages, so a
// new language needs no server release.
package locale

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// Sentinel errors. Each wraps nothing from x/text, so a caller never sees a
// library error.
var (
	// ErrInvalidLanguage means a language tag has a wrong shape.
	ErrInvalidLanguage = errors.New("language must be a BCP 47 tag of 2 to 35 characters, for example en or ar")
	// ErrInvalidFormattingLocale means a formatting locale tag has a wrong
	// shape, or has a -u- or -x- extension.
	ErrInvalidFormattingLocale = errors.New("formatting locale must be a BCP 47 tag of 2 to 35 characters with no -u- or -x- extension, for example ar-EG")
	// ErrInvalidDigits means a digits value is not one of the valid values.
	ErrInvalidDigits = errors.New("digits must be latn, arab or null")
	// ErrNotFinite means a number to format is not finite.
	ErrNotFinite = errors.New("number must be finite")
	// ErrFractionDigits means a count of fraction digits is out of range.
	ErrFractionDigits = errors.New("fraction digits must be 0 to 9")
)

const (
	minTagLen = 2
	maxTagLen = 35
	// MaxFractionDigits is the largest count of fraction digits that
	// Formatter.Decimal accepts.
	MaxFractionDigits = 9
)

// checkTag applies the checks that language tags and formatting locale tags
// share. It returns the parsed tag. It never changes the input.
func checkTag(s string) (language.Tag, bool) {
	if len(s) < minTagLen || len(s) > maxTagLen {
		return language.Und, false
	}
	// Allowlist of bytes. This stops control characters, spaces, `_`,
	// non-ASCII text and CR or LF before the parser sees the value.
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
		if !ok {
			return language.Und, false
		}
	}
	tag, err := language.Parse(s)
	if err != nil {
		// An unknown subtag is a well-formed tag: the shape is right, and
		// the console owns the list of languages.
		var ve language.ValueError
		if !errors.As(err, &ve) {
			return language.Und, false
		}
	}
	return tag, true
}

// ValidateLanguage returns nil when s has the shape of a language tag: a
// well-formed BCP 47 tag of 2 to 35 characters. It does not check that the
// console has the language. It returns ErrInvalidLanguage for any other
// value, and never changes the value.
func ValidateLanguage(s string) error {
	if _, ok := checkTag(s); !ok {
		return ErrInvalidLanguage
	}
	return nil
}

// ValidateFormattingLocale returns nil when s has the shape of a formatting
// locale: the shape of a language tag with no -u- extension and no -x-
// extension. The preferences hourCycle, firstDayOfWeek and digits are the
// only overrides of a formatting locale, so a tag cannot carry one.
func ValidateFormattingLocale(s string) error {
	tag, ok := checkTag(s)
	if !ok {
		return ErrInvalidFormattingLocale
	}
	// Two layers: the raw text, and the parsed extensions. Each one alone
	// refuses the tag, so a change to the parser cannot let one through.
	lower := strings.ToLower(s)
	if strings.Contains(lower, "-u-") || strings.Contains(lower, "-x-") || strings.HasPrefix(lower, "x-") {
		return ErrInvalidFormattingLocale
	}
	for _, ext := range tag.Extensions() {
		if t := ext.Type(); t == 'u' || t == 'x' {
			return ErrInvalidFormattingLocale
		}
	}
	return nil
}

// Digits is the numbering system of the digits in a number. The zero value
// is the null of the preference: the formatting locale decides.
type Digits string

// The valid values of the digits preference. The list can grow when a new
// language uses other digits.
const (
	// DigitsLatin is the Western digits, 0 to 9.
	DigitsLatin Digits = "latn"
	// DigitsArabic is the Arabic-Indic digits.
	DigitsArabic Digits = "arab"
)

// ParseDigits returns the digits for the text of the preference. A value
// that is not exactly latn or arab, including an empty text and a different
// case, returns ErrInvalidDigits. A null of the preference is not a text:
// the caller maps it to the zero Digits and does not call ParseDigits.
func ParseDigits(s string) (Digits, error) {
	switch d := Digits(s); d {
	case DigitsLatin, DigitsArabic:
		return d, nil
	default:
		return "", fmt.Errorf("%w (got %q)", ErrInvalidDigits, truncate(s))
	}
}

// truncate keeps an error text short when the value is long or hostile.
func truncate(s string) string {
	const limit = 16
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
