package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// namespaceTime is the name and the create time of a namespace in an answer.
type namespaceTime struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

// namespaceTimes is the page of the namespace list, with the fields that the
// test reads.
type namespaceTimes struct {
	Namespaces []namespaceTime `json:"namespaces"`
}

// sendAsAdmin sends one request with the token of an administrator and
// returns the answer.
func sendAsAdmin(t *testing.T, handler testHandler, method, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, "/api/server/namespaces", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// TestNamespaceCreateTimeEqualsTheListTime proves that the createdAt of the
// answer of a namespace create is the createdAt that the namespace list gives
// for the same namespace, so a client that compares the two values sees one
// time.
func TestNamespaceCreateTimeEqualsTheListTime(t *testing.T) {
	t.Parallel()

	handler := newListNoAccessHandler(t, nil, testCursors(t))

	rec := sendAsAdmin(t, handler, http.MethodPost, `{"name":"acme"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST namespaces answers %d, want %d (%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created namespaceTime

	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode the create answer %q: %v", rec.Body.String(), err)
	}

	rec = sendAsAdmin(t, handler, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET namespaces answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var list namespaceTimes

	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the list %q: %v", rec.Body.String(), err)
	}

	if len(list.Namespaces) != 1 || list.Namespaces[0] != created {
		t.Fatalf("list answers %s, want acme with createdAt %q of the create answer", rec.Body.String(),
			created.CreatedAt)
	}
}
