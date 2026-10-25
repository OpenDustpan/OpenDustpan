package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// postgresURLEnv names the variable with a PostgreSQL URL for the tests that
// read the store through serve. Without it, the PostgreSQL cases skip.
const postgresURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// keyBackend is one store that the key file tests run serve against.
type keyBackend struct {
	name string
	// database answers a --database value for a new, empty store in dir.
	database func(t *testing.T, dir string) string
	// seed stores one wrapped key with key in the store at database.
	seed func(t *testing.T, database string, key []byte)
}

// keyBackends answers SQLite, and PostgreSQL when DUSTPAN_TEST_POSTGRES_URL
// is set.
func keyBackends() []keyBackend {
	return []keyBackend{
		{
			name: "sqlite",
			database: func(_ *testing.T, dir string) string {
				return "sqlite:" + filepath.Join(dir, "dustpan.db")
			},
			seed: func(t *testing.T, database string, key []byte) {
				t.Helper()

				seedMainKey(t, strings.TrimPrefix(database, "sqlite:"), key)
			},
		},
		{
			name: "postgres",
			database: func(t *testing.T, _ string) string {
				t.Helper()

				base := os.Getenv(postgresURLEnv)
				if base == "" {
					t.Skipf("set %s to run the PostgreSQL case", postgresURLEnv)
				}

				return isolatedPostgresURL(t, base)
			},
			seed: func(t *testing.T, database string, key []byte) {
				t.Helper()

				st, err := postgres.Open(t.Context(), database)
				if err != nil {
					t.Fatalf("open postgres store: %v", err)
				}

				defer st.Close()

				seedWrappedKey(t, st, key)
			},
		},
	}
}

// isolatedPostgresURL makes a new schema in the database at base and answers
// a URL whose search_path is that schema, so each case starts empty.
func isolatedPostgresURL(t *testing.T, base string) string {
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
		t.Fatalf("parse %s: %v", postgresURLEnv, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// TestServeKeyFileStartsOnEmptyStore proves that serve starts on an empty
// store with a key file that the operator made, logs the path of the file,
// and never logs the key. Serve reads process environment, so the cases run
// one after the other.
func TestServeKeyFileStartsOnEmptyStore(t *testing.T) {
	for _, backend := range keyBackends() {
		t.Run(backend.name, func(t *testing.T) {
			t.Setenv(keyEnvVar, "")
			t.Setenv(keyFileEnvVar, "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

			dir := t.TempDir()
			keyPath := writeTestKeyFile(t, testKeyBase64()+"\n")

			// A PostgreSQL URL holds a password, and serve refuses a password in
			// a flag, so the database goes in the variable.
			t.Setenv("DUSTPAN_DATABASE", backend.database(t, dir))

			args := []string{"--key-encryption-key-file", keyPath, "--listen", "127.0.0.1:0"}

			logs, err := startServerLogs(t, args...)
			if err != nil {
				t.Fatalf("serve on an empty store: %v", err)
			}

			if !strings.Contains(logs, keyPath) {
				t.Fatalf("serve output lacks the key file path %q:\n%s", keyPath, logs)
			}

			if strings.Contains(logs, testKeyBase64()) {
				t.Fatal("serve output holds the key")
			}
		})
	}
}
