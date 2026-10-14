package tfe_test

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/workspace"
)

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

	checkBusy(t, rec)

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a busy upload, want 0", got)
	}
}
