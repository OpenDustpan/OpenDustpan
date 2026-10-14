package tofuhttp_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// checkTransientRetryAfter proves the Retry-After of a transient budget
// refusal is 1, 2 or 3 seconds.
func checkTransientRetryAfter(t *testing.T, recBody, got string) {
	t.Helper()

	switch got {
	case "1", "2", "3":
	default:
		t.Fatalf("Retry-After = %q, want 1, 2 or 3; body %q", got, recBody)
	}
}

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

	checkTransientRetryAfter(t, rec.Body.String(), rec.Header().Get("Retry-After"))

	if !strings.Contains(rec.Body.String(), "server is busy: try again after the Retry-After time") {
		t.Fatalf("body %q lacks the busy text", rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a busy POST, want 0", got)
	}
}

// TestPostTooSmallAnswers413 proves a POST whose service reports a budget
// that can never fit answers 413 with the flag name.
func TestPostTooSmallAnswers413(t *testing.T) {
	t.Parallel()

	gate := bodygate.New(0)
	units := newFakeUnits(true)
	units.writeErr = fmt.Errorf(
		"%w: this upload needs at least %d bytes of body budget and the server has %d bytes: set --max-body-bytes-in-flight to raise the budget",
		workspace.ErrBudgetTooSmall,
		2<<20,
		1<<20,
	)
	h := newGateHandler(t, units, gate)

	rec := postState(h, `{"serial":2,"lineage":"busy"}`)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want %d; body %q", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "--max-body-bytes-in-flight") {
		t.Fatalf("body %q lacks the flag name", rec.Body.String())
	}

	if got := gate.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after a too-small POST, want 0", got)
	}
}
