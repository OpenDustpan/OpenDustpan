package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNamespaceOverrideEmptyBodyChangesNothing proves that a PUT with no body
// answers 400 and leaves the switches as they were (ADR 44, decision 6).
func TestNamespaceOverrideEmptyBodyChangesNothing(t *testing.T) {
	t.Parallel()

	handler := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	path := "/api/server/namespaces/" + testNamespace + "/override-switches"

	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, path, http.NoBody)
	put.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty PUT answers %d, want 400: %s", rec.Code, rec.Body.String())
	}

	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	get.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"allowForcedWrites":false`) ||
		!strings.Contains(rec.Body.String(), `"allowUnlockedWrites":false`) {
		t.Fatalf("switches after an empty PUT: %d %s, want both off", rec.Code, rec.Body.String())
	}
}
