package tfe

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorStoreLost proves each way that a store reports a database that
// does not answer gives a JSON:API 503 with Retry-After 5 and the catalog code,
// never a 500.
func TestWriteErrorStoreLost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", namespace.ErrUnavailable},
		{"wrapped sentinel", fmt.Errorf("create state version: transaction: %w", namespace.ErrUnavailable)},
		{"connection failed", fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, syscall.ECONNREFUSED)},
		{"connection lost", errors.Join(namespace.ErrUnavailable, io.ErrUnexpectedEOF)},
		{"server shutdown", fmt.Errorf("write: %w", fmt.Errorf("%w: server is shutting down", namespace.ErrUnavailable))},
		{"read only", fmt.Errorf("write: %w: database is read-only", namespace.ErrUnavailable)},
		{"busy", fmt.Errorf("write: %w: database is busy", namespace.ErrUnavailable)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := writeErrorOf(tc.err)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}

			if got := rec.Header().Get("Retry-After"); got != "5" {
				t.Fatalf("Retry-After = %q, want 5", got)
			}

			checkErrorDocument(t, rec, http.StatusServiceUnavailable, namespace.ErrUnavailable.Error())

			if body := rec.Body.String(); !strings.Contains(body, errcode.NamespaceUnavailable) {
				t.Fatalf("body = %q, want the code %s", body, errcode.NamespaceUnavailable)
			}
		})
	}
}
