package runnerpools_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerpools"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// storeLostCauses are the ways that a store reports a database that does not
// answer.
func storeLostCauses() []struct {
	name string
	err  error
} {
	return []struct {
		name string
		err  error
	}{
		{"sentinel", namespace.ErrUnavailable},
		{"wrapped sentinel", fmt.Errorf("claim phase: %w", namespace.ErrUnavailable)},
		{"connection failed", fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, syscall.ECONNREFUSED)},
		{"connection lost", errors.Join(namespace.ErrUnavailable, io.ErrUnexpectedEOF)},
		{"server shutdown", fmt.Errorf("%w: server is shutting down", namespace.ErrUnavailable)},
		{"read only", fmt.Errorf("%w: database is read-only", namespace.ErrUnavailable)},
		{"busy", fmt.Errorf("%w: database is busy", namespace.ErrUnavailable)},
		{"with a runner error", errors.Join(runner.ErrNotFound, namespace.ErrUnavailable)},
	}
}

// TestStoreLostManagement proves the same for the management routes, in the
// problem shape.
func TestStoreLostManagement(t *testing.T) {
	t.Parallel()

	for _, tc := range storeLostCauses() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			runnerpools.WriteManagementError(t.Context(), rec, tc.err)

			var body struct {
				Code      int    `json:"code"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}

			if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" ||
				body.Code != http.StatusServiceUnavailable || body.ErrorCode != errcode.NamespaceUnavailable {
				t.Fatalf("status = %d, Retry-After = %q, body = %+v, want 503, 5 and %s",
					rec.Code, rec.Header().Get("Retry-After"), body, errcode.NamespaceUnavailable)
			}
		})
	}
}
