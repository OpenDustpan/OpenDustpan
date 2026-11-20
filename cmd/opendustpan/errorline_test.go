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

// checkUsageLines fails when out does not hold the usage groups on lines of
// their own: the usage text is fixed program text and keeps its line breaks.
func checkUsageLines(t *testing.T, out string) {
	t.Helper()

	for _, want := range []string{"\nTry out:\n", "\n  opendustpan dev\n", "\nRun in production:\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q on its own line:\n%s", want, out)
		}
	}
}

// TestWriteErrorLineKeepsUsageLines is the stderr layer for the usage error:
// the message, which can hold an unknown command name, is escaped on one
// line, and the fixed usage text after it keeps its line breaks.
func TestWriteErrorLineKeepsUsageLines(t *testing.T) {
	t.Parallel()

	hostile := "a\r\nlevel=ERROR msg=forged\u0085\u202eb"

	for name, err := range map[string]error{
		"command error": &commandError{text: "unknown command " + hostile},
		"from run":      run(t.Context(), []string{hostile}, &strings.Builder{}, &strings.Builder{}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var b strings.Builder

			writeErrorLine(&b, "opendustpan", err)

			out := b.String()

			first, rest, found := strings.Cut(out, "\n")
			if !found {
				t.Fatalf("output is one line, want the usage after the message: %q", out)
			}

			if r, hidden := ident.FirstHidden(first); hidden {
				t.Errorf("first line holds hidden rune %U: %q", r, first)
			}

			if !strings.HasPrefix(rest, "usage: opendustpan <command>") {
				t.Errorf("second line = %q, want the usage line", rest)
			}

			if strings.Contains(out, "\nlevel=ERROR") {
				t.Errorf("the forged line is its own line:\n%s", out)
			}

			checkUsageLines(t, out)
		})
	}
}

func TestWriteErrorLineKeepsBareUsageLines(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	writeErrorLine(&b, "opendustpan", run(t.Context(), nil, &strings.Builder{}, &strings.Builder{}))

	if out := b.String(); !strings.HasPrefix(out, "opendustpan: usage: opendustpan <command>") {
		t.Errorf("output = %q, want the usage", out)
	}

	checkUsageLines(t, b.String())
}

// TestWriteErrorLineEscapesOtherUsageText checks that only the usage error
// itself keeps its line breaks: a wrapped usage error and an error with
// other text are escaped in full on one line.
func TestWriteErrorLineEscapesOtherUsageText(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"wrapped command error": fmt.Errorf("run a\nb: %w", &commandError{text: "unknown command x"}),
		"wrapped usage":         fmt.Errorf("x\ny: %w", errUsage),
		"other usage text":      lineError("usage: fake\nlevel=ERROR msg=forged"),
	} {
		var b strings.Builder

		writeErrorLine(&b, "opendustpan", err)

		out := strings.TrimSuffix(b.String(), "\n")
		if strings.Contains(out, "\n") {
			t.Errorf("%s: output is more than one line:\n%s", name, out)
		}

		if r, hidden := ident.FirstHidden(out); hidden {
			t.Errorf("%s: output holds hidden rune %U", name, r)
		}
	}
}
