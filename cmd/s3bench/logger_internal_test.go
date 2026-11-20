package main

import (
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
)

// TestLoggerEscapesHiddenRunes is the guard on the logger of the
// benchmark: a value with a line break gives one line with the escape of
// the guard. The text handler alone quotes the value, so the unquoted form
// shows that the guard ran.
func TestLoggerEscapesHiddenRunes(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	newLogger(&b).Info("write", slog.String("key", "a\r\nX"))

	out := b.String()
	line := strings.TrimSuffix(out, "\n")

	if r, found := ident.FirstHidden(line); found {
		t.Errorf("output holds hidden rune %U: %q", r, out)
	}

	if want := `key=a\r\nX`; !strings.Contains(out, want) {
		t.Errorf("output = %q, want it to hold %q", out, want)
	}
}
