package tofuhttp_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// newGateHandler returns a handler over units with gate, built the same way
// as newTestHandler.
func newGateHandler(t *testing.T, units tofuhttp.WorkspaceService, gate *bodygate.Gate) http.Handler {
	t.Helper()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     units,
		Namespaces:     fakeNamespaces{},
		Audit:          newTestAudit(),
		Gate:           gate,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return h
}

// countingUnits records how often WriteState runs.
type countingUnits struct {
	*fakeUnits

	calls atomic.Int32
}

// WriteState records the call and runs the fake units.
func (c *countingUnits) WriteState(
	ctx context.Context, id workspace.ID, in workspace.StreamInput, auth workspace.StreamAuthority, body io.Reader,
) (workspace.StateVersion, error) {
	c.calls.Add(1)

	return c.fakeUnits.WriteState(ctx, id, in, auth, body)
}

// postState sends a POST with body to the unit path as the admin.
func postState(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		unitPath,
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// waitForStreamCost waits until gate holds StreamCost, failing the test when
// the deadline passes first.
func waitForStreamCost(t *testing.T, gate *bodygate.Gate) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()

	for {
		if gate.InFlight() == bodygate.StreamCost {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("InFlight() = %d, want %d", gate.InFlight(), bodygate.StreamCost)
		case <-timer.C:
			timer.Reset(time.Millisecond)
		}
	}
}

// TestGateBusyPost proves a POST answers 503 with Retry-After when the gate
// is full, without reaching the workspace service.
func TestGateBusyPost(t *testing.T) {
	t.Parallel()

	units := &countingUnits{fakeUnits: newFakeUnits(true), calls: atomic.Int32{}}
	h := newGateHandler(t, units, bodygate.New(1))

	rec := postState(h, `{"serial":1,"lineage":"l"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want %d; body %q", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}

	if !strings.Contains(rec.Body.String(), "server is busy: try again in one second") {
		t.Fatalf("body %q lacks the busy text", rec.Body.String())
	}

	if got := units.calls.Load(); got != 0 {
		t.Fatalf("WriteState ran %d times with a full gate, want none", got)
	}
}

// TestGateReleasePostOnSuccess proves a POST succeeds with an ample gate and
// releases the reservation after the response.
func TestGateReleasePostOnSuccess(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	h := newGateHandler(t, newFakeUnits(true), gate)

	rec := postState(h, `{"serial":1,"lineage":"l"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d; body %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after success, want 0", got)
	}
}

// TestGateReleasePostOnError proves a failing POST releases the reservation
// after the response.
func TestGateReleasePostOnError(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	units := newFakeUnits(true)
	units.writeErr = workspace.ErrInvalidState
	h := newGateHandler(t, units, gate)

	rec := postState(h, `{"serial":1,"lineage":"l"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want %d; body %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after error, want 0", got)
	}
}

// signalUnits closes started when WriteState runs, then drains the body.
type signalUnits struct {
	*fakeUnits

	started chan struct{}
	once    sync.Once
}

// WriteState signals the start and runs the fake units.
func (s *signalUnits) WriteState(
	ctx context.Context, id workspace.ID, in workspace.StreamInput, auth workspace.StreamAuthority, body io.Reader,
) (workspace.StateVersion, error) {
	s.once.Do(func() { close(s.started) })

	return s.fakeUnits.WriteState(ctx, id, in, auth, body)
}

// TestGateHoldsStreamCostWhilePostRuns proves a running POST holds StreamCost
// while its body stays open, and releases it when the body ends.
func TestGateHoldsStreamCostWhilePostRuns(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	units := &signalUnits{fakeUnits: newFakeUnits(true), started: make(chan struct{}), once: sync.Once{}}
	h := newGateHandler(t, units, gate)

	reader, writer := io.Pipe()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, unitPath, reader)
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	select {
	case <-units.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not start reading the state")
	}

	waitForStreamCost(t, gate)

	if err := writer.Close(); err != nil {
		t.Fatalf("close state body: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the POST did not finish after the body closed")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want %d; body %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after the POST, want 0", got)
	}
}
