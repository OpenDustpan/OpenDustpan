package server

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
)

// errPlainCause is a cause with no type of its own in the chain.
var errPlainCause = errors.New("plain cause")

// redactedError stands in for a redacted error: it hides its text and wraps
// the error that it redacts.
type redactedError struct {
	err error
}

// Error implements error with a text that holds no cause.
func (redactedError) Error() string { return "redacted" }

// Unwrap returns the error that is redacted.
func (r redactedError) Unwrap() error { return r.err }

// Redacted marks the fixture as a redaction.
func (redactedError) Redacted() {}

// TestCauseTypeSkipsARedaction proves that causeType names the cause under a
// redaction, so the log of a 500 keeps the type of the cause, and that a plain
// wrap gives the same type.
func TestCauseTypeSkipsARedaction(t *testing.T) {
	t.Parallel()

	cause := &fs.PathError{Op: "open", Path: "unit", Err: fs.ErrNotExist}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"a redaction of the cause", redactedError{err: cause}, "*fs.PathError"},
		{"a plain wrap of the cause", fmt.Errorf("read: %w", cause), "*fs.PathError"},
		{"a redaction of a plain wrap", redactedError{err: fmt.Errorf("read: %w", cause)}, "*fs.PathError"},
		{"a redaction with no cause", redactedError{err: errPlainCause}, "server.redactedError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := causeType(tt.err); got != tt.want {
				t.Fatalf("causeType() = %q, want %q", got, tt.want)
			}
		})
	}
}
