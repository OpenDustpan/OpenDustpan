package locale_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/locale"
)

func TestValidateLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tag  string
		ok   bool
	}{
		{"Afrikaans", "af", true},
		{"Arabic", "ar", true},
		{"Hebrew", "he", true},
		{"Ukrainian", "uk", true},
		{"Russian", "ru", true},
		{"English", "en", true},
		{"regional Arabic", "ar-EG", true},
		{"old Hebrew tag", "iw", true},
		{"pseudo locale", "ar-XB", true},
		{"script and region", "zh-Hans-CN", true},
		{"unknown but well formed", "zz", true},
		{"37 characters", "de-DE-u-co-phonebk-x-aaaaaaa-bbbbbbbb", false},
		{"empty", "", false},
		{"one character", "a", false},
		{"36 characters", strings.Repeat("a", 36), false},
		{"underscore", "ar_EG", false},
		{"space", "ar EG", false},
		{"leading space", " ar", false},
		{"CR LF", "ar\r\nSet-Cookie: x", false},
		{"NUL", "ar\x00", false},
		{"non ASCII", "عربي", false},
		{"slash", "../ar", false},
		{"SQL", "ar'; DROP TABLE x;--", false},
		{"empty subtag", "ar--EG", false},
		{"trailing hyphen", "ar-", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := locale.ValidateLanguage(tc.tag)
			if tc.ok && err != nil {
				t.Fatalf("ValidateLanguage(%q) = %v, want nil", tc.tag, err)
			}
			if !tc.ok && !errors.Is(err, locale.ErrInvalidLanguage) {
				t.Fatalf("ValidateLanguage(%q) = %v, want ErrInvalidLanguage", tc.tag, err)
			}
		})
	}
}

func TestValidateFormattingLocale(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tag  string
		ok   bool
	}{
		{"plain", "ar-EG", true},
		{"language only", "ru", true},
		{"script", "zh-Hans", true},
		{"u extension", "ar-u-nu-latn", false},
		{"u extension upper case", "AR-U-NU-LATN", false},
		{"hour cycle extension", "en-US-u-hc-h23", false},
		{"private use", "en-x-private", false},
		{"private use only", "x-private", false},
		{"other extension", "en-a-bbb", true},
		{"bad shape", "en US", false},
		{"too long", strings.Repeat("a", 36), false},
		{"empty", "", false},
		{"CR LF", "en\r\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := locale.ValidateFormattingLocale(tc.tag)
			if tc.ok && err != nil {
				t.Fatalf("ValidateFormattingLocale(%q) = %v, want nil", tc.tag, err)
			}
			if !tc.ok && !errors.Is(err, locale.ErrInvalidFormattingLocale) {
				t.Fatalf("ValidateFormattingLocale(%q) = %v, want ErrInvalidFormattingLocale", tc.tag, err)
			}
		})
	}
}

func TestParseDigits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want locale.Digits
	}{
		{"latn", locale.DigitsLatin},
		{"arab", locale.DigitsArabic},
	} {
		got, err := locale.ParseDigits(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseDigits(%q) = %q, %v, want %q, nil", tc.in, got, err, tc.want)
		}
	}
}

func TestParseDigitsRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"upper case", "ARAB"},
		{"leading space", " arab"},
		{"newline", "arab\n"},
		{"other numbering system", "deva"},
		{"the word null", "null"},
		{"long hostile value", strings.Repeat("x", 4096)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := locale.ParseDigits(tc.in)
			if !errors.Is(err, locale.ErrInvalidDigits) || got != "" {
				t.Fatalf("ParseDigits(%q) = %q, %v, want ErrInvalidDigits", tc.in, got, err)
			}
			// The text names the valid values and stays short.
			if !strings.Contains(err.Error(), "latn, arab or null") {
				t.Fatalf("error %q does not name the valid values", err)
			}
			if len(err.Error()) > 200 {
				t.Fatalf("error text is %d bytes, want at most 200", len(err.Error()))
			}
		})
	}
}
