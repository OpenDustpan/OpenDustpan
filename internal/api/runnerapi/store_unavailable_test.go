package runnerapi_test

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

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestStoreLostAnswers proves each way that a store reports a database that
// does not answer gives 503 with Retry-After 5 on the routes of a runner, in
// the error shape of the runner protocol, and on the management routes, in the
// problem shape. A runner claims and sends heartbeats there, so a 500 would
// stop it.
func TestStoreLostAnswers(t *testing.T) {
	t.Parallel()

	causes := []struct {
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

	for _, scope := range []runnerapi.Scope{runnerapi.ScopeRunnerKey, runnerapi.ScopeJoinToken} {
		for _, tc := range causes {
			t.Run(fmt.Sprintf("protocol scope %d %s", scope, tc.name), func(t *testing.T) {
				t.Parallel()

				rec := httptest.NewRecorder()
				runnerapi.WriteError(t.Context(), rec, slog.New(slog.DiscardHandler), scope, tc.err)

				if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
					t.Fatalf("status = %d, Retry-After = %q, want 503 and 5", rec.Code, rec.Header().Get("Retry-After"))
				}

				var body protocol.Error
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode body %q: %v", rec.Body.String(), err)
				}

				if body.Code != protocol.ErrorUnavailable || body.Message != namespace.ErrUnavailable.Error() {
					t.Fatalf("body = %+v, want code %q and the sentence of the sentinel", body, protocol.ErrorUnavailable)
				}
			})
		}
	}

	for _, tc := range causes {
		t.Run("management "+tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			runnerapi.WriteManagementError(t.Context(), rec, tc.err)

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
