package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorStoreLost proves each way that a store reports a database that
// does not answer gives the problem shape 503 with Retry-After 5 and the
// catalog code, never a 500.
func TestWriteErrorStoreLost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", namespace.ErrUnavailable},
		{"wrapped sentinel", fmt.Errorf("list units: %w", namespace.ErrUnavailable)},
		{"connection failed", fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, syscall.ECONNREFUSED)},
		{"connection lost", errors.Join(namespace.ErrUnavailable, io.ErrUnexpectedEOF)},
		{"server shutdown", fmt.Errorf("%w: server is shutting down", namespace.ErrUnavailable)},
		{"read only", fmt.Errorf("%w: database is read-only", namespace.ErrUnavailable)},
		{"busy", fmt.Errorf("%w: database is busy", namespace.ErrUnavailable)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := &Handler{logger: slog.New(slog.DiscardHandler)}

			rec := httptest.NewRecorder()
			h.writeError(t.Context(), rec, tc.err)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}

			if got := rec.Header().Get("Retry-After"); got != "5" {
				t.Fatalf("Retry-After = %q, want 5", got)
			}

			var body struct {
				Code      int    `json:"code"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}

			if body.Code != http.StatusServiceUnavailable || body.ErrorCode != errcode.NamespaceUnavailable {
				t.Fatalf("body = %+v, want code 503 and %s", body, errcode.NamespaceUnavailable)
			}
		})
	}
}
