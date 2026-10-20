package locale_test

import (
	"errors"
	"math"
	"testing"

	"opendustpan.org/opendustpan/pkg/locale"
)

func TestDefaultDigits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale string
		want   locale.Digits
	}{
		{"ar", locale.DigitsArabic},
		{"ar-EG", locale.DigitsArabic},
		{"ar-SA", locale.DigitsArabic},
		{"ar-AE", locale.DigitsLatin},
		{"ar-MA", locale.DigitsLatin},
		{"ar-DZ", locale.DigitsLatin},
		{"ar-TN", locale.DigitsLatin},
		{"ar-LY", locale.DigitsLatin},
		{"en-US", locale.DigitsLatin},
		{"he", locale.DigitsLatin},
		{"ru", locale.DigitsLatin},
		{"uk-UA", locale.DigitsLatin},
		{"af", locale.DigitsLatin},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			t.Parallel()
			got, err := locale.DefaultDigits(tc.locale)
			if err != nil || got != tc.want {
				t.Fatalf("DefaultDigits(%q) = %q, %v, want %q", tc.locale, got, err, tc.want)
			}
		})
	}
	if _, err := locale.DefaultDigits("not a tag"); !errors.Is(err, locale.ErrInvalidFormattingLocale) {
		t.Fatalf("DefaultDigits(bad) = %v, want ErrInvalidFormattingLocale", err)
	}
}

