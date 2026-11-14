package dustpan_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// newBuiltinSQLiteServer opens a server over the built-in SQLite store at path,
// with the extra options after the options that NewServer requires. The caller
// closes the server.
func newBuiltinSQLiteServer(t *testing.T, path string, extra ...dustpan.Option) *dustpan.Server {
	t.Helper()

	opts := builtinServerOptions(t)
	opts = append(opts, dustpan.WithSQLite(path))
	opts = append(opts, extra...)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// builtinServerOptions returns the options that NewServer requires besides the
// store, with a blob store that the test closes.
func builtinServerOptions(t *testing.T) []dustpan.Option {
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

	return []dustpan.Option{
		dustpan.WithInstallation(installationName),
		dustpan.WithBlobStore(blobs),
		dustpan.WithAuthenticator(authn),
		dustpan.WithSecretWrapper(testWrapper(t)),
	}
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

	// The message names the accepted form sqlite:<path>, so it holds the word
	// sqlite by design. Only the refusal is checked for this value.
	t.Run("SQLite without a colon", func(t *testing.T) {
		t.Parallel()

		if _, err := dustpan.OpenStore(t.Context(), "sqlite"); !errors.Is(err, dustpan.ErrStoreDatabaseForm) {
			t.Fatalf("OpenStore(%q) = %v, want the form error", "sqlite", err)
		}
	})

	cases := []struct {
		name     string
		database string
	}{
		{name: "empty value", database: ""},
		{name: "other scheme", database: "mysql://x"},
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

	if repeatsValue(err.Error(), database) {
		t.Fatalf("OpenStore error = %v, want it to repeat no part of the value", err)
	}
}

// repeatsValue reports whether message repeats a part of value. A value with no
// character of its own has nothing to repeat. Every other value must be absent:
// the message holds the names of the accepted forms only.
func repeatsValue(message, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}

	return strings.Contains(message, value)
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

		// The file is absent from a directory that exists, so an open with a
		// migration makes it. Without the migration, the open fails and makes
		// nothing; the open of the same path below then succeeds.
		path := filepath.Join(t.TempDir(), "dustpan.db")

		_, err := dustpan.OpenStore(t.Context(), "sqlite:"+path, dustpan.WithoutStoreMigrate())
		if err == nil {
			t.Fatal("OpenStore succeeded with an absent database and without a migration, want an error")
		}

		checkOpenStoreOpens(t, "sqlite:"+path)
	})
}

// TestWithPostgreSQLRefusesAValueOfAnotherForm proves that WithPostgreSQL
// refuses each value that does not name a PostgreSQL store before it opens
// anything, and that the error repeats no part of the value. The cases need no
// PostgreSQL server: the check refuses before any connection.
func TestWithPostgreSQLRefusesAValueOfAnotherForm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		dsn  string
	}{
		{name: "empty value", dsn: ""},
		{name: "spaces", dsn: " "},
		{name: "keyword form", dsn: "host=localhost dbname=x"},
		{name: "other scheme", dsn: "mysql://x"},
		{name: "SQLite form", dsn: "sqlite:/tmp/x"},
		{name: "wrong case", dsn: "POSTGRES://x"},
		{name: "leading newline", dsn: "\npostgres://x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := dustpan.NewServer(dustpan.WithPostgreSQL(tc.dsn))
			if err == nil {
				t.Fatalf("NewServer(WithPostgreSQL(%q)) succeeded, want an error", tc.dsn)
			}

			if !errors.Is(err, dustpan.ErrStoreDatabaseForm) {
				t.Fatalf("NewServer error = %v, want the form error", err)
			}

			if repeatsValue(err.Error(), tc.dsn) {
				t.Fatalf("NewServer error = %v, want it to repeat no part of the value", err)
			}
		})
	}
}

// TestOpenBuiltinPostgresRefusesAValueOfAnotherForm proves that the outer
// layer refuses each value that does not name a PostgreSQL store before it
// opens anything, and that the error repeats no part of the value. The cases
// need no PostgreSQL server: the check refuses before any connection.
func TestOpenBuiltinPostgresRefusesAValueOfAnotherForm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		dsn  string
	}{
		{name: "empty value", dsn: ""},
		{name: "SQLite form", dsn: "sqlite:x"},
		{name: "other scheme", dsn: "mysql://h/db"},
		{name: "keyword form", dsn: "host=h dbname=d"},
		{name: "leading space", dsn: " postgres://h/db"},
		{name: "wrong case", dsn: "POSTGRES://h/db"},
		{name: "missing slash", dsn: "postgres:/h/db"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := dustpan.OpenBuiltinPostgresForTest(tc.dsn)
			if !errors.Is(err, dustpan.ErrStoreDatabaseForm) {
				t.Fatalf("openBuiltinPostgres(%q) = %v, want the form error", tc.dsn, err)
			}

			want := "WithPostgreSQL: " + dustpan.ErrStoreDatabaseForm.Error()
			if err.Error() != want {
				t.Fatalf("openBuiltinPostgres(%q) error = %q, want %q", tc.dsn, err.Error(), want)
			}
		})
	}
}

