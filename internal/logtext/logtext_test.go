package logtext_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/logtext"
)

func TestCleanRemovesLineBreaks(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"a\r\nb":        "ab",
		"x\ny\rz":       "xyz",
		"\r\n\r\n":      "",
		"plain":         "plain",
		"INFO forged\n": "INFO forged",
	} {
		if got := logtext.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanBoundsLength(t *testing.T) {
	t.Parallel()

	exact := strings.Repeat("a", logtext.MaxBytes)
	if got := logtext.Clean(exact); got != exact {
		t.Errorf("value of %d bytes changed to %d bytes", len(exact), len(got))
	}

	got := logtext.Clean(exact + "b")
	if want := exact + "..."; got != want {
		t.Errorf("value of %d bytes: got %d bytes, want %d bytes with the marker", len(exact)+1, len(got), len(want))
	}

	// The break comes after the cut, so the strip does not hide a long value.
	if got := logtext.Clean(strings.Repeat("a\n", 5000)); len(got) > logtext.MaxBytes+3 {
		t.Errorf("log text of %d bytes, want at most %d", len(got), logtext.MaxBytes+3)
	}
}

func TestCleanCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()

	// Each rune has 3 bytes, so byte 200 is inside a rune.
	got := logtext.Clean(strings.Repeat("€", 100))
	if !utf8.ValidString(got) {
		t.Errorf("log text %q is not valid UTF-8", got)
	}

	if want := strings.Repeat("€", 66) + "..."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCleanRemovesEveryHiddenRune(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]string{
		"NUL":                    "a\x00b",
		"BEL":                    "a\ab",
		"backspace":              "a\bb",
		"tab":                    "a\tb",
		"vertical tab":           "a\vb",
		"form feed":              "a\fb",
		"ESC":                    "a\x1b[31mb",
		"DEL":                    "a\x7fb",
		"C1 control":             "a\u0080b",
		"C1 control CSI":         "a\u009bb",
		"NEL":                    "a\u0085b",
		"line separator":         "a\u2028b",
		"paragraph separator":    "a\u2029b",
		"right-to-left override": "a\u202eb",
		"left-to-right isolate":  "a\u2066b",
		"zero width space":       "a\u200bb",
		"byte order mark":        "a\ufeffb",
	} {
		got := logtext.Clean(in)

		for _, r := range got {
			if ident.IsHiddenRune(r) {
				t.Errorf("%s: Clean(%q) = %q, holds the rune %U", name, in, got, r)
			}
		}

		if !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") {
			t.Errorf("%s: Clean(%q) = %q, lost the text around the rune", name, in, got)
		}
	}

	// Text with no hidden rune stays as it is, with multibyte runes too.
	if got, want := logtext.Clean("naïve €"), "naïve €"; got != want {
		t.Errorf("Clean changed a clean value: got %q, want %q", got, want)
	}
}
