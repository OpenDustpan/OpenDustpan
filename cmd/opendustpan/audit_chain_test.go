package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// auditChainFake answers the audit chain route with a waiting change to
// off, and records the method of each request that carried the token.
type auditChainFake struct {
	server  *httptest.Server
	posts   atomic.Int64
	gets    atomic.Int64
	refused atomic.Int64
}

// newAuditChainFake starts the fake server, expecting token as the Bearer
// credential.
func newAuditChainFake(t *testing.T, token string, approveStatus int) *auditChainFake {
	t.Helper()

	f := &auditChainFake{server: nil, posts: atomic.Int64{}, gets: atomic.Int64{}, refused: atomic.Int64{}}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/server/audit/chain" || r.Header.Get("Authorization") != "Bearer "+token {
			f.refused.Add(1)
			http.Error(w, `{"code":403,"message":"denied"}`, http.StatusForbidden)

			return
		}

		w.Header().Set("Content-Type", "application/json")

		status := http.StatusOK

		if r.Method == http.MethodPost {
			f.posts.Add(1)

			status = approveStatus
		} else {
			f.gets.Add(1)
		}

		w.WriteHeader(status)

		if status >= http.StatusBadRequest {
			refusal := `{"code":409,"message":"no audit chain change waits for approval"}`
			if _, err := w.Write([]byte(refusal)); err != nil {
				t.Errorf("write refusal: %v", err)
			}

			return
		}

		doc := `{"state":"on","setting":"off","changedAt":"2026-10-15T00:00:00Z",` +
			`"changedBy":"alice","pending":true,"migrating":false}`
		if _, err := w.Write([]byte(doc)); err != nil {
			t.Errorf("write state: %v", err)
		}
	}))

	t.Cleanup(f.server.Close)

	return f
}

// runAuditChain runs the audit-chain command sub against server and answers
// its stdout and error.
func runAuditChain(t *testing.T, sub, server string) (string, error) {
	t.Helper()
	t.Setenv(adminTokenEnvVar, "test-token")

	var stdout, stderr bytes.Buffer

	err := run(t.Context(), []string{"audit-chain", sub, "--server", server}, &stdout, &stderr)

	return stdout.String(), err
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestAuditChainStatusCommand(t *testing.T) {
	fake := newAuditChainFake(t, "test-token", http.StatusAccepted)

	out, err := runAuditChain(t, "status", fake.server.URL)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if !strings.Contains(out, "stored state on, setting of this server off, a change waits for approval") ||
		!strings.Contains(out, "by alice") {
		t.Fatalf("status output = %q, want the state, the setting and the waiting change", out)
	}

	if fake.gets.Load() != 1 || fake.posts.Load() != 0 || fake.refused.Load() != 0 {
		t.Fatalf("requests: %d GET, %d POST, %d refused; want one GET", fake.gets.Load(), fake.posts.Load(),
			fake.refused.Load())
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestAuditChainApproveCommand(t *testing.T) {
	fake := newAuditChainFake(t, "test-token", http.StatusAccepted)

	out, err := runAuditChain(t, "approve", fake.server.URL)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if !strings.Contains(out, "approved the change of the audit chain to off") {
		t.Fatalf("approve output = %q, want the approved change", out)
	}

	if fake.posts.Load() != 1 {
		t.Fatalf("POST requests = %d, want 1", fake.posts.Load())
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestAuditChainApproveCommandReportsARefusal(t *testing.T) {
	fake := newAuditChainFake(t, "test-token", http.StatusConflict)

	_, err := runAuditChain(t, "approve", fake.server.URL)
	if err == nil || !strings.Contains(err.Error(), "no audit chain change waits") {
		t.Fatalf("approve with nothing waiting: %v, want the server message", err)
	}
}
