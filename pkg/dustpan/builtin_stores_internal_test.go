package dustpan

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/pgtest"
)

// TestOpenStoreWiresTheJobClaimer proves that the built-in SQLite store sets
// the job store and the job claimer. Without the claimer, a poll of a runner
// of a pool for jobs finds no work.
func TestOpenStoreWiresTheJobClaimer(t *testing.T) {
	t.Parallel()

	st, err := OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	WithBuiltinStore(st)(cfg)

	if applyErr := applyBuiltinStore(cfg); applyErr != nil {
		t.Fatalf("apply the built-in store: %v", applyErr)
	}

	if cfg.jobStore == nil || cfg.jobClaimer == nil {
		t.Fatalf("job store = %v, job claimer = %v; want both", cfg.jobStore, cfg.jobClaimer)
	}
}

// TestBuiltinStoresWireTheJobTokenStore proves that each built-in store sets
// the job token store. Without it the binary issues no job token: the
// credentials route of a launcher session answers not found, and the
// authenticator chain has no job token gate.
func TestBuiltinStoresWireTheJobTokenStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		open func(t *testing.T) *BuiltinStore
	}{
		{name: "sqlite", open: openSQLiteForJobTokens},
		{name: "postgres", open: openPostgresForJobTokens},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkJobTokenStoreWired(t, tc.open(t))
		})
	}
}

// checkJobTokenStoreWired applies the options of st and checks the job token
// store. It closes st at the end of the test.
func checkJobTokenStoreWired(t *testing.T, st *BuiltinStore) {
	t.Helper()

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	WithBuiltinStore(st)(cfg)

	if applyErr := applyBuiltinStore(cfg); applyErr != nil {
		t.Fatalf("apply the built-in store: %v", applyErr)
	}

	if cfg.jobTokenStore == nil {
		t.Fatal("the job token store is nil; want the built-in store to set it")
	}
}

// openSQLiteForJobTokens opens a SQLite built-in store in a new directory.
func openSQLiteForJobTokens(t *testing.T) *BuiltinStore {
	t.Helper()

	st, err := OpenStore(t.Context(), "sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	return st
}

// openPostgresForJobTokens opens a PostgreSQL built-in store in a new schema.
// It skips the test when the test database is not set.
func openPostgresForJobTokens(t *testing.T) *BuiltinStore {
	t.Helper()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL case")
	}

	st, err := OpenStore(t.Context(), freshPostgresSchemaURL(t, base))
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	return st
}

// freshPostgresSchemaURL returns the URL of a new schema of the database at
// base, so that the case starts empty.
func freshPostgresSchemaURL(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	pgtest.DropSchemaAtCleanup(t, base, name)

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse the database URL: %v", err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}
