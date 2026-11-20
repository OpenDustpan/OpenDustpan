package main

import (
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

// valueError is an error whose text is the test value.
type valueError string

func (e valueError) Error() string { return string(e) }

// TestLoggerEscapesHiddenRunes is the guard on the logger of the launcher:
// an error text with a line break gives one line with the escape of the
// guard. The text handler alone quotes the value ("\r\nX" in quotes), so
// the unquoted form shows that the guard ran.
func TestLoggerEscapesHiddenRunes(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	newLogger(&b).Error("protect the launcher process", slog.Any("error", valueError("a\r\nX")))

	out := b.String()
	line := strings.TrimSuffix(out, "\n")

	if r, found := ident.FirstHidden(line); found {
		t.Errorf("output holds hidden rune %U: %q", r, out)
	}

	if want := `error=a\r\nX`; !strings.Contains(out, want) {
		t.Errorf("output = %q, want it to hold %q", out, want)
	}
}
