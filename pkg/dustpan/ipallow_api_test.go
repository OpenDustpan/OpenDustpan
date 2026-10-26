package dustpan_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// newIPAllowlistAPIServer opens a store with an empty namespace acme and
// returns a server over it with IP allowlists on and the bootstrap admin
// checked too.
func newIPAllowlistAPIServer(t *testing.T) *dustpan.Server {
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

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	created := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC),
		Name:            "acme",
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if createErr := st.CreateNamespace(t.Context(), created); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	srv, err := dustpan.NewServer(append(signInOptions(t, st),
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithIPAllowlists(true),
		dustpan.WithIPAllowlistsIncludeBootstrap(true),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	registerServer(srv, st, func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) })

	return srv
}

// ipAPIRequest is one installation API request: its method, path, client
// address and body. Every call signs in with the admin token.
type ipAPIRequest struct {
	method string
	path   string
	remote string
	body   string
}

// serveIPAllowAPI serves req and answers its status and body.
func serveIPAllowAPI(t *testing.T, srv *dustpan.Server, req ipAPIRequest) (int, string) {
	t.Helper()

	httpReq := httptest.NewRequestWithContext(t.Context(), req.method, req.path, strings.NewReader(req.body))
	httpReq.RemoteAddr = req.remote
	httpReq.Header.Set("Authorization", "Bearer "+adminToken)

	// A change of a list needs a console session with a recent sign-in.
	if needsConsole(req.method, req.path) {
		addConsole(t, srv, httpReq, consolePerson)
	}

	rec := httptest.NewRecorder()
	startedHandler(t, srv).ServeHTTP(rec, httpReq)

	respBody, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return rec.Code, string(respBody)
}

// TestIPAllowlistAPIThroughHandler proves the API, the store and the gate
// work together: PUT stores a list for acme that GET shows, a client address
// outside the list is refused on a namespace route, and DELETE lifts the
// refusal.
func TestIPAllowlistAPIThroughHandler(t *testing.T) {
	t.Parallel()

	srv := newIPAllowlistAPIServer(t)
	inside := "198.51.100.7:1234"
	outside := "192.0.2.10:5678"
	allowlistPath := "/api/server/namespaces/acme/ip-allowlist"
	switchesPath := "/api/server/namespaces/acme/override-switches"

	code, body := serveIPAllowAPI(t, srv, ipAPIRequest{
		method: http.MethodPut, path: allowlistPath, remote: inside, body: `{"cidrs":["198.51.100.0/24"]}`,
	})
	if code != http.StatusOK || !strings.Contains(body, `"optedIn":true`) ||
		!strings.Contains(body, "198.51.100.0/24") {
		t.Fatalf("PUT answers %d %s, want 200 with the stored list", code, body)
	}

	code, body = serveIPAllowAPI(t, srv, ipAPIRequest{
		method: http.MethodGet, path: allowlistPath, remote: inside, body: "",
	})
	if code != http.StatusOK || !strings.Contains(body, `"optedIn":true`) ||
		!strings.Contains(body, "198.51.100.0/24") {
		t.Fatalf("GET answers %d %s, want 200 with the stored list", code, body)
	}

	code, body = serveIPAllowAPI(t, srv, ipAPIRequest{
		method: http.MethodGet, path: switchesPath, remote: outside, body: "",
	})
	if code != http.StatusForbidden {
		t.Fatalf("outside address answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if strings.Contains(body, "198.51.100.0/24") {
		t.Fatalf("403 body %q leaks the list", body)
	}

	code, body = serveIPAllowAPI(t, srv, ipAPIRequest{
		method: http.MethodDelete, path: allowlistPath, remote: inside, body: "",
	})
	if code != http.StatusNoContent {
		t.Fatalf("DELETE answers %d, want %d: %s", code, http.StatusNoContent, body)
	}

	code, body = serveIPAllowAPI(t, srv, ipAPIRequest{
		method: http.MethodGet, path: switchesPath, remote: outside, body: "",
	})
	if code != http.StatusOK {
		t.Fatalf("outside address after DELETE answers %d, want %d: %s", code, http.StatusOK, body)
	}
}
