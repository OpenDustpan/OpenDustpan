package client_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// serveNamespaceCreate answers a namespace create with status and body.
func serveNamespaceCreate(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		_, _ = w.Write([]byte(body)) //nolint:errcheck // The client may close early on an oversized body.
	}))
	t.Cleanup(srv.Close)

	return srv
}

// createNamespace runs namespace create against srv and returns its output
// and error.
func createNamespace(t *testing.T, srv *httptest.Server) (string, error) {
	t.Helper()

	t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

	var stdout, stderr bytes.Buffer

	err := client.CreateNamespace(t.Context(), []string{"--server", srv.URL, "acme"}, &stdout, &stderr)

	return stdout.String(), err
}

// TestCreateNamespacePrintsTheNamespace proves the output of a created
// namespace. The test sets the admin token, so it cannot run in parallel.
//
//nolint:paralleltest // The test sets the admin token, so it cannot run in parallel.
func TestCreateNamespacePrintsTheNamespace(t *testing.T) {
	srv := serveNamespaceCreate(t, http.StatusCreated, `{"name":"acme","homeRegion":"local","failoverVersion":1}`)

	out, err := createNamespace(t, srv)
	if err != nil || !strings.Contains(out, "created namespace acme in region local") {
		t.Fatalf("namespace create = %q, %v; want the created namespace", out, err)
	}
}

// TestCreateNamespaceCapsTheAnswer proves that the answer of a namespace
// create is read up to MaxClientBodyBytes, as for every other command of the
// installation API: a larger answer is refused, and a larger error body
// answers the status alone. The test sets the admin token, so it cannot run
// in parallel.
//
//nolint:paralleltest // The test sets the admin token, so it cannot run in parallel.
func TestCreateNamespaceCapsTheAnswer(t *testing.T) {
	huge := `{"name":"acme","homeRegion":"local","pad":"` + strings.Repeat("x", client.MaxClientBodyBytes) + `"}`

	_, err := createNamespace(t, serveNamespaceCreate(t, http.StatusCreated, huge))
	if !errors.Is(err, client.ErrResponseTooLarge) || !errors.Is(err, client.ErrNamespaceCreate) {
		t.Fatalf("oversized answer: err = %v, want ErrNamespaceCreate and ErrResponseTooLarge", err)
	}

	hugeError := `{"message":"` + strings.Repeat("y", client.MaxClientBodyBytes) + `"}`

	_, err = createNamespace(t, serveNamespaceCreate(t, http.StatusConflict, hugeError))
	if err == nil || !errors.Is(err, client.ErrNamespaceCreate) || strings.Contains(err.Error(), "yyyy") {
		t.Fatalf("oversized error body: err holds the body or lacks ErrNamespaceCreate: %.200v", err)
	}

	_, err = createNamespace(t, serveNamespaceCreate(t, http.StatusConflict, `{"message":"namespace exists"}`))
	if err == nil || !strings.Contains(err.Error(), "409 Conflict: namespace exists") {
		t.Fatalf("error body: err = %v, want the status and the message", err)
	}
}
