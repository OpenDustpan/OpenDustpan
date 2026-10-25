package tfe_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errorDetail is one error of a JSON:API error document.
type errorDetail struct {
	Detail string `json:"detail"`
}

// errorDetails is the part of a JSON:API error document that the test reads.
type errorDetails struct {
	Errors []errorDetail `json:"errors"`
}

// TestManualLockDetailHasNoWrapperText sends a refusal of a manual lock that a
// service wrapped twice to the error writer. The detail is the text of the
// refusal and the route that removes the lock, with none of the wrapper text.
func TestManualLockDetailHasNoWrapperText(t *testing.T) {
	t.Parallel()

	unit := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}
	manual := &manuallock.LockedError{
		Lock: manuallock.Lock{
			Since: time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC), Holder: "user-1", Reason: "release freeze",
			Target: manuallock.UnitTarget(unit),
		},
		Unit: unit, Noun: "workspace", Name: "acme/web",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/x", http.NoBody)
	tfe.WriteErrorForTest(rec, req, fmt.Errorf("upload state: internal step: %w: %w", workspace.ErrLocked, manual))

	var doc errorDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 {
		t.Fatalf("answer = %s, want one error: %v", rec.Body.String(), err)
	}

	want := "workspace acme/web has a manual lock by user-1 since 2026-10-23T09:30:00Z: release freeze. " +
		"To remove the lock, the holder sends DELETE /api/inventory/unit-lock?unit=opentofu:acme/web."
	if got := doc.Errors[0].Detail; got != want || strings.Contains(got, "internal step") {
		t.Errorf("detail = %q, want %q", got, want)
	}
}
