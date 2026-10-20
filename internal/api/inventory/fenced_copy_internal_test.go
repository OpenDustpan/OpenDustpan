package inventory

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorFencedCopy proves that a fenced write answers the sentence of
// the sentinel and nothing else, also when code wraps the sentinel: the
// context of the wrap stays in the log. An error that is not a fence keeps its
// own answer and writes no fenced line.
func TestWriteErrorFencedCopy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		status int
		body   string
		// wrap is the context of the wrap: the log holds it, the body never.
		wrap string
		// fenced is true when the log holds the "write fenced" line.
		fenced bool
	}{
		{
			name:   "sentinel",
			err:    namespace.ErrFenced,
			status: http.StatusConflict,
			body:   fmt.Sprintf("{\"message\":%q,\"code\":%d}\n", namespace.ErrFenced.Error(), http.StatusConflict),
			wrap:   "",
			fenced: true,
		},
		{
			name:   "wrapped",
			err:    fmt.Errorf("record resource changes: %w", namespace.ErrFenced),
			status: http.StatusConflict,
			body:   fmt.Sprintf("{\"message\":%q,\"code\":%d}\n", namespace.ErrFenced.Error(), http.StatusConflict),
			wrap:   "record resource changes",
			fenced: true,
		},
		{
			name:   "wrapped twice",
			err:    fmt.Errorf("update: %w", fmt.Errorf("append: %w", namespace.ErrFenced)),
			status: http.StatusConflict,
			body:   fmt.Sprintf("{\"message\":%q,\"code\":%d}\n", namespace.ErrFenced.Error(), http.StatusConflict),
			wrap:   "append",
			fenced: true,
		},
		{
			name:   "not a fence",
			err:    collection.ErrAlreadyExists,
			status: http.StatusConflict,
			body: fmt.Sprintf(
				"{\"message\":%q,\"code\":%d}\n",
				collection.ErrAlreadyExists.Error(),
				http.StatusConflict,
			),
			wrap:   "",
			fenced: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				h   Handler
				buf bytes.Buffer
			)

			h.logger = slog.New(slog.NewTextHandler(&buf, nil))

			rec := httptest.NewRecorder()
			h.writeError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}

			if got := rec.Body.String(); got != tc.body {
				t.Fatalf("body = %q, want %q", got, tc.body)
			}

			if got := strings.Contains(buf.String(), "write fenced"); got != tc.fenced {
				t.Fatalf("log has the fenced line = %t, want %t: %q", got, tc.fenced, buf.String())
			}

			checkWrapInLog(t, buf.String(), rec.Body.String(), tc.wrap)
		})
	}
}

// checkWrapInLog proves the log holds the context of the wrap and the body
// never does. An empty wrap has nothing to check.
func checkWrapInLog(t *testing.T, logged, body, wrap string) {
	t.Helper()

	if wrap == "" {
		return
	}

	if !strings.Contains(logged, wrap) {
		t.Fatalf("log = %q, want the wrap context %q", logged, wrap)
	}

	if strings.Contains(body, wrap) {
		t.Fatalf("body = %q, must not hold the wrap context %q", body, wrap)
	}
}
