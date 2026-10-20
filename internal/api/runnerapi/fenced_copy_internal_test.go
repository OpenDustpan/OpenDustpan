package runnerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestWriteErrorFencedCopy proves that a fenced write answers the sentence of
// the sentinel and nothing else on the runner protocol and on the management
// API, also when code wraps the sentinel: the context of the wrap stays in the
// log.
func TestWriteErrorFencedCopy(t *testing.T) {
	t.Parallel()

	protocolBody, err := json.Marshal(protocol.Error{
		Code: protocol.ErrorUnavailable, Message: namespace.ErrFenced.Error(), Supported: "",
	})
	if err != nil {
		t.Fatalf("marshal protocol error: %v", err)
	}

	managementBody, err := json.Marshal(
		managementError{Message: namespace.ErrFenced.Error(), Code: http.StatusConflict},
	)
	if err != nil {
		t.Fatalf("marshal management error: %v", err)
	}

	scopes := []struct {
		name   string
		scope  Scope
		status int
		body   string
	}{
		{
			name:   "runner protocol",
			scope:  ScopeRunnerKey,
			status: http.StatusServiceUnavailable,
			body:   string(protocolBody) + "\n",
		},
		{name: "management", scope: ScopeAuthorized, status: http.StatusConflict, body: string(managementBody) + "\n"},
	}

	errs := []struct {
		name string
		err  error
	}{
		{name: "sentinel", err: namespace.ErrFenced},
		{name: "wrapped", err: fmt.Errorf("record resource changes: %w", namespace.ErrFenced)},
		{name: "wrapped twice", err: fmt.Errorf("update: %w", fmt.Errorf("append: %w", namespace.ErrFenced))},
	}

	for _, sc := range scopes {
		for _, tc := range errs {
			t.Run(sc.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				var h Handler

				h.logger = slog.New(slog.DiscardHandler)

				rec := httptest.NewRecorder()
				h.writeError(context.Background(), rec, sc.scope, tc.err)

				if rec.Code != sc.status {
					t.Fatalf("status = %d, want %d", rec.Code, sc.status)
				}

				if got := rec.Body.String(); got != sc.body {
					t.Fatalf("body = %q, want %q", got, sc.body)
				}
			})
		}
	}
}