func TestFormatterNumbers(t *testing.T) {
	t.Parallel()
	const (
		latin  = "1,234,567.891"
		indic  = "١٬٢٣٤٬٥٦٧٫٨٩١"
		spaced = "1 234 567,891"
	)
	tests := []struct {
		name       string
		locale     string
		digits     locale.Digits
		wantDigits locale.Digits
		want       string
	}{
		{"Arabic follows the locale", "ar", "", locale.DigitsArabic, indic},
		{"regional Arabic follows the locale", "ar-EG", "", locale.DigitsArabic, indic},
		{"Arabic in a Western region", "ar-AE", "", locale.DigitsLatin, latin},
		{"Arabic override to Western", "ar", locale.DigitsLatin, locale.DigitsLatin, latin},
		{"regional Arabic override to Western", "ar-EG", locale.DigitsLatin, locale.DigitsLatin, latin},
		{"Arabic override to Arabic-Indic", "ar", locale.DigitsArabic, locale.DigitsArabic, indic},
		{"Western region override to Arabic-Indic", "ar-AE", locale.DigitsArabic, locale.DigitsArabic, indic},
		{"English follows the locale", "en-US", "", locale.DigitsLatin, latin},
		{"English override to Arabic-Indic", "en-US", locale.DigitsArabic, locale.DigitsArabic, indic},
		{"Hebrew follows the locale", "he", "", locale.DigitsLatin, latin},
		{"Russian uses its separators", "ru", "", locale.DigitsLatin, spaced},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := locale.NewFormatter(tc.locale, tc.digits)
			if err != nil {
				t.Fatalf("NewFormatter(%q, %q) = %v", tc.locale, tc.digits, err)
			}
			if f.Digits() != tc.wantDigits {
				t.Fatalf("Digits() = %q, want %q", f.Digits(), tc.wantDigits)
			}
			got, err := f.Decimal(1234567.891, 3)
			if err != nil || got != tc.want {
				t.Fatalf("Decimal() = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestFormatterInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		locale string
		digits locale.Digits
		n      int64
		want   string
	}{
		{"Arabic digits", "ar", "", 1204, "١٬٢٠٤"},
		{"Arabic override off", "ar", locale.DigitsLatin, 1204, "1,204"},
		{"zero in Arabic", "ar-SA", "", 0, "٠"},
		{"English override on", "en", locale.DigitsArabic, 7, "٧"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := locale.NewFormatter(tc.locale, tc.digits)
			if err != nil {
				t.Fatal(err)
			}
			// x/text writes a minus sign as U+2212 for some locales, so
			// compare after the same replacement on both sides.
			if got := f.Int(tc.n); got != tc.want && got != "−"+tc.want[1:] {
				t.Fatalf("Int(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}

func TestFormatterRefusals(t *testing.T) {
	t.Parallel()
	// Each row sends hostile input straight to the formatter, as if the
	// checks before it had failed.
	for _, tag := range []string{"", "ar-u-nu-latn", "en-x-foo", "ar\r\n", "ar_EG"} {
		if _, err := locale.NewFormatter(tag, ""); !errors.Is(err, locale.ErrInvalidFormattingLocale) {
			t.Errorf("NewFormatter(%q) = %v, want ErrInvalidFormattingLocale", tag, err)
		}
	}
	for _, d := range []locale.Digits{"deva", "ARAB", "latn-u-x", " "} {
		if _, err := locale.NewFormatter("ar", d); !errors.Is(err, locale.ErrInvalidDigits) {
			t.Errorf("NewFormatter(ar, %q) = %v, want ErrInvalidDigits", d, err)
		}
	}
	f, err := locale.NewFormatter("en", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := f.Decimal(v, 2); !errors.Is(err, locale.ErrNotFinite) {
			t.Errorf("Decimal(%v) = %v, want ErrNotFinite", v, err)
		}
	}
	for _, n := range []int{-1, locale.MaxFractionDigits + 1} {
		if _, err := f.Decimal(1, n); !errors.Is(err, locale.ErrFractionDigits) {
			t.Errorf("Decimal(1, %d) = %v, want ErrFractionDigits", n, err)
		}
	}
}

func TestPluralCategory(t *testing.T) {
	t.Parallel()
	const (
		z, o, tw, f, m, ot = locale.Zero, locale.One, locale.Two, locale.Few, locale.Many, locale.Other
	)
	tests := []struct {
		lang string
		n    int64
		want locale.Category
	}{
		// Russian and Ukrainian: one, few, many.
		{"ru", 0, m},
		{"ru", 1, o},
		{"ru", 2, f},
		{"ru", 4, f},
		{"ru", 5, m},
		{"ru", 11, m},
		{"ru", 12, m},
		{"ru", 14, m},
		{"ru", 21, o},
		{"ru", 22, f},
		{"ru", 25, m},
		{"ru", 100, m},
		{"ru", 101, o},
		{"ru", 111, m},
		{"ru", 112, m},
		{"uk", 0, m},
		{"uk", 1, o},
		{"uk", 3, f},
		{"uk", 5, m},
		{"uk", 11, m},
		{"uk", 21, o},
		{"uk", 24, f},
		{"uk", 111, m},
		{"uk", 1001, o},
		// Arabic: all six categories.
		{"ar", 0, z},
		{"ar", 1, o},
		{"ar", 2, tw},
		{"ar", 3, f},
		{"ar", 10, f},
		{"ar", 11, m},
		{"ar", 99, m},
		{"ar", 100, ot},
		{"ar", 101, ot},
		{"ar", 102, ot},
		{"ar", 103, f},
		{"ar", 111, m},
		{"ar", 200, ot},
		// A regional tag uses the rules of its language.
		{"ar-EG", 2, tw},
		{"ru-UA", 21, o},
		// Afrikaans and Hebrew.
		{"af", 1, o},
		{"af", 2, ot},
		{"af", 0, ot},
		{"he", 1, o},
		{"he", 2, tw},
		// A negative count has the category of its absolute value.
		{"ru", -1, o},
		{"ar", -2, tw},
		// The largest counts keep their category by their last digits.
		{"ru", math.MaxInt64, m}, // ...807
		{"ru", math.MinInt64, m}, // ...808
		{"ar", math.MaxInt64, f}, // ...807
	}
	for _, tc := range tests {
		got, err := locale.PluralCategory(tc.lang, tc.n)
		if err != nil || got != tc.want {
			t.Errorf("PluralCategory(%q, %d) = %q, %v, want %q", tc.lang, tc.n, got, err, tc.want)
		}
	}
	for _, tag := range []string{"", "r", "ru\r\n", "ru_RU", "ру"} {
		if got, err := locale.PluralCategory(tag, 1); !errors.Is(err, locale.ErrInvalidLanguage) || got != "" {
			t.Errorf("PluralCategory(%q) = %q, %v, want ErrInvalidLanguage", tag, got, err)
		}
	}
}
