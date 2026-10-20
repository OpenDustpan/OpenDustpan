package locale

import (
	"fmt"
	"math"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// arabicIndicRegion reports whether a region is one of the regions where Arabic text uses Arabic-Indic
// digits by default, as in CLDR 47 (defaultNumberingSystem of the ar-XX
// locales). Every other Arabic locale, including ar and ar-001, uses Western
// digits. This is the data that current browsers use. An engine with CLDR 45
// or older shows Arabic-Indic digits for bare ar.
func arabicIndicRegion(region string) bool {
	switch region {
	case "BH", "DJ", "EG", "ER", "IL", "IQ", "JO", "KM", "KW", "LB", "MR",
		"OM", "PS", "QA", "SA", "SD", "SO", "SS", "SY", "TD", "YE":
		return true
	default:
		return false
	}
}

// DefaultDigits returns the digits that a formatting locale uses when the
// person sets no override. The result is DigitsLatin or DigitsArabic only.
// A locale with other default digits (for example fa, which uses arabext)
// gets DigitsLatin. The data of x/text gives Western digits for a regional
// Arabic tag such as ar-EG, so this function holds the rule and the
// Formatter always names the digits it wants.
func DefaultDigits(formattingLocale string) (Digits, error) {
	tag, ok := checkTag(formattingLocale)
	if !ok {
		return "", ErrInvalidFormattingLocale
	}
	return digitsOf(tag), nil
}

func digitsOf(tag language.Tag) Digits {
	base, _ := tag.Base()
	if base.String() != "ar" {
		return DigitsLatin
	}
	// Only a region in the tag counts. A region that x/text adds for a bare
	// ar is not Exact, and bare ar uses Western digits.
	if region, conf := tag.Region(); conf == language.Exact && arabicIndicRegion(region.String()) {
		return DigitsArabic
	}
	return DigitsLatin
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
		effective = digitsOf(tag)
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

// hebrewCategory is the Hebrew rule of CLDR 47 for a whole count: one for 1,
// two for 2, other for the rest.
func hebrewCategory(n int64) Category {
	const two = 2
	switch n {
	case 1:
		return One
	case two:
		return Two
	default:
		return Other
	}
}

// reduceCount maps a whole count to a non-negative count with the same
// plural category. The rules of the supported languages use n mod 10 and
// n mod 100, and other languages use n mod 1e6, and the rules compare with
// values below 1e6. The code keeps n mod 1e9, a multiple of all three, and
// adds 1e9 to a count of 1e9 or more so that it stays above every compared
// value. The value fits in a 32-bit int.
func reduceCount(n int64) int64 {
	const billion = 1_000_000_000
	big := n >= billion || n <= -billion
	rest := n % billion
	if rest < 0 {
		rest = -rest
	}
	if big {
		rest += billion
	}
	return rest
}

// PluralCategory returns the plural category of a whole count for a
// language. The language of the text decides the plural rule, not the
// formatting locale. It returns ErrInvalidLanguage for a tag with a wrong
// shape. A negative count has the category of its absolute value.
func PluralCategory(languageTag string, n int64) (Category, error) {
	tag, ok := checkTag(languageTag)
	if !ok {
		return "", ErrInvalidLanguage
	}
	n = reduceCount(n)
	// CLDR 42 and later removed the many form of Hebrew, and x/text still
	// gives it, so Hebrew has its own rule.
	if base, _ := tag.Base(); base.String() == "he" {
		return hebrewCategory(n), nil
	}
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
	case plural.Other:
		return Other, nil
	default:
		// Fail closed: a future version of x/text may add a form.
		return "", ErrUnknownPlural
	}
}
