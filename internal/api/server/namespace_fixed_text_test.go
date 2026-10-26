package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestNamespaceSentinelsAnswerFixedText proves that the answer to a namespace
// refusal holds the text of the sentinel and never the wrap, which names the
// operation and the namespace.
func TestNamespaceSentinelsAnswerFixedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{"invalid page", namespace.ErrInvalidPage, http.StatusBadRequest, namespace.ErrInvalidPage.Error()},
		{"already exists", namespace.ErrAlreadyExists, http.StatusConflict, namespace.ErrAlreadyExists.Error()},
		{"no paging", namespace.ErrNoPaging, http.StatusNotImplemented, namespace.ErrNoPaging.Error()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapped := fmt.Errorf("list namespaces of acme-secret: %w", tc.err)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/namespaces", http.NoBody)
			rec := httptest.NewRecorder()
			apiserver.WriteErrorForTest(rec, r, wrapped)

			var doc struct {
				Message string `json:"message"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}

			if rec.Code != tc.status || doc.Message != tc.want || strings.Contains(rec.Body.String(), "acme-secret") {
				t.Fatalf("answer = %d %s, want %d with message %q and no wrap text",
					rec.Code, rec.Body.String(), tc.status, tc.want)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}
