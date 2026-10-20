package locale

import (
	"fmt"
	"math"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// latinArabicRegions are the regions where Arabic text uses Western digits
// by default. Every other Arabic region uses Arabic-Indic digits.
var latinArabicRegions = map[string]bool{
	"AE": true, "DZ": true, "EH": true, "LY": true, "MA": true, "TN": true,
}

// DefaultDigits returns the digits that a formatting locale uses when the
// person sets no override. The data of x/text gives Western digits for a
// regional Arabic tag such as ar-EG, so this function holds the rule and the
// Formatter always names the digits it wants.
func DefaultDigits(formattingLocale string) (Digits, error) {
	tag, ok := checkTag(formattingLocale)
	if !ok {
		return "", ErrInvalidFormattingLocale
	}
	return defaultDigits(tag), nil
}

func defaultDigits(tag language.Tag) Digits {
	base, _ := tag.Base()
	if base.String() != "ar" {
		return DigitsLatin
	}
	if region, conf := tag.Region(); conf == language.Exact && latinArabicRegions[region.String()] {
		return DigitsLatin
	}
	return DigitsArabic
}

// Formatter formats numbers for one formatting locale and one digits
// choice. It holds no state that changes, so it is safe for concurrent use.
type Formatter struct {
	digits  Digits
	printer *message.Printer
}

// NewFormatter returns a Formatter for a formatting locale. A zero digits
// follows the formatting locale, and any other value overrides it. An
// invalid locale or digits returns an error: the function fails closed and
// never falls back to a default.
func NewFormatter(formattingLocale string, digits Digits) (*Formatter, error) {
	// Defense in depth: the code that stores a preference validates both
	// values too. This check stays so that a new caller cannot skip it.
	if err := ValidateFormattingLocale(formattingLocale); err != nil {
		return nil, err
	}
	if digits != "" {
		if _, err := ParseDigits(string(digits)); err != nil {
			return nil, err
		}
	}
	tag, _ := checkTag(formattingLocale)
	effective := digits
	if effective == "" {
		effective = defaultDigits(tag)
	}
	// The tag has no -u- extension (checked above), so the digits extension
	// is the only one and the parse cannot fail for a valid Digits.
	withDigits, err := language.Parse(tag.String() + "-u-nu-" + string(effective))
	if err != nil {
		return nil, fmt.Errorf("%w (got %q)", ErrInvalidDigits, truncate(string(effective)))
	}
	return &Formatter{digits: effective, printer: message.NewPrinter(withDigits)}, nil
}

// Digits returns the digits that the Formatter uses: the override, or the
// default of the formatting locale.
func (f *Formatter) Digits() Digits { return f.digits }

// Int formats a whole number with the group separators of the locale.
func (f *Formatter) Int(n int64) string {
	return f.printer.Sprint(number.Decimal(n))
}

// Decimal formats v with exactly fractionDigits digits after the decimal
// separator. It returns ErrNotFinite for NaN and for an infinity, and
// ErrFractionDigits when fractionDigits is not 0 to MaxFractionDigits.
func (f *Formatter) Decimal(v float64, fractionDigits int) (string, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", ErrNotFinite
	}
	if fractionDigits < 0 || fractionDigits > MaxFractionDigits {
		return "", ErrFractionDigits
	}
	return f.printer.Sprint(number.Decimal(v, number.Scale(fractionDigits))), nil
}

// Category is a CLDR plural category.
type Category string

// The plural categories. A language uses some of them: English uses one and
// other, Russian and Ukrainian use one, few, many and other, and Arabic uses
// all six.
const (
	Zero  Category = "zero"
	One   Category = "one"
	Two   Category = "two"
	Few   Category = "few"
	Many  Category = "many"
	Other Category = "other"
)

// PluralCategory returns the plural category of a whole count for a
// language. The language of the text decides the plural rule, not the
// formatting locale. It returns ErrInvalidLanguage for a tag with a wrong
// shape. A negative count has the category of its absolute value.
func PluralCategory(languageTag string, n int64) (Category, error) {
	tag, ok := checkTag(languageTag)
	if !ok {
		return "", ErrInvalidLanguage
	}
	// The rules read the last two digits of a count, and the checks "more
	// than 10". A count above the range of int keeps its last nine digits,
	// so its category does not change.
	u := uint64(n)
	if n < 0 {
		u = -u
	}
	const maxInt = uint64(^uint(0) >> 1)
	if u > maxInt {
		u %= 1_000_000_000
	}
	n = int64(u)
	switch plural.Cardinal.MatchPlural(tag, int(n), 0, 0, 0, 0) {
	case plural.Zero:
		return Zero, nil
	case plural.One:
		return One, nil
	case plural.Two:
		return Two, nil
	case plural.Few:
		return Few, nil
	case plural.Many:
		return Many, nil
	default:
		return Other, nil
	}
}
