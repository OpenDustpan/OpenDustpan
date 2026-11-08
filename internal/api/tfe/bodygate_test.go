package tfe_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// newGateWriteHandler returns a production Handler over ws with gate, built
// the same way as newWriteHandler.
func newGateWriteHandler(t *testing.T, ws tfe.WorkspaceService, gate *bodygate.Gate) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           gate,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// gateWriteService returns a writeWorkspaces whose three funcs record their
// calls and answer want.
func gateWriteService(called *bool, want error) writeWorkspaces {
	return writeWorkspaces{
		stubWorkspaces: stubWorkspaces{},
		begin: func(
			_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
		) (workspace.PendingState, error) {
			*called = true

			return workspace.PendingState{}, want
		},
		upload: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
		) (workspace.StateVersion, error) {
			*called = true

			return workspace.StateVersion{}, want
		},
		discard: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
		) error {
			*called = true

			return want
		},
	}
}

// checkBusy proves rec is the full-gate answer: 503 with Retry-After 1 and a
// JSON:API error body naming the busy server.
func checkBusy(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	checkJSONAPIError(t, rec, http.StatusServiceUnavailable)

	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}

	if !strings.Contains(rec.Body.String(), "server is busy") {
		t.Fatalf("error body lacks the busy text: %s", rec.Body.String())
	}
}

// gateUploadToken mints one upload capability for the upload tests.
func gateUploadToken(t *testing.T) string {
	t.Helper()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	return tok.Reveal()
}

// TestGateBusyAnswers503 proves the three state write routes answer 503 with
// Retry-After when the gate is full, without reaching the service.
func TestGateBusyAnswers503(t *testing.T) {
	t.Parallel()

	t.Run("state upload", func(t *testing.T) {
		t.Parallel()

		var called bool

		handler := newGateWriteHandler(t, gateWriteService(&called, nil), bodygate.New(1))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(
			rec,
			uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+gateUploadToken(t), "state-content"),
		)

		checkBusy(t, rec)
		errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusServiceUnavailable, rec.Body.Bytes(),
			errcodetest.Want{Code: errcode.StateVersionBusy, Args: nil, RequestBody: nil})

		if called {
			t.Fatal("full gate reached the Upload service")
		}
	})

	t.Run("json rendering upload", func(t *testing.T) {
		t.Parallel()

		var called bool

		handler := newGateWriteHandler(t, gateWriteService(&called, nil), bodygate.New(1))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(
			rec,
			uploadRequest(t, "/api/v2/json-state-uploads/sv-new123/"+gateUploadToken(t), `{"rendering":1}`),
		)

		checkBusy(t, rec)

		if called {
			t.Fatal("full gate reached the DiscardJSONRendering service")
		}
	})

	t.Run("state version create", func(t *testing.T) {
		t.Parallel()

		var called bool

		handler := newGateWriteHandler(t, gateWriteService(&called, nil), bodygate.New(1))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, stateCreateOutputs()),
		))

		checkBusy(t, rec)

		if called {
			t.Fatal("full gate reached the BeginState service")
		}
	})
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

// TestGateReleasesUploadOnSuccess proves a state upload succeeds with an
// ample gate and releases the reservation after the response.
func TestGateReleasesUploadOnSuccess(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)

	setup := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, content io.Reader,
	) (workspace.StateVersion, error) {
		if _, err := io.ReadAll(content); err != nil {
			return workspace.StateVersion{}, fmt.Errorf("read upload content: %w", err)
		}

		return stubStateVersion(), nil
	})
	handler := newGateWriteHandler(t, setup.service, gate)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, `{"version":4}`),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after success, want 0", got)
	}
}

// TestGateReleasesRenderingOnSuccess proves a JSON rendering upload succeeds
// with an ample gate and releases the reservation after the response.
func TestGateReleasesRenderingOnSuccess(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	reveal := gateUploadToken(t)

	handler := newGateWriteHandler(t, writeWorkspaces{
		stubWorkspaces: stubWorkspaces{},
		begin: func(
			_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
		) (workspace.PendingState, error) {
			return workspace.PendingState{}, workspace.ErrNotLocked
		},
		upload: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
		) (workspace.StateVersion, error) {
			return workspace.StateVersion{}, workspace.ErrUploadDenied
		},
		discard: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, content io.Reader,
		) error {
			if _, err := io.ReadAll(content); err != nil {
				return fmt.Errorf("read rendering content: %w", err)
			}

			return nil
		},
	}, gate)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/json-state-uploads/sv-new123/"+reveal, `{"rendering":1}`),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("rendering upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after success, want 0", got)
	}
}

// TestGateReleasesCreateOnSuccess proves a state version create succeeds with
// an ample gate and releases the reservation after the response.
func TestGateReleasesCreateOnSuccess(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	handler := newGateWriteHandler(t, stubWorkspaces{}, gate)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, createStateRequest(
		t,
		"/api/v2/workspaces/ws-test123/state-versions",
		stateCreateBody(4, stateCreateOutputs()),
	))

	if rec.Code != http.StatusCreated {
		t.Fatalf("create answers %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after success, want 0", got)
	}
}

// TestGateReleasesOnError proves a failing upload releases the reservation
// after the response.
func TestGateReleasesOnError(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)

	setup := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
	) (workspace.StateVersion, error) {
		return workspace.StateVersion{}, workspace.ErrChecksumMismatch
	})
	handler := newGateWriteHandler(t, setup.service, gate)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, "state"),
	)

	checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after error, want 0", got)
	}
}

// TestGateHoldsStreamCostWhileUploadRuns proves a running state upload holds
// StreamCost while its body stays open, and releases it when the body ends.
func TestGateHoldsStreamCostWhileUploadRuns(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	started := make(chan struct{})

	var once sync.Once

	service := writeWorkspaces{
		stubWorkspaces: stubWorkspaces{},
		begin: func(
			_ context.Context, _ workspace.ID, _ string, _ workspace.NewState,
		) (workspace.PendingState, error) {
			return workspace.PendingState{}, workspace.ErrNotLocked
		},
		upload: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, content io.Reader,
		) (workspace.StateVersion, error) {
			once.Do(func() { close(started) })

			if _, err := io.ReadAll(content); err != nil {
				return workspace.StateVersion{}, fmt.Errorf("read upload content: %w", err)
			}

			return stubStateVersion(), nil
		},
		discard: func(
			_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
		) error {
			return nil
		},
	}
	handler := newGateWriteHandler(t, service, gate)

	reader, writer := io.Pipe()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/api/v2/state-uploads/sv-new123/"+gateUploadToken(t),
		reader,
	)
	req.ContentLength = -1

	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		defer close(done)
		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not start reading the upload")
	}

	waitForStreamCost(t, gate)

	if err := writer.Close(); err != nil {
		t.Fatalf("close upload body: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the upload did not finish after the body closed")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after the upload, want 0", got)
	}
}
