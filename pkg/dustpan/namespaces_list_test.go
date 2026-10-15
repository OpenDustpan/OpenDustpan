package dustpan_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// namespaceListItem decodes one namespace of the list.
type namespaceListItem struct {
	Name string `json:"name"`
}

// namespaceListPage decodes one page of GET /api/server/namespaces.
type namespaceListPage struct {
	Namespaces []namespaceListItem `json:"namespaces"`
	NextCursor string              `json:"nextCursor"`
	Total      *int64              `json:"total"`
}

// serveServerAPI sends one installation API request with the bootstrap
// token and returns its status and body.
func serveServerAPI(t *testing.T, f fixture, method, path, body string) (int, string) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, string(data)
}

// TestNamespaceListThroughServer proves the wiring hands the real store to
// the namespace service: two namespaces created through the API come back
// from the list.
func TestNamespaceListThroughServer(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, name := range []string{"acme", "bravo"} {
		status, body := serveServerAPI(t, f, http.MethodPost, "/api/server/namespaces", `{"name":"`+name+`"}`)
		if status != http.StatusCreated {
			t.Fatalf("POST namespace %q answers %d, want %d (%s)", name, status, http.StatusCreated, body)
		}
	}

	status, body := serveServerAPI(t, f, http.MethodGet, "/api/server/namespaces", "")
	if status != http.StatusOK {
		t.Fatalf("GET namespaces answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var page namespaceListPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode list %q: %v", body, err)
	}

	got := make([]string, 0, len(page.Namespaces))
	for _, ns := range page.Namespaces {
		got = append(got, ns.Name)
	}

	if len(got) != 2 || got[0] != "acme" || got[1] != "bravo" {
		t.Fatalf("namespaces = %v, want [acme bravo]", got)
	}

	if page.Total == nil || *page.Total != 2 {
		t.Fatalf("total = %v, want 2", page.Total)
	}
}
