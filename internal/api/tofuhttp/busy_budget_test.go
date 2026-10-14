package tofuhttp_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestPostBusyAnswers503 proves a POST whose service reports a full body
// budget answers 503 with Retry-After and releases the reservation.
func TestPostBusyAnswers503(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	units := newFakeUnits(true)
	units.writeErr = workspace.ErrBusy
	h := newGateHandler(t, units, gate)

	rec := postState(h, `{"serial":2,"lineage":"busy"}`)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want %d; body %q", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}

	if !strings.Contains(rec.Body.String(), "server is busy: try again in one second") {
		t.Fatalf("body %q lacks the busy text", rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a busy POST, want 0", got)
	}
}
