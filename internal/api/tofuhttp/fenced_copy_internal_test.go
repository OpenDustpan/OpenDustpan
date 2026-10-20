package tofuhttp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// writeFunc is the shape of writeError and writeLockError.
type writeFunc func(h *Handler, ctx context.Context, w http.ResponseWriter, err error)

// fencedAnswer is a write path with its answer to a fenced write.
type fencedAnswer struct {
	name  string
	write writeFunc
	// status and retryAfter are the answer to a fenced write.
	status     int
	retryAfter bool
}

// TestWriteErrorFencedCopy proves that a fenced write answers the sentence of
// the sentinel and nothing else, also when code wraps the sentinel: the
// context of the wrap stays in the log. A fenced LOCK answers 503 with a
// Retry-After time, every other fenced write 409. An error that is not a fence
// keeps its own answer and writes no fenced line.
func TestWriteErrorFencedCopy(t *testing.T) {
	t.Parallel()

	answers := []fencedAnswer{
		{
			name:  "write error",
			write: (*Handler).writeError, status: http.StatusConflict, retryAfter: false,
		},
		{
			name:  "lock error",
			write: (*Handler).writeLockError, status: http.StatusServiceUnavailable, retryAfter: true,
		},
	}

	cases := []struct {
		name string
		err  error
		// wrap is the context of the wrap: the log holds it, the body never.
		wrap string
	}{
		{name: "sentinel", err: namespace.ErrFenced, wrap: ""},
		{
			name: "wrapped",
			err:  fmt.Errorf("record resource changes: %w", namespace.ErrFenced),
			wrap: "record resource changes",
		},
		{
			name: "wrapped twice",
			err:  fmt.Errorf("update: %w", fmt.Errorf("append: %w", namespace.ErrFenced)),
			wrap: "append",
		},
	}

	for _, an := range answers {
		for _, tc := range cases {
			t.Run(an.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkFencedAnswer(t, an, tc.err, tc.wrap)
			})
		}
	}
}

// TestWriteErrorNotFencedKeepsAnswer proves that an error that is not a fence
// keeps its own answer on both paths and writes no fenced line.
func TestWriteErrorNotFencedKeepsAnswer(t *testing.T) {
	t.Parallel()

	writers := map[string]writeFunc{
		"write error": (*Handler).writeError,
		"lock error":  (*Handler).writeLockError,
	}

	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				h   Handler
				buf bytes.Buffer
			)

			h.logger = slog.New(slog.NewTextHandler(&buf, nil))

			rec := httptest.NewRecorder()
			write(&h, t.Context(), rec, workspace.ErrLocked)

			if rec.Code != http.StatusLocked {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusLocked)
			}

			if want := "workspace is locked\n"; rec.Body.String() != want {
				t.Fatalf("body = %q, want %q", rec.Body.String(), want)
			}

			if strings.Contains(buf.String(), "write fenced") {
				t.Fatalf("log = %q, must not hold the fenced line", buf.String())
			}
		})
	}
}

// checkFencedAnswer proves the answer of write to a fenced err: the sentence
// of the sentinel in the body, and the wrap context in the log only.
func checkFencedAnswer(t *testing.T, an fencedAnswer, err error, wrap string) {
	t.Helper()

	var (
		h   Handler
		buf bytes.Buffer
	)

	h.logger = slog.New(slog.NewTextHandler(&buf, nil))

	rec := httptest.NewRecorder()
	an.write(&h, t.Context(), rec, err)

	if rec.Code != an.status {
		t.Fatalf("status = %d, want %d", rec.Code, an.status)
	}

	if got := rec.Header().Get("Retry-After") != ""; got != an.retryAfter {
		t.Fatalf("Retry-After set = %t, want %t", got, an.retryAfter)
	}

	if want := namespace.ErrFenced.Error() + "\n"; rec.Body.String() != want {
		t.Fatalf("body = %q, want %q", rec.Body.String(), want)
	}

	logged := buf.String()
	if !strings.Contains(logged, "write fenced") {
		t.Fatalf("log = %q, want the fenced line", logged)
	}

	if wrap != "" && !strings.Contains(logged, wrap) {
		t.Fatalf("log = %q, want the wrap context %q", logged, wrap)
	}
}
