package main

import (
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

// lineError is an error whose text is the test value.
type lineError string

func (e lineError) Error() string { return string(e) }

// TestWriteErrorLineEscapesHiddenRunes is the stderr layer: an error whose
// text holds a hidden rune gives one line with a visible escape, as if no
// call site had quoted the value.
func TestWriteErrorLineEscapesHiddenRunes(t *testing.T) {
	t.Parallel()

	for name, v := range map[string]string{
		"CR LF":     "a\r\nb",
		"LF":        "a\nb",
		"NUL":       "a\x00b",
		"ESC":       "a\x1b[31mb",
		"DEL":       "a\x7fb",
		"NEL":       "a\u0085b",
		"U+2028":    "a\u2028b",
		"U+2029":    "a\u2029b",
		"bad UTF-8": "a\xffb",
		"forged":    "ok\nlevel=ERROR msg=forged",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder

			writeErrorLine(&b, "opendustpan", fmt.Errorf("open %s: %w", v, lineError(v)))

			out := b.String()
			if !strings.HasPrefix(out, "opendustpan: open ") || !strings.HasSuffix(out, "\n") {
				t.Fatalf("output = %q, want one line that starts with the prefix", out)
			}

			line := strings.TrimSuffix(out, "\n")
			if strings.ContainsAny(line, "\r\n") {
				t.Errorf("output holds more than one line: %q", out)
			}

			if r, found := ident.FirstHidden(line); found {
				t.Errorf("output holds hidden rune %U: %q", r, out)
			}

			if strings.Contains(out, "\nlevel=ERROR") {
				t.Errorf("the forged line is its own line: %q", out)
			}
		})
	}
}

func TestWriteErrorLineKeepsPlainText(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	writeErrorLine(&b, "opendustpan", lineError(`open "/etc/ü": no such file`))

	if got, want := b.String(), "opendustpan: open \"/etc/ü\": no such file\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	b.Reset()
	writeErrorLine(&b, "opendustpan", lineError("a\nb"))

	if got, want := b.String(), `opendustpan: a\nb`+"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	// A backslash and an "n" is two backslashes and an "n", so a reader can
	// tell it from an escaped line feed.
	b.Reset()
	writeErrorLine(&b, "opendustpan", lineError(`a\nb`))

	if got, want := b.String(), `opendustpan: a\\nb`+"\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestWarnCloseDatabaseEscapesHiddenRunes is the guard on the warning of a
// failed close of the database. The text handler alone quotes the value,
// so the unquoted form shows that the guard ran.
func TestWarnCloseDatabaseEscapesHiddenRunes(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	warnCloseDatabase(&b, lineError("a\r\nX"))

	out := b.String()
	if r, found := ident.FirstHidden(strings.TrimSuffix(out, "\n")); found {
		t.Errorf("output holds hidden rune %U: %q", r, out)
	}

	if want := `error=a\r\nX`; !strings.Contains(out, want) {
		t.Errorf("output = %q, want it to hold %q", out, want)
	}
}
