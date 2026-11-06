package tofuhttp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorStoreLost proves each way that a store reports a database that
// does not answer gives 503 with Retry-After 5, never a 500.
func TestWriteErrorStoreLost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", namespace.ErrUnavailable},
		{"wrapped sentinel", fmt.Errorf("write state: transaction: %w", namespace.ErrUnavailable)},
		{"connection failed", fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, syscall.ECONNREFUSED)},
		{"connection lost", errors.Join(namespace.ErrUnavailable, io.ErrUnexpectedEOF)},
		{"server shutdown", fmt.Errorf("write: %w: server is shutting down", namespace.ErrUnavailable)},
		{"read only", fmt.Errorf("write: %w: database is read-only", namespace.ErrUnavailable)},
		{"busy", fmt.Errorf("write: %w: database is busy", namespace.ErrUnavailable)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h Handler

			h.logger = slog.New(slog.DiscardHandler)

			rec := httptest.NewRecorder()
			h.writeError(t.Context(), rec, tc.err)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}

			if got := rec.Header().Get("Retry-After"); got != "5" {
				t.Fatalf("Retry-After = %q, want 5", got)
			}
		})
	}
}
