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
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// checkGateWorkspaces denies every capability except valid: CheckUpload and
// CheckJSONRenderingUpload answer ErrUploadDenied for any other token, and
// Upload and DiscardJSONRendering fail the test when they see one.
type checkGateWorkspaces struct {
	stubWorkspaces

	t     *testing.T
	valid string
}

func (w checkGateWorkspaces) CheckUpload(
	_ context.Context, _ workspace.StateVersionID, tok token.Token,
) (namespace.Name, error) {
	if tok.Reveal() != w.valid {
		return "", workspace.ErrUploadDenied
	}

	return "acme", nil
}

func (w checkGateWorkspaces) CheckJSONRenderingUpload(
	_ context.Context, _ workspace.StateVersionID, tok token.Token,
) (namespace.Name, error) {
	if tok.Reveal() != w.valid {
		return "", workspace.ErrUploadDenied
	}

	return "acme", nil
}

func (w checkGateWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, tok token.Token, content io.Reader,
) (workspace.StateVersion, error) {
	w.t.Helper()

	if tok.Reveal() != w.valid {
		w.t.Errorf("Upload called with a wrong capability")

		return workspace.StateVersion{}, workspace.ErrUploadDenied
	}

	if _, err := io.ReadAll(content); err != nil {
		return workspace.StateVersion{}, fmt.Errorf("read upload: %w", err)
	}

	return stubStateVersion(), nil
}

func (w checkGateWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, tok token.Token, content io.Reader,
) error {
	w.t.Helper()

	if tok.Reveal() != w.valid {
		w.t.Errorf("DiscardJSONRendering called with a wrong capability")

		return workspace.ErrUploadDenied
	}

	if _, err := io.ReadAll(content); err != nil {
		return fmt.Errorf("read rendering: %w", err)
	}

	return nil
}

// newCheckGateHandler returns a production Handler over ws with gate kept by
// the caller.
func newCheckGateHandler(t *testing.T, ws tfe.WorkspaceService, gate *bodygate.Gate) *tfe.Handler {
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

// readDetector records whether its body was read at all.
type readDetector struct {
	r    io.Reader
	read atomic.Bool
}

func (d *readDetector) Read(p []byte) (int, error) {
	d.read.Store(true)

	n, err := d.r.Read(p)
	if err != nil {
		return n, fmt.Errorf("read body: %w", err)
	}

	return n, nil
}

// wrongToken mints one well-formed but unknown upload capability.
func wrongToken(t *testing.T) string {
	t.Helper()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	return tok.Reveal()
}

// sampleGate polls the gate every 100 microseconds until stop closes and
// returns the highest value it saw on done.
func sampleGate(gate *bodygate.Gate, stop <-chan struct{}) <-chan int64 {
	done := make(chan int64, 1)

	go func() {
		var maxSeen int64

		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				done <- maxSeen

				return
			case <-ticker.C:
				maxSeen = max(maxSeen, gate.InFlight())
			}
		}
	}()

	return done
}

// floodWrongUploads sends n PUTs to target+wrong with a declared
// Content-Length of 100 MiB. It proves every answer is 403, no body was read,
// and the gate never held anything while they ran.
func floodWrongUploads(t *testing.T, handler *tfe.Handler, gate *bodygate.Gate, target string, n int) {
	t.Helper()

	detectors := make([]*readDetector, n)
	codes := make([]int, n)
	stop := make(chan struct{})
	sampled := sampleGate(gate, stop)

	var wg sync.WaitGroup

	for i := range n {
		detectors[i] = &readDetector{r: strings.NewReader("wrong-content"), read: atomic.Bool{}}

		wg.Go(func() {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, target, detectors[i])
			req.ContentLength = 100 << 20

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			codes[i] = rec.Code
		})
	}

	wg.Wait()
	close(stop)

	maxSeen := <-sampled

	for i := range n {
		if codes[i] != http.StatusForbidden {
			t.Errorf("%s upload %d answers %d, want %d", target, i, codes[i], http.StatusForbidden)
		}

		if detectors[i].read.Load() {
			t.Errorf("%s upload %d read its body before the capability check", target, i)
		}
	}

	if maxSeen > 0 {
		t.Errorf("%s uploads held %d bytes with wrong capabilities, want 0", target, maxSeen)
	}

	if got := gate.InFlight(); got != 0 {
		t.Errorf("InFlight() = %d after %s uploads, want 0", got, target)
	}
}

// TestWrongTokenUploadsDoNotReserve proves a wrong upload capability never
// reserves budget: 64 concurrent uploads with a wrong token and a declared
// length of 100 MiB all answer 403, no body is read, and the gate never holds
// anything. A valid upload then succeeds.
func TestWrongTokenUploadsDoNotReserve(t *testing.T) {
	t.Parallel()

	validTok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	valid := validTok.Reveal()
	bad := wrongToken(t)

	gate := bodygate.New(64 << 20)
	handler := newCheckGateHandler(t, checkGateWorkspaces{stubWorkspaces: stubWorkspaces{}, t: t, valid: valid}, gate)

	floodWrongUploads(t, handler, gate, "/api/v2/state-uploads/sv-new123/"+bad, 64)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+valid, `{"version":4}`),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("valid state upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after the valid state upload, want 0", got)
	}

	floodWrongUploads(t, handler, gate, "/api/v2/json-state-uploads/sv-new123/"+bad, 64)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/json-state-uploads/sv-new123/"+valid, `{"rendering":1}`),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("valid rendering upload answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after the valid rendering upload, want 0", got)
	}
}

// TestUploadChecksTokenBeforeBusyGate proves the capability check runs before
// the budget reservation: with a gate of 1 byte a wrong capability answers
// 403 (not 503) on both upload routes, while a valid one meets the full gate
// with 503 and Retry-After 1.
func TestUploadChecksTokenBeforeBusyGate(t *testing.T) {
	t.Parallel()

	validTok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	valid := validTok.Reveal()
	bad := wrongToken(t)

	gate := bodygate.New(1)
	handler := newCheckGateHandler(t, checkGateWorkspaces{stubWorkspaces: stubWorkspaces{}, t: t, valid: valid}, gate)

	for _, tc := range []struct {
		name   string
		target string
	}{
		{name: "state upload with a wrong capability", target: "/api/v2/state-uploads/sv-new123/" + bad},
		{name: "rendering upload with a wrong capability", target: "/api/v2/json-state-uploads/sv-new123/" + bad},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, uploadRequest(t, tc.target, "state-content"))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("request answers %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		target string
	}{
		{name: "state upload with a valid capability", target: "/api/v2/state-uploads/sv-new123/" + valid},
		{name: "rendering upload with a valid capability", target: "/api/v2/json-state-uploads/sv-new123/" + valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, uploadRequest(t, tc.target, "state-content"))

			checkBusy(t, rec)
		})
	}
}
