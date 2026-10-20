package locale

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// TestFormattingLocaleLayers sends hostile input to each layer of
// ValidateFormattingLocale alone, as if the other layer had failed.
func TestFormattingLocaleLayers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tag  string
		deny bool
	}{
		{"ar-u-nu-latn", true},
		{"AR-U-NU-LATN", true},
		{"en-US-u-hc-h23", true},
		{"en-x-private", true},
		{"x-private", true},
		{"ar-EG", false},
		{"en-a-bbb", false},
	} {
		if got := rawHasExtension(tc.tag); got != tc.deny {
			t.Errorf("raw layer for %q = %v, want %v", tc.tag, got, tc.deny)
		}
		tag, err := language.Parse(tc.tag)
		if err != nil {
			t.Errorf("language.Parse(%q) = %v", tc.tag, err)
			continue
		}
		if got := parsedHasExtension(tag); got != tc.deny {
			t.Errorf("parsed layer for %q = %v, want %v", tc.tag, got, tc.deny)
		}
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	if got := truncate("short"); got != "short" {
		t.Errorf("truncate(short) = %q", got)
	}
	// Each prefix moves the cut inside a 3-byte rune.
	for _, prefix := range []string{"", "a", "ab"} {
		in := prefix + strings.Repeat("\u20ac", 20)
		got := truncate(in)
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q) = %q is not valid UTF-8", in, got)
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("truncate(%q) = %q has no ellipsis", in, got)
		}
	}
}
