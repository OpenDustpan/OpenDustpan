package tfe_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// checkTransientBusy proves rec is the transient budget answer: 503 with a
// Retry-After of 1 to 3 seconds and the busy text.
func checkTransientBusy(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	checkJSONAPIError(t, rec, http.StatusServiceUnavailable)

	switch got := rec.Header().Get("Retry-After"); got {
	case "1", "2", "3":
	default:
		t.Fatalf("Retry-After = %q, want 1, 2 or 3", got)
	}

	if !strings.Contains(rec.Body.String(), "server is busy") {
		t.Fatalf("error body lacks the busy text: %s", rec.Body.String())
	}
}

// TestUploadBusyAnswers503 proves a state upload whose service reports a full
// body budget answers 503 with Retry-After and releases the reservation.
func TestUploadBusyAnswers503(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)

	setup := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
	) (workspace.StateVersion, error) {
		return workspace.StateVersion{}, workspace.ErrBusy
	})
	handler := newGateWriteHandler(t, setup.service, gate)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, "state-content"),
	)

	checkTransientBusy(t, rec)

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a busy upload, want 0", got)
	}
}

// TestUploadTooSmallAnswers413 proves a state upload whose service reports a
// budget that can never fit answers 413 with the flag name.
func TestUploadTooSmallAnswers413(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)

	tooSmall := fmt.Errorf(
		"%w: this upload needs at least %d bytes of body budget and the server has %d bytes: set --max-body-bytes-in-flight to raise the budget",
		workspace.ErrBudgetTooSmall,
		2<<20,
		1<<20,
	)

	setup := newUploadSetup(t, func(
		_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
	) (workspace.StateVersion, error) {
		return workspace.StateVersion{}, tooSmall
	})
	handler := newGateWriteHandler(t, setup.service, gate)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		uploadRequest(t, "/api/v2/state-uploads/sv-new123/"+setup.reveal, "state-content"),
	)

	checkJSONAPIError(t, rec, http.StatusRequestEntityTooLarge)

	if !strings.Contains(rec.Body.String(), "--max-body-bytes-in-flight") {
		t.Fatalf("error body %q lacks the flag name", rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a too-small upload, want 0", got)
	}
}
