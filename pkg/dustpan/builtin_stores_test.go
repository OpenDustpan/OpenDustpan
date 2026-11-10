package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
)

// newBuiltinSQLiteServer opens a server over the built-in SQLite store at path,
// with only the non-store options that NewServer requires. The caller closes
// the server.
func newBuiltinSQLiteServer(t *testing.T, path string) *dustpan.Server {
	t.Helper()

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

	srv, err := dustpan.NewServer(
		dustpan.WithSQLite(path),
		dustpan.WithInstallation(installationName),
		dustpan.WithBlobStore(blobs),
		dustpan.WithAuthenticator(authn),
		dustpan.WithSecretWrapper(testWrapper(t)),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// newBuiltinSQLiteFixture starts a server whose only store option is WithSQLite.
func newBuiltinSQLiteFixture(t *testing.T) fixture {
	t.Helper()

	srv := newBuiltinSQLiteServer(t, filepath.Join(t.TempDir(), "dustpan.db"))

	t.Cleanup(func() {
		if closeErr := srv.Close(); closeErr != nil {
			t.Errorf("close server: %v", closeErr)
		}
	})

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return fixture{store: nil, server: ts, srv: srv}
}

// TestWithSQLiteAlone proves that WithSQLite alone sets every store option:
// one Pulumi stack update and one OpenTofu state write go through the HTTP API
// and read back.
func TestWithSQLiteAlone(t *testing.T) {
	t.Parallel()

	f := newBuiltinSQLiteFixture(t)

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

	savePulumiCheckpoint(t, f)

	status, body = f.do(t, http.MethodGet, "/api/stacks/acme/web/prod/export", adminToken, "")
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

// TestBuiltinStoreOpenError proves that NewServer names the built-in store
// option that fails to open.
func TestBuiltinStoreOpenError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		option dustpan.Option
		want   string
	}{
		{
			name:   "SQLite path in a missing directory",
			option: dustpan.WithSQLite(filepath.Join(t.TempDir(), "missing", "dustpan.db")),
			want:   "WithSQLite",
		},
		{
			name:   "PostgreSQL DSN that does not parse",
			option: dustpan.WithPostgreSQL("postgres://localhost:not-a-port/dustpan"),
			want:   "WithPostgreSQL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := dustpan.NewServer(tc.option)
			if err == nil {
				t.Fatal("NewServer succeeded, want an error")
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewServer error = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// TestServerCloseTwice proves that Close closes the built-in store once and
// answers nil on the second call.
func TestServerCloseTwice(t *testing.T) {
	t.Parallel()

	srv := newBuiltinSQLiteServer(t, filepath.Join(t.TempDir(), "dustpan.db"))

	if err := srv.Close(); err != nil {
		t.Fatalf("close the server: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("close the server a second time: %v", err)
	}
}