// TestOpenPostgresStoreRefusesAValueOfAnotherForm proves that the inner layer
// refuses such a value too, so that a new caller of the open cannot skip the
// check.
func TestOpenPostgresStoreRefusesAValueOfAnotherForm(t *testing.T) {
	t.Parallel()

	_, err := dustpan.OpenPostgresStoreForTest(t.Context(), "")
	if !errors.Is(err, dustpan.ErrStoreDatabaseForm) {
		t.Fatalf("openPostgresStore with an empty value = %v, want the form error", err)
	}
}

// TestBuiltinStoreOptionReplacesTheStoreOption proves that the store options of
// a built-in store apply after all other options: a namespace that the API
// creates lands in the built-in store, not in the store of WithNamespaceStore.
func TestBuiltinStoreOptionReplacesTheStoreOption(t *testing.T) {
	t.Parallel()

	second, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatalf("open the second store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("close the second store: %v", closeErr)
		}
	})

	srv := newBuiltinSQLiteServer(t, filepath.Join(t.TempDir(), "dustpan.db"), dustpan.WithNamespaceStore(second))

	t.Cleanup(func() {
		if closeErr := srv.Close(); closeErr != nil {
			t.Errorf("close the server: %v", closeErr)
		}
	})

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: nil, server: ts, srv: srv}

	status, body := serveServerAPI(t, f, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("create the namespace: status %d, body %s", status, body)
	}

	if _, readErr := second.GetNamespace(t.Context(), "acme"); !errors.Is(readErr, namespace.ErrNotFound) {
		t.Fatalf("read acme from the store of WithNamespaceStore = %v, want the built-in store to hold it", readErr)
	}

	// The switches of acme come from the store that the server reads, so a 200
	// proves that the built-in store holds the namespace.
	status, body = serveServerAPI(t, f, http.MethodGet, "/api/server/namespaces/acme/override-switches", "")
	if status != http.StatusOK {
		t.Fatalf("read the switches of acme: status %d, body %s", status, body)
	}
}

// TestServerCloseClosesTheBuiltinStore proves that Close closes the store that
// WithSQLite opened: the database file stays locked until then.
func TestServerCloseClosesTheBuiltinStore(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	srv := newBuiltinSQLiteServer(t, path)

	if _, err := dustpan.OpenStore(t.Context(), "sqlite:"+path); !errors.Is(err, sqlite.ErrInUse) {
		t.Fatalf("open the same database file while the server runs = %v, want the lock of the server", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("close the server: %v", err)
	}

	checkOpenStoreOpens(t, "sqlite:"+path)
}

// TestServerCloseLeavesTheCallerStoreOpen proves that Close closes the store
// that WithSQLite or WithPostgreSQL opened only: a store that the caller gave
// to WithBuiltinStore stays open and holds its data.
func TestServerCloseLeavesTheCallerStoreOpen(t *testing.T) {
	t.Parallel()

	st, err := dustpan.OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}

	opts := builtinServerOptions(t)
	opts = append(opts, dustpan.WithBuiltinStore(st))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if closeErr := srv.Close(); closeErr != nil {
		t.Fatalf("close the server: %v", closeErr)
	}

	if _, readErr := st.GetNamespace(t.Context(), "acme"); !errors.Is(readErr, namespace.ErrNotFound) {
		t.Fatalf("read through the store after Close = %v, want the store to answer", readErr)
	}

	if closeErr := st.Close(); closeErr != nil {
		t.Fatalf("close the store: %v", closeErr)
	}
}

// TestNewServerClosesTheBuiltinStoreWhenItFails proves that NewServer closes
// the store that it opened when a later check fails, and that the returned
// error carries that failure. The absent authenticator fails the check after
// applyBuiltinStore opened the store; a second open of the same path then shows
// that the server released it.
func TestNewServerClosesTheBuiltinStoreWhenItFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	_, err := dustpan.NewServer(dustpan.WithSQLite(path))
	if err == nil {
		t.Fatal("NewServer succeeded without an authenticator, want an error")
	}

	if !errors.Is(err, dustpan.ErrMissingOption) || !strings.Contains(err.Error(), "WithAuthenticator") {
		t.Fatalf("NewServer error = %v, want the absent authenticator", err)
	}

	checkOpenStoreOpens(t, "sqlite:"+path)
}

// TestWithPostgreSQLOpensTheTestDatabase proves that WithPostgreSQL opens the
// store of the test database and sets its store options, so that
// postgresStoreOptions runs. The test skips without the test database.
func TestWithPostgreSQLOpensTheTestDatabase(t *testing.T) {
	t.Parallel()

	base := os.Getenv(postgresURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL case", postgresURLEnv)
	}

	opts := builtinServerOptions(t)
	opts = append(opts, dustpan.WithPostgreSQL(isolatedPostgresURL(t, base)))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if closeErr := srv.Close(); closeErr != nil {
		t.Fatalf("close the server: %v", closeErr)
	}
}
