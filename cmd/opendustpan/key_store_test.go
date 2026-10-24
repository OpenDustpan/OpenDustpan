package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// keyMakeMode is one way that serve makes a key file: the terminal yes or
// the opt-in.
type keyMakeMode struct {
	name     string
	stdin    string
	terminal bool
	optIn    bool
}

func keyMakeModes() []keyMakeMode {
	return []keyMakeMode{
		{name: "terminal yes", stdin: "yes\n", terminal: true, optIn: false},
		{name: "opt-in", stdin: "", terminal: false, optIn: true},
	}
}

// TestServeKeyRefusesNewFileOverWrappedKeys proves that serve makes no key
// file when the store already holds wrapped keys and no key file exists
// (issue 355): the terminal yes and the opt-in both stop with
// errKeyFileMissing, the error names the key file path and the setting that
// helps, and no file exists after the stop. Serve reads process environment,
// so the cases run one after the other.
//
//nolint:paralleltest // serve reads process environment, so the cases set it and run alone.
func TestServeKeyRefusesNewFileOverWrappedKeys(t *testing.T) {
	for _, backend := range keyBackends() {
		for _, mode := range keyMakeModes() {
			t.Run(backend.name+"/"+mode.name, func(t *testing.T) {
				checkRefusesNewKeyFile(t, backend, mode)
			})
		}
	}
}

// checkRefusesNewKeyFile runs one case of
// TestServeKeyRefusesNewFileOverWrappedKeys.
func checkRefusesNewKeyFile(t *testing.T, backend keyBackend, mode keyMakeMode) {
	t.Helper()

	clearKeyEnv(t)

	if mode.optIn {
		t.Setenv(generateKeyEnvVar, "true")
	}

	dir := t.TempDir()
	database := backend.database(t, dir)
	keyPath := filepath.Join(dir, "dustpan.key")

	backend.seed(t, database, []byte(otherKeyString))

	// A PostgreSQL URL holds a password, and serve refuses a password in a
	// flag, so the database goes in the variable.
	t.Setenv("DUSTPAN_DATABASE", database)

	var logs lockedBuffer

	err := serveWithIO(
		t.Context(),
		[]string{"--key-encryption-key-file", keyPath},
		strings.NewReader(mode.stdin),
		&logs,
		mode.terminal,
	)
	if err == nil || !errors.Is(err, errKeyFileMissing) {
		t.Fatalf("serve over wrapped keys: err = %v, want errKeyFileMissing", err)
	}

	for _, want := range []string{keyPath, keyEnvVar, "restore the original key file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("serve error = %q, want it to name %q", err, want)
		}
	}

	if _, statErr := os.Stat(keyPath); !os.IsNotExist(statErr) {
		t.Fatalf("serve made a key file over wrapped keys (stat err = %v), want no file", statErr)
	}
}

// TestServeKeyMakesFileOnEmptyStore proves that a store with no wrapped keys
// keeps the behavior of #344: the terminal yes and the opt-in make an
// owner-only key file and the server starts. Serve reads process
// environment, so the cases run one after the other.
//
//nolint:paralleltest // serve reads process environment, so the cases set it and run alone.
func TestServeKeyMakesFileOnEmptyStore(t *testing.T) {
	for _, backend := range keyBackends() {
		for _, mode := range keyMakeModes() {
			t.Run(backend.name+"/"+mode.name, func(t *testing.T) {
				checkMakesKeyFile(t, backend, mode)
			})
		}
	}
}

// checkMakesKeyFile runs one case of TestServeKeyMakesFileOnEmptyStore.
func checkMakesKeyFile(t *testing.T, backend keyBackend, mode keyMakeMode) {
	t.Helper()

	clearKeyEnv(t)

	if mode.optIn {
		t.Setenv(generateKeyEnvVar, "true")
	}

	dir := t.TempDir()
	database := backend.database(t, dir)
	keyPath := filepath.Join(dir, "dustpan.key")

	// A PostgreSQL URL holds a password, and serve refuses a password in a
	// flag, so the database goes in the variable.
	t.Setenv("DUSTPAN_DATABASE", database)

	var logs lockedBuffer

	args := []string{"--key-encryption-key-file", keyPath, "--listen", "127.0.0.1:0"}

	if err := startServerWithIO(t, mode.stdin, mode.terminal, &logs, args...); err != nil {
		t.Fatalf("serve on an empty store: %v", err)
	}

	checkMadeKeyFile(t, keyPath)
}
