package runnerapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// fencedScope is one error scope of the handler with its answer to a fenced
// write and to an error that is not a fence.
type fencedScope struct {
	name       string
	scope      Scope
	status     int
	body       string
	retryAfter bool
	other      error
	// otherStatus and otherBody are the answer to an error that is not a fence.
	otherStatus int
	otherBody   string
}

// TestWriteErrorFencedCopy proves that a fenced write answers the sentence of
// the sentinel and nothing else on the runner protocol and on the management
// API, also when code wraps the sentinel: the context of the wrap stays in the
// log. The runner protocol answers 503 with a Retry-After time on purpose: a
// runner retries it. An error that is not a fence keeps its own answer.
func TestWriteErrorFencedCopy(t *testing.T) {
	t.Parallel()

	protocolBody, err := json.Marshal(protocol.Error{
		Code: protocol.ErrorUnavailable, Message: namespace.ErrFenced.Error(), Supported: "",
	})
	if err != nil {
		t.Fatalf("marshal protocol error: %v", err)
	}

	managementBody, err := json.Marshal(
		problem.Body{
			Message: namespace.ErrFenced.Error(), Code: http.StatusConflict, ErrorCode: errcode.NamespaceFenced,
			ErrorArgs: nil, Reauthenticate: "", Units: nil,
		},
	)
	if err != nil {
		t.Fatalf("marshal management error: %v", err)
	}

	otherProtocol := protocol.Error{
		Code: protocol.ErrorRateLimited, Message: runner.ErrLimit.Error(), Supported: "",
	}

	otherProtocolBody, err := json.Marshal(otherProtocol)
	if err != nil {
		t.Fatalf("marshal protocol error: %v", err)
	}

	otherManagementBody, err := json.Marshal(
		problem.Body{
			Message:   "the runner is retired",
			Code:      http.StatusConflict,
			ErrorCode: errcode.RunnerRetired,
			ErrorArgs: nil, Reauthenticate: "", Units: nil,
		},
	)
	if err != nil {
		t.Fatalf("marshal management error: %v", err)
	}

	scopes := []fencedScope{
		{
			name:        "runner protocol",
			scope:       ScopeRunnerKey,
			status:      http.StatusServiceUnavailable,
			body:        string(protocolBody) + "\n",
			retryAfter:  true,
			other:       runner.ErrLimit,
			otherStatus: http.StatusTooManyRequests,
			otherBody:   string(otherProtocolBody) + "\n",
		},
		{
			name: "management", scope: ScopeAuthorized,
			status: http.StatusConflict, body: string(managementBody) + "\n", retryAfter: false,
			other: runner.ErrRetired, otherStatus: http.StatusConflict, otherBody: string(otherManagementBody) + "\n",
		},
	}

	errs := []struct {
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

	for _, sc := range scopes {
		for _, tc := range errs {
			t.Run(sc.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkFencedAnswer(t, sc, tc.err, tc.wrap)
			})
		}

		t.Run(sc.name+"/not a fence", func(t *testing.T) {
			t.Parallel()

			checkOtherAnswer(t, sc)
		})
	}
}

// writeErrorOf answers err through a handler that holds only a logger, the one
// field writeError reads, and returns the answer and the log text.
func writeErrorOf(t *testing.T, scope Scope, err error) (*httptest.ResponseRecorder, string) {
	t.Helper()

	var (
		h   Handler
		buf bytes.Buffer
	)

	h.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := httptest.NewRecorder()
	h.writeError(t.Context(), rec, scope, err)

	return rec, buf.String()
}

// checkFencedAnswer proves the answer of the scope to err: the sentence of the
// sentinel in the body, and the wrap context in the log only.
func checkFencedAnswer(t *testing.T, sc fencedScope, err error, wrap string) {
	t.Helper()

	rec, logged := writeErrorOf(t, sc.scope, err)

	if rec.Code != sc.status {
		t.Fatalf("status = %d, want %d", rec.Code, sc.status)
	}

	if got := rec.Header().Get("Retry-After") != ""; got != sc.retryAfter {
		t.Fatalf("Retry-After set = %t, want %t", got, sc.retryAfter)
	}

	if got := rec.Body.String(); got != sc.body {
		t.Fatalf("body = %q, want %q", got, sc.body)
	}

	if !strings.Contains(logged, "write fenced") {
		t.Fatalf("log = %q, want the fenced line", logged)
	}

	if wrap == "" {
		return
	}

	if !strings.Contains(logged, wrap) {
		t.Fatalf("log = %q, want the wrap context %q", logged, wrap)
	}

	if strings.Contains(rec.Body.String(), wrap) {
		t.Fatalf("body = %q, must not hold the wrap context %q", rec.Body.String(), wrap)
	}
}

// checkOtherAnswer proves an error that is not a fence keeps its own answer
// and writes no fenced line.
func checkOtherAnswer(t *testing.T, sc fencedScope) {
	t.Helper()

	rec, logged := writeErrorOf(t, sc.scope, sc.other)

	if rec.Code != sc.otherStatus {
		t.Fatalf("status = %d, want %d", rec.Code, sc.otherStatus)
	}

	if got := rec.Body.String(); got != sc.otherBody {
		t.Fatalf("body = %q, want %q", got, sc.otherBody)
	}

	if strings.Contains(logged, "write fenced") {
		t.Fatalf("log = %q, must not hold the fenced line", logged)
	}
}
