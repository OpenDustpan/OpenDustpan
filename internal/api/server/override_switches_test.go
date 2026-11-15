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
	path := "/api/dustpan/v1/namespaces/" + testNamespace + "/override-switches"

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

// TestNamespaceOverrideBodyNeedsBothSwitches proves that a PUT answers 400 for
// every body that does not name both switches and no other field, and that
// such a body changes no switch (ADR 44, decision 6).
func TestNamespaceOverrideBodyNeedsBothSwitches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "empty object", body: `{}`},
		{name: "null", body: `null`},
		{name: "forced switch only", body: `{"allowForcedWrites":true}`},
		{name: "unlocked switch only", body: `{"allowUnlockedWrites":true}`},
		{name: "unknown field", body: `{"allowForcedWrites":true,"allowUnlockedWrites":true,"extra":1}`},
		{name: "two objects", body: `{"allowForcedWrites":true,"allowUnlockedWrites":true}{}`},
		{name: "not JSON", body: `allow`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
			path := "/api/dustpan/v1/namespaces/" + testNamespace + "/override-switches"

			put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, path, strings.NewReader(tt.body))
			put.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, put)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s answers %d, want 400: %s", tt.body, rec.Code, rec.Body.String())
			}

			if strings.Contains(rec.Body.String(), `{"name"`) {
				t.Fatalf("400 names the namespace create body: %s", rec.Body.String())
			}

			get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			get.Header.Set("Authorization", "Bearer test-token")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, get)

			if !strings.Contains(rec.Body.String(), `"allowForcedWrites":false`) ||
				!strings.Contains(rec.Body.String(), `"allowUnlockedWrites":false`) {
				t.Fatalf("switches after PUT %s: %s, want both off", tt.body, rec.Body.String())
			}
		})
	}
}

// TestNamespaceOverrideBothSwitchesSets proves that a PUT that names both
// switches answers 200 with them as stored.
func TestNamespaceOverrideBothSwitchesSets(t *testing.T) {
	t.Parallel()

	handler := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	path := "/api/dustpan/v1/namespaces/" + testNamespace + "/override-switches"

	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, path,
		strings.NewReader(`{"allowForcedWrites":true,"allowUnlockedWrites":false}`))
	put.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"allowForcedWrites":true`) {
		t.Fatalf("PUT with both switches answers %d %s, want 200 with forced on", rec.Code, rec.Body.String())
	}
}
