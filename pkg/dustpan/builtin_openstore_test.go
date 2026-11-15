package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// prepareAcmeNamespace creates the acme namespace with its default collection
// and the placement rules of both tools.
func prepareAcmeNamespace(t *testing.T, f fixture) {
	t.Helper()

	status, body := serveServerAPI(t, f, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("create namespace: status %d, body %s", status, body)
	}

	mustDo(t, f, call{
		method: http.MethodPost, path: "/api/dustpan/v1/namespaces/acme/collections", body: `{"name":"default"}`,
	}, http.StatusCreated)

	for _, tool := range []string{"pulumi", "opentofu"} {
		mustDo(t, f, call{
			method: http.MethodPost, path: "/api/dustpan/v1/namespaces/acme/placement-rules",
			body: `{"tool":"` + tool + `","project":"*","stack":"*","collection":"default"}`,
		}, http.StatusCreated)
	}
}

// TestOpenStoreAlone proves that OpenStore with WithBuiltinStore alone sets
// every store option: one Pulumi stack update and one OpenTofu state write go
// through the HTTP API and read back, with no other store option.
func TestOpenStoreAlone(t *testing.T) {
	t.Parallel()

	store, err := dustpan.OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close the store: %v", closeErr)
		}
	})

	opts := builtinServerOptions(t)
	opts = append(opts, dustpan.WithBuiltinStore(store))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := srv.Close(); closeErr != nil {
			t.Errorf("close server: %v", closeErr)
		}
	})

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: nil, server: ts, srv: srv}

	prepareAcmeNamespace(t, f)

	savePulumiCheckpoint(t, f)

	status, body := f.do(t, http.MethodGet, "/api/stacks/acme/web/prod/export", adminToken, "")
	if status != http.StatusOK || !strings.Contains(body, `"version":3`) {
		t.Fatalf("export the stack: status %d, body %s", status, body)
	}

	const state = `{"version":4,"serial":1,"lineage":"l1","outputs":{},"resources":[]}`

	status, body = tofuDoPath(t, f, "LOCK", "/tofu/state/acme/web", `{"ID":"probe"}`)
	if status != http.StatusOK {
		t.Fatalf("lock the workspace: status %d, body %s", status, body)
	}

	status, body = tofuDoPath(t, f, http.MethodPost, "/tofu/state/acme/web?ID=probe", state)
	if status != http.StatusOK {
		t.Fatalf("write the state: status %d, body %s", status, body)
	}

	status, body = tofuDoPath(t, f, http.MethodGet, "/tofu/state/acme/web", "")
	if status != http.StatusOK || body != state {
		t.Fatalf("read the state: status %d, body %s", status, body)
	}
}
