package dustpan

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const muxTestToken = "admin-token-for-the-mux-layer-test-of-the-server"

// newMuxServer builds a server over a new store with the components set.
func newMuxServer(t *testing.T, set Components) *Server {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	authn, err := identity.NewStaticToken("admin", muxTestToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	srv, err := NewServer(
		WithInstallation("test"), WithNamespaceStore(st), WithStackStore(st), WithUpdateStore(st),
		WithCheckpointStore(st), WithEventStore(st), WithSecretKeyStore(st), WithSecretWrapper(wrapper),
		WithWorkspaceStore(st), WithStateStore(st), WithBlobStore(blobs), WithInventoryReader(st),
		WithAuthenticator(authn), WithComponents(set),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// statusOf sends a GET with the admin token to h and answers the status.
func statusOf(t *testing.T, h http.Handler, target string) int {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.invalid"+target, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+muxTestToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code
}

// TestWorkerMuxHoldsNoAPIRoute proves the second layer of the component
// gate alone: the handler before the gate, which holds only the mux, answers
// no API route when the process has no api component. Every request here
// skips the gate, as if the gate failed or a later change removed it.
func TestWorkerMuxHoldsNoAPIRoute(t *testing.T) {
	t.Parallel()

	worker := newMuxServer(t, workerOnly()).ungated

	for _, target := range []string{
		"/api/server/namespaces", "/api/dustpan/v1/components", "/api/server/backups", "/api/user", "/api/stacks",
		"/api/v2/ping", "/api/inventory/units", "/api/dustpan/v1/units", "/tofu/state/x", "/runner/v1/claim",
		"/.well-known/terraform.json", "/", "/console", "/nothing",
		"/health/ready/../api/server/namespaces", "/health/ready/%2e%2e/api/server/namespaces",
		"//api/server/namespaces", "/health//ready/../api/user",
	} {
		if got := statusOf(t, worker, target); got != http.StatusNotFound && got != http.StatusTemporaryRedirect {
			t.Errorf("worker mux, GET %s: status %d, want 404 or a redirect to the clean path", target, got)
		}
	}

	for _, target := range []string{"/health/live", "/health/ready", "/health/ready/worker", "/api/server/metrics"} {
		if got := statusOf(t, worker, target); got == http.StatusNotFound {
			t.Errorf("worker mux, GET %s: status 404, want the probe or the metrics route", target)
		}
	}

	if got := statusOf(t, worker, "/api/server/metrics"); got != http.StatusOK {
		t.Errorf("worker mux, GET /api/server/metrics: status %d, want 200", got)
	}

	if got := statusOf(t, newMuxServer(t, AllComponents()).ungated, "/api/server/namespaces"); got != http.StatusOK {
		t.Errorf("mux with the api component, GET /api/server/namespaces: status %d, want 200", got)
	}
}
