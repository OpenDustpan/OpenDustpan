package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/localuser"
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

// TestOpenStore proves that OpenStore opens a SQLite path and refuses every
// other form without repeating the value.
func TestOpenStore(t *testing.T) {
	t.Parallel()

	database := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")
	password := "fake-password"
	postgresURL := fakecred.URL("postgres", "user", password, "127.0.0.1:1", "dustpan")

	t.Run("SQLite path", func(t *testing.T) {
		t.Parallel()

		checkOpenStoreOpens(t, database)
	})

	cases := []struct {
		name     string
		database string
	}{
		{name: "empty value", database: ""},
		{name: "other scheme", database: "mysql://x"},
		{name: "SQLite without a colon", database: "sqlite"},
		{name: "PostgreSQL on a closed port", database: postgresURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkOpenStoreRefused(t, tc.database)
		})
	}
}

// TestOpenStoreKeepsEveryPortOfDev proves that the handle of an open SQLite
// store satisfies every port that the dev command asserts on it.
func TestOpenStoreKeepsEveryPortOfDev(t *testing.T) {
	t.Parallel()

	st, err := dustpan.OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the store: %v", closeErr)
		}
	})

	var handle any = st

	cases := []struct {
		name  string
		holds func(any) bool
	}{
		{
			name:  "collection.Store",
			holds: func(value any) bool { _, ok := value.(collection.Store); return ok },
		},
		{
			name:  "collection.PlacementStore",
			holds: func(value any) bool { _, ok := value.(collection.PlacementStore); return ok },
		},
		{
			name:  "localuser.PasswordStore",
			holds: func(value any) bool { _, ok := value.(localuser.PasswordStore); return ok },
		},
		{
			name:  "localusersvc.DemoGrants",
			holds: func(value any) bool { _, ok := value.(localusersvc.DemoGrants); return ok },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if !tc.holds(handle) {
				t.Fatalf("the built-in store does not keep %s", tc.name)
			}
		})
	}
}

// checkOpenStoreOpens proves that database opens and closes.
func checkOpenStoreOpens(t *testing.T, database string) {
	t.Helper()

	st, err := dustpan.OpenStore(t.Context(), database)
	if err != nil {
		t.Fatalf("OpenStore(%q) = %v, want an open store", database, err)
	}

	if closeErr := st.Close(); closeErr != nil {
		t.Fatalf("close the store: %v", closeErr)
	}
}

// checkOpenStoreRefused proves that database fails without a repeated value.
func checkOpenStoreRefused(t *testing.T, database string) {
	t.Helper()

	_, err := dustpan.OpenStore(t.Context(), database)
	if err == nil {
		t.Fatalf("OpenStore(%q) succeeded, want an error", database)
	}

	if database != "" && strings.Contains(err.Error(), database) {
		t.Fatalf("OpenStore error = %v, want it to repeat no part of the value", err)
	}
}

// TestWithBuiltinStoreNil proves that NewServer refuses a nil store and names
// the option.
func TestWithBuiltinStoreNil(t *testing.T) {
	t.Parallel()

	_, err := dustpan.NewServer(dustpan.WithBuiltinStore(nil))
	if err == nil {
		t.Fatal("NewServer succeeded, want an error")
	}

	if !strings.Contains(err.Error(), "WithBuiltinStore") {
		t.Fatalf("NewServer error = %v, want it to name WithBuiltinStore", err)
	}
}

// TestBuiltinStoreCloseTwice proves that Close answers nil on the second call.
func TestBuiltinStoreCloseTwice(t *testing.T) {
	t.Parallel()

	st, err := dustpan.OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}

	if closeErr := st.Close(); closeErr != nil {
		t.Fatalf("close the store: %v", closeErr)
	}

	if closeErr := st.Close(); closeErr != nil {
		t.Fatalf("close the store a second time: %v", closeErr)
	}
}

// TestOpenStoreOptionsReachTheAdapter proves that the binary version and the
// migration switch reach the adapter.
func TestOpenStoreOptionsReachTheAdapter(t *testing.T) {
	t.Parallel()

	t.Run("binary version", func(t *testing.T) {
		t.Parallel()

		_, err := dustpan.OpenStore(
			t.Context(),
			"sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"),
			dustpan.WithStoreBinaryVersion("v0.0.0"),
		)
		if err == nil {
			t.Fatal("OpenStore succeeded with a binary version below the schema minimum, want an error")
		}
	})

	t.Run("without migrate", func(t *testing.T) {
		t.Parallel()

		_, err := dustpan.OpenStore(
			t.Context(),
			"sqlite:"+filepath.Join(t.TempDir(), "missing", "dustpan.db"),
			dustpan.WithoutStoreMigrate(),
		)
		if err == nil {
			t.Fatal("OpenStore succeeded without a database and without a migration, want an error")
		}
	})
}
