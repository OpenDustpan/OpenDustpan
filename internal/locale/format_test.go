package locale_test

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/locale"
)

// TestDefaultDigits pins the 28 CLDR 47 Arabic locales (ar and the 27
// regional ones) and ar-001, which has no data of its own.
func TestDefaultDigits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale string
		want   locale.Digits
	}{
		{"ar", locale.DigitsLatin},
		{"ar-001", locale.DigitsLatin},
		{"ar-AE", locale.DigitsLatin},
		{"ar-BH", locale.DigitsArabic},
		{"ar-DJ", locale.DigitsArabic},
		{"ar-DZ", locale.DigitsLatin},
		{"ar-EG", locale.DigitsArabic},
		{"ar-EH", locale.DigitsLatin},
		{"ar-ER", locale.DigitsArabic},
		{"ar-IL", locale.DigitsArabic},
		{"ar-IQ", locale.DigitsArabic},
		{"ar-JO", locale.DigitsArabic},
		{"ar-KM", locale.DigitsArabic},
		{"ar-KW", locale.DigitsArabic},
		{"ar-LB", locale.DigitsArabic},
		{"ar-LY", locale.DigitsLatin},
		{"ar-MA", locale.DigitsLatin},
		{"ar-MR", locale.DigitsArabic},
		{"ar-OM", locale.DigitsArabic},
		{"ar-PS", locale.DigitsArabic},
		{"ar-QA", locale.DigitsArabic},
		{"ar-SA", locale.DigitsArabic},
		{"ar-SD", locale.DigitsArabic},
		{"ar-SO", locale.DigitsArabic},
		{"ar-SS", locale.DigitsArabic},
		{"ar-SY", locale.DigitsArabic},
		{"ar-TD", locale.DigitsArabic},
		{"ar-TN", locale.DigitsLatin},
		{"ar-YE", locale.DigitsArabic},
		{"en-US", locale.DigitsLatin},
		{"he", locale.DigitsLatin},
		{"ru", locale.DigitsLatin},
		{"uk-UA", locale.DigitsLatin},
		{"af", locale.DigitsLatin},
		// A locale with other default digits gets latn: the server has only
		// latn and arab.
		{"fa", locale.DigitsLatin},
		{"fa-IR", locale.DigitsLatin},
		// A region in a non-Arabic tag does not change the digits.
		{"en-EG", locale.DigitsLatin},
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
	for _, bad := range []string{"not a tag", "", "ar-u-nu-latn", "ar-EG-u-nu-latn", "en-x-private", "x-private"} {
		if got, err := locale.DefaultDigits(bad); !errors.Is(err, locale.ErrInvalidFormattingLocale) || got != "" {
			t.Fatalf("DefaultDigits(%q) = %q, %v, want ErrInvalidFormattingLocale", bad, got, err)
		}
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
		{"Arabic follows the locale", "ar", "", locale.DigitsLatin, latin},
		{"world Arabic follows the locale", "ar-001", "", locale.DigitsLatin, latin},
		{"regional Arabic follows the locale", "ar-EG", "", locale.DigitsArabic, indic},
		{"Arabic in a Western region", "ar-AE", "", locale.DigitsLatin, latin},
		{"Arabic override to Western", "ar-SA", locale.DigitsLatin, locale.DigitsLatin, latin},
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
		{"Arabic digits", "ar-SA", "", 1204, "١٬٢٠٤"},
		{"bare Arabic has Western digits", "ar", "", 1204, "1,204"},
		{"Arabic override on", "ar", locale.DigitsArabic, 1204, "١٬٢٠٤"},
		{"Arabic override off", "ar-SA", locale.DigitsLatin, 1204, "1,204"},
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
		{"he", 0, ot},
		{"he", 1, o},
		{"he", 2, tw},
		{"he", 3, ot},
		{"he", 10, ot},
		{"he", 20, ot},
		{"he", 100, ot},
		{"he", 1000, ot},
		{"iw", 2, tw},
		{"he-IL", 20, ot},
		{"he", -2, tw},
		{"he", math.MaxInt64, ot},
		{"he", 1_000_000_001, ot},
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

//go:embed testdata/cldr47-plural-integers.txt
var pluralFixture embed.FS

// parseSamples expands a CLDR @integer sample list: numbers and inclusive
// ranges (a~b) separated by commas.
func parseSamples(t *testing.T, list string) []int64 {
	t.Helper()
	var out []int64
	for part := range strings.SplitSeq(list, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "~")
		from, err := strconv.ParseInt(lo, 10, 64)
		if err != nil {
			t.Fatalf("sample %q: %v", part, err)
		}
		to := from
		if isRange {
			if to, err = strconv.ParseInt(hi, 10, 64); err != nil {
				t.Fatalf("sample %q: %v", part, err)
			}
		}
		for n := from; n <= to; n++ {
			out = append(out, n)
		}
	}
	return out
}

// readPluralFixture reads the fixture into rows of language, category and
// whole counts.
func readPluralFixture(t *testing.T) []pluralRow {
	t.Helper()
	data, err := pluralFixture.ReadFile("testdata/cldr47-plural-integers.txt")
	if err != nil {
		t.Fatal(err)
	}
	var rows []pluralRow
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.SplitN(line, " ", 3)
		if len(fields) != 3 {
			t.Fatalf("fixture line %q: want language, category, samples", line)
		}
		rows = append(rows, pluralRow{fields[0], locale.Category(fields[1]), parseSamples(t, fields[2])})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

type pluralRow struct {
	lang    string
	want    locale.Category
	samples []int64
}

// TestPluralCategoryCLDR47 checks each supported language against the
// integer samples of CLDR 47, so that a change in the data of x/text fails.
func TestPluralCategoryCLDR47(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, row := range readPluralFixture(t) {
		seen[row.lang] = true
		for _, n := range row.samples {
			t.Run(fmt.Sprintf("%s/%s/%d", row.lang, row.want, n), func(t *testing.T) {
				t.Parallel()
				got, err := locale.PluralCategory(row.lang, n)
				if err != nil || got != row.want {
					t.Fatalf("PluralCategory(%q, %d) = %q, %v, want %q", row.lang, n, got, err, row.want)
				}
			})
		}
	}
	for _, lang := range []string{"af", "ar", "he", "uk", "ru"} {
		if !seen[lang] {
			t.Errorf("the fixture has no samples for %q", lang)
		}
	}
}
