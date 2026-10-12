package main

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The test computes the MD5 values the manifest records.
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// testKeyString is a fixed 32-byte key-encryption key for these tests.
const testKeyString = "0123456789abcdef0123456789abcdef"

// otherKeyString is another fixed 32-byte key, for the mismatch tests.
const otherKeyString = "fedcba9876543210fedcba9876543210"

// adminTestToken authenticates serve in these tests.
const adminTestToken = "test-token-0123456789abcdef0123456789"

const (
	// serveStartTimeout bounds how long a start test waits for the
	// listening log line.
	serveStartTimeout = 10 * time.Second
	// serveStartPoll sets how often a start test looks for it.
	serveStartPoll = 10 * time.Millisecond
)

func testKeyBase64() string {
	return base64.StdEncoding.EncodeToString([]byte(testKeyString))
}

// loadWith runs the loader against env instead of the process environment.
func loadWith(env map[string]string) ([]byte, error) {
	return loadKeyEncryptionKeyFrom(func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	})
}

// writeTestKeyFile writes content to an owner-only key file and returns its path.
func writeTestKeyFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "key")

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	return path
}

// assertLoad proves that loading env answers wantKey, or fails naming the
// setting and wrapping wantSentinel when wantKey is nil.
func assertLoad(t *testing.T, env map[string]string, wantKey []byte, wantSentinel error) {
	t.Helper()

	key, err := loadWith(env)

	if wantKey != nil {
		if err != nil {
			t.Fatalf("load key: %v", err)
		}

		if !bytes.Equal(key, wantKey) {
			t.Fatalf("loaded key = %x, want %x", key, wantKey)
		}

		return
	}

	if err == nil || !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("load key: err = %v, want it to name the setting", err)
	}

	if wantSentinel != nil && !errors.Is(err, wantSentinel) {
		t.Fatalf("load key: err = %v, want %v", err, wantSentinel)
	}
}

func TestLoadKeyEncryptionKey(t *testing.T) {
	t.Parallel()

	t.Run("unset returns no key", func(t *testing.T) {
		t.Parallel()

		key, err := loadWith(map[string]string{})
		if err != nil || key != nil {
			t.Fatalf("load unset key: key = %x, err = %v, want nil, nil", key, err)
		}
	})

	t.Run("direct value returns the key", func(t *testing.T) {
		t.Parallel()

		assertLoad(t, map[string]string{keyEnvVar: testKeyBase64()}, []byte(testKeyString), nil)
	})

	t.Run("bad base64 is refused", func(t *testing.T) {
		t.Parallel()

		assertLoad(t, map[string]string{keyEnvVar: "not-base64!!!"}, nil, nil)
	})

	t.Run("wrong size is refused", func(t *testing.T) {
		t.Parallel()

		short := base64.StdEncoding.EncodeToString([]byte("short"))
		assertLoad(t, map[string]string{keyEnvVar: short}, nil, secret.ErrInvalidKey)
	})

	t.Run("key file returns the key", func(t *testing.T) {
		t.Parallel()

		path := writeTestKeyFile(t, testKeyBase64()+"\n")
		assertLoad(t, map[string]string{keyFileEnvVar: path}, []byte(testKeyString), nil)
	})

	t.Run("group-readable key file is refused", func(t *testing.T) {
		t.Parallel()

		path := writeTestKeyFile(t, testKeyBase64())

		//nolint:gosec // The test proves a group-readable key file is refused.
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod key file: %v", err)
		}

		assertLoad(t, map[string]string{keyFileEnvVar: path}, nil, errKeyFilePermissions)
	})

	t.Run("missing key file is refused", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "missing")
		assertLoad(t, map[string]string{keyFileEnvVar: missing}, nil, nil)
	})

	t.Run("bad base64 in the key file is refused", func(t *testing.T) {
		t.Parallel()

		path := writeTestKeyFile(t, "not-base64!!!")
		assertLoad(t, map[string]string{keyFileEnvVar: path}, nil, nil)
	})

	t.Run("direct value wins over the key file", func(t *testing.T) {
		t.Parallel()

		fileKey := base64.StdEncoding.EncodeToString([]byte(otherKeyString))
		path := writeTestKeyFile(t, fileKey)

		assertLoad(
			t,
			map[string]string{keyEnvVar: testKeyBase64(), keyFileEnvVar: path},
			[]byte(testKeyString),
			nil,
		)
	})
}

func TestLoadKeyEncryptionKeyClearsVariable(t *testing.T) {
	t.Setenv(keyEnvVar, testKeyBase64())

	key, err := loadKeyEncryptionKey()
	if err != nil {
		t.Fatalf("load key: %v", err)
	}

	if !bytes.Equal(key, []byte(testKeyString)) {
		t.Fatalf("loaded key = %x, want %x", key, []byte(testKeyString))
	}

	if _, ok := os.LookupEnv(keyEnvVar); ok {
		t.Fatalf("load kept %s in the process environment", keyEnvVar)
	}
}

// setServeEnv sets the admin token and the key-encryption key for a serve
// test: an empty key means no key is configured.
func setServeEnv(t *testing.T, keyBase64 string) {
	t.Helper()

	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

	if keyBase64 == "" {
		if err := os.Unsetenv(keyEnvVar); err != nil {
			t.Fatalf("unset %s: %v", keyEnvVar, err)
		}

		if err := os.Unsetenv(keyFileEnvVar); err != nil {
			t.Fatalf("unset %s: %v", keyFileEnvVar, err)
		}

		return
	}

	t.Setenv(keyEnvVar, keyBase64)
}

// seedMainKey stores one wrapped key with key in the SQLite database at path.
func seedMainKey(t *testing.T, path string, key []byte) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	}()

	err = st.CreateNamespace(t.Context(), namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		t.Fatalf("new wrapper: %v", err)
	}

	svc, err := secret.NewService(secret.Config{
		Keys:          st,
		Wrapper:       wrapper,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	sc, err := secret.NewScope("acme", "stk-mainseed00000000001")
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	if _, err := svc.Encrypt(t.Context(), sc, [][]byte{[]byte("s3cr3t")}); err != nil {
		t.Fatalf("seed wrapped key: %v", err)
	}
}

// serveDatabase runs serve against the SQLite database at path and reports
// its error. Refusals answer before the server starts; a started server
// would block, so only refusal cases use this.
func serveDatabase(t *testing.T, path string) error {
	t.Helper()

	return serve(t.Context(), []string{"--database", "sqlite:" + path}, io.Discard)
}

// startServer runs serve with args until it logs "listening", then ends it. A
// nil error means the start-up checks passed and the server ran.
func startServer(t *testing.T, args ...string) error {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var logs lockedBuffer

	done := make(chan error, 1)

	go func() {
		done <- serve(ctx, args, &logs)
	}()

	ticker := time.NewTicker(serveStartPoll)
	defer ticker.Stop()

	timeout := time.NewTimer(serveStartTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-ticker.C:
			if strings.Contains(logs.String(), "listening") {
				cancel()

				return <-done
			}
		case <-timeout.C:
			t.Fatal("server did not start in time")
		}
	}
}

// lockedBuffer is a goroutine-safe log sink for the serve start tests: serve
// logs from the serving goroutine while the test watches for the line.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log buffer: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
func TestServeKeyStartup(t *testing.T) {
	refuseWithoutKey(t)
	refuseAnotherKey(t)
	refuseHalfRotatedKey(t)
	startWithMatchingKey(t)
	startWithoutKeyOnEmptyDatabase(t)
}

func refuseWithoutKey(t *testing.T) {
	t.Helper()

	setServeEnv(t, "")

	path := filepath.Join(t.TempDir(), "dustpan.db")
	seedMainKey(t, path, []byte(testKeyString))

	err := serveDatabase(t, path)
	if err == nil || !strings.Contains(err.Error(), keyEnvVar) {
		t.Fatalf("serve without a key: err = %v, want it to name %s", err, keyEnvVar)
	}
}

func refuseAnotherKey(t *testing.T) {
	t.Helper()

	setServeEnv(t, base64.StdEncoding.EncodeToString([]byte(otherKeyString)))

	path := filepath.Join(t.TempDir(), "dustpan.db")
	seedMainKey(t, path, []byte(testKeyString))

	err := serveDatabase(t, path)
	if err == nil || !strings.Contains(err.Error(), keyEnvVar) {
		t.Fatalf("serve with another key: err = %v, want it to name %s", err, keyEnvVar)
	}

	if _, ok := os.LookupEnv(keyEnvVar); ok {
		t.Fatalf("serve kept %s in the process environment", keyEnvVar)
	}
}

// seedSecondKey stores one wrapped key with key in a second scope of the
// SQLite database at path. The namespace already exists, so it creates no
// namespace.
func seedSecondKey(t *testing.T, path string, key []byte) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	}()

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		t.Fatalf("new wrapper: %v", err)
	}

	svc, err := secret.NewService(secret.Config{
		Keys:          st,
		Wrapper:       wrapper,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	sc, err := secret.NewScope("acme", "stk-mainseed00000000002")
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	if _, err := svc.Encrypt(t.Context(), sc, [][]byte{[]byte("other")}); err != nil {
		t.Fatalf("seed second wrapped key: %v", err)
	}
}

// refuseHalfRotatedKey proves serve refuses a database that holds wrapped
// keys under more than one key-encryption key, and names the missing rewrap
// command (ADR 23).
func refuseHalfRotatedKey(t *testing.T) {
	t.Helper()

	setServeEnv(t, testKeyBase64())

	path := filepath.Join(t.TempDir(), "dustpan.db")
	seedMainKey(t, path, []byte(testKeyString))
	seedSecondKey(t, path, []byte(otherKeyString))

	err := serveDatabase(t, path)
	if err == nil || !errors.Is(err, secret.ErrWrapperMismatch) {
		t.Fatalf("serve a half-rotated database: err = %v, want ErrWrapperMismatch", err)
	}

	if !strings.Contains(err.Error(), "rewrap") {
		t.Fatalf("serve a half-rotated database: err = %v, want it to name the rewrap command", err)
	}
}

func startWithMatchingKey(t *testing.T) {
	t.Helper()

	setServeEnv(t, testKeyBase64())

	path := filepath.Join(t.TempDir(), "dustpan.db")
	seedMainKey(t, path, []byte(testKeyString))

	err := startServer(t, "--database", "sqlite:"+path, "--listen", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve with the matching key: %v", err)
	}
}

func startWithoutKeyOnEmptyDatabase(t *testing.T) {
	t.Helper()

	setServeEnv(t, "")

	err := startServer(
		t,
		"--database",
		"sqlite:"+filepath.Join(t.TempDir(), "dustpan.db"),
		"--listen",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf("serve without a key on an empty database: %v", err)
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
func TestServeTLSAndPublicURLFlags(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	database := "--database"
	path := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "certificate without a key",
			args: []string{database, path, "--tls-cert", "cert.pem"},
			want: "DUSTPAN_TLS_KEY",
		},
		{
			name: "key without a certificate",
			args: []string{database, path, "--tls-key", "key.pem"},
			want: "DUSTPAN_TLS_CERT",
		},
		{
			name: "bad public URL",
			args: []string{database, path, "--public-url", "not a url"},
			want: "public URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := serve(t.Context(), tc.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("serve %s: err = %v, want it to name %s", tc.name, err, tc.want)
			}
		})
	}
}

// newTestFlagSet registers serve flags on a new set, so tests read defaults.
func newTestFlagSet(f *serveFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	f.register(fs)

	return fs
}

//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
func TestServeLogFlags(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	database := "--database"
	path := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "bad log level",
			args: []string{database, path, "--log-level", "verbose"},
			want: "--log-level",
		},
		{
			name: "bad log format",
			args: []string{database, path, "--log-format", "yaml"},
			want: "--log-format",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := serve(t.Context(), tc.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("serve %s: err = %v, want it to name %s", tc.name, err, tc.want)
			}
		})
	}
}

func TestServeLogFlagDefaults(t *testing.T) {
	t.Parallel()

	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if f.logLevel != "info" {
		t.Fatalf("log level = %q, want info", f.logLevel)
	}

	if f.logFormat != "json" {
		t.Fatalf("log format = %q, want json", f.logFormat)
	}

	if err := checkLogFlags(f.logLevel, f.logFormat); err != nil {
		t.Fatalf("default log flags: %v", err)
	}

	f.logLevel = "debug"
	f.logFormat = "text"

	if err := checkLogFlags(f.logLevel, f.logFormat); err != nil {
		t.Fatalf("debug/text log flags: %v", err)
	}

	logger, err := f.loggerFor(io.Discard)
	if err != nil || logger == nil {
		t.Fatalf("logger for debug/text: logger %v, err %v", logger, err)
	}
}

func TestServeSizeFlagDefaults(t *testing.T) {
	t.Parallel()

	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if f.maxCheckpointBytes != 0 || f.maxSecretBatchBytes != 0 || f.maxStateBytes != 0 || f.maxBodyBytesInFlight != 0 {
		t.Fatalf(
			"size flags = %d %d %d %d, want all zero for domain defaults",
			f.maxCheckpointBytes, f.maxSecretBatchBytes, f.maxStateBytes, f.maxBodyBytesInFlight,
		)
	}

	sizeLimits := []int64{
		f.maxCheckpointBytes,
		f.maxSecretBatchBytes,
		f.maxStateBytes,
		f.maxBodyBytesInFlight,
	}

	if err := checkSizeFlags(sizeLimits); err != nil {
		t.Fatalf("default size flags: %v", err)
	}

	if err := checkSizeFlags([]int64{1, 1, 1, 1}); err != nil {
		t.Fatalf("positive size flags: %v", err)
	}

	if err := checkSizeFlags([]int64{-1, 0, 0, 0}); err == nil {
		t.Fatal("negative size flag succeeded")
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	if err := run(t.Context(), []string{"--version"}, &out, io.Discard); err != nil {
		t.Fatalf("run --version: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != version {
		t.Fatalf("--version printed %q, want %q", got, version)
	}

	if !strings.Contains(errUsage.Error(), "--version") {
		t.Fatalf("usage = %q, want it to keep --version", errUsage.Error())
	}
}

func TestDefaultBlobDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, database, flag, want string
	}{
		{"explicit flag wins", "sqlite:/data/dustpan.db", "/custom/blobs", "/custom/blobs"},
		{"sqlite next to file", "sqlite:/data/dustpan.db", "", "/data/blobs"},
		{"sqlite bare filename", "sqlite:dustpan.db", "", "blobs"},
		{"postgres uses working directory", "postgres://user@localhost/db", "", "blobs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := defaultBlobDir(tc.database, tc.flag); got != tc.want {
				t.Fatalf("defaultBlobDir(%q, %q) = %q, want %q", tc.database, tc.flag, got, tc.want)
			}
		})
	}
}

// TestServeBlobDir sets the process environment, so it runs alone.
func TestServeBlobDir(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	if err := os.Unsetenv("DUSTPAN_BLOB_DIR"); err != nil {
		t.Fatalf("unset DUSTPAN_BLOB_DIR: %v", err)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")

	if err := startServer(t, "--database", "sqlite:"+dbPath, "--listen", "127.0.0.1:0"); err != nil {
		t.Fatalf("serve with default blob directory: %v", err)
	}

	checkBlobDir(t, filepath.Join(dir, "blobs"))

	// serve clears DUSTPAN_ADMIN_TOKEN after the read, so the second start
	// in this process needs it again.
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

	custom := filepath.Join(dir, "custom-blobs")
	customArgs := []string{
		"--database", "sqlite:" + dbPath,
		"--listen", "127.0.0.1:0",
		"--blob-dir", custom,
	}

	if serveErr := startServer(t, customArgs...); serveErr != nil {
		t.Fatalf("serve with --blob-dir: %v", serveErr)
	}

	checkBlobDir(t, custom)
}

// checkBlobDir proves the server created dir with owner-only permissions.
func checkBlobDir(t *testing.T, dir string) {
	t.Helper()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat blob directory %s: %v", dir, err)
	}

	if !info.IsDir() {
		t.Fatalf("blob directory %s is not a directory", dir)
	}

	if perm := info.Mode().Perm(); perm != blobDirPerm {
		t.Fatalf("blob directory %s mode %04o, want %04o", dir, perm, blobDirPerm)
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestNewerSchemaRefusesCommands(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	path, dbVersion, binVersion := newerSchemaDatabase(t)

	if err := serveDatabase(t, path); !isNewerSchemaError(t, err, dbVersion, binVersion) {
		t.Fatalf("serve newer database: err = %v, want it to name %d, %d and %q", err, dbVersion, binVersion, version)
	}
}

// isNewerSchemaError reports whether err wraps ErrSchemaNewer and names the
// schema version of the database, the schema version of the binary, the
// version of the binary and the one action.
func isNewerSchemaError(t *testing.T, err error, dbVersion, binVersion int64) bool {
	t.Helper()

	if err == nil || !errors.Is(err, schema.ErrSchemaNewer) {
		return false
	}

	msg := err.Error()

	for _, want := range []string{
		strconv.FormatInt(dbVersion, 10),
		strconv.FormatInt(binVersion, 10),
		version,
		"start a newer binary or restore a backup",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %q", msg, want)

			return false
		}
	}

	return true
}

// newerSchemaDatabase creates a SQLite database at the embedded migrations
// plus one future version row, and returns its path with the database and
// binary schema versions.
//
//nolint:nonamedreturns // The names keep the database and binary versions in order at callers.
func newerSchemaDatabase(t *testing.T) (path string, dbVersion, binVersion int64) {
	t.Helper()

	path = filepath.Join(t.TempDir(), "dustpan.db")

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	conn, connErr := sql.Open("sqlite", "file:"+path)
	if connErr != nil {
		t.Fatalf("open raw database: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	row := conn.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`)
	if scanErr := row.Scan(&binVersion); scanErr != nil {
		t.Fatalf("read binary schema version: %v", scanErr)
	}

	dbVersion = binVersion + 1

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	name := fmt.Sprintf("%04d_future.sql", dbVersion)

	if _, execErr := conn.ExecContext(t.Context(), record, dbVersion, name); execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}

	return path, dbVersion, binVersion
}

func TestBackupTargetFlagPrecedence(t *testing.T) {
	if err := os.Unsetenv("DUSTPAN_BACKUP_TARGET"); err != nil {
		t.Fatalf("unset DUSTPAN_BACKUP_TARGET: %v", err)
	}

	var absent serveFlags

	if err := newTestFlagSet(&absent).Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if absent.backupTarget != "" {
		t.Fatalf("backup target = %q, want empty for no backups", absent.backupTarget)
	}

	t.Setenv("DUSTPAN_BACKUP_TARGET", "dir:/env-target")

	var fromEnv serveFlags

	if err := newTestFlagSet(&fromEnv).Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if fromEnv.backupTarget != "dir:/env-target" {
		t.Fatalf("backup target = %q, want the variable dir:/env-target", fromEnv.backupTarget)
	}

	var flagWins serveFlags

	if err := newTestFlagSet(&flagWins).Parse([]string{"--backup-target", "dir:/flag-target"}); err != nil {
		t.Fatalf("parse --backup-target: %v", err)
	}

	if flagWins.backupTarget != "dir:/flag-target" {
		t.Fatalf("backup target = %q, want the flag dir:/flag-target to win", flagWins.backupTarget)
	}
}

func TestOpenBackupTarget(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "empty means no backups", raw: "", wantErr: ""},
		{name: "blank means no backups", raw: "   ", wantErr: ""},
		{name: "no scheme", raw: "/tmp/backups", wantErr: "dir:/path"},
		{name: "unknown scheme", raw: "file:/tmp/backups", wantErr: "dir:/path"},
		{name: "missing path", raw: "dir:", wantErr: "dir:/path"},
		{name: "blank path", raw: "dir:   ", wantErr: "dir:/path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBackupTargetCase(t, tc.raw, tc.wantErr)
		})
	}
}

// checkBackupTargetCase proves openBackupTarget answers wantErr for raw:
// empty means no target without an error, else the error names wantErr and
// shows the allowed forms.
func checkBackupTargetCase(t *testing.T, raw, wantErr string) {
	t.Helper()

	target, err := openBackupTarget(t.Context(), raw, "", "", nil)
	if wantErr == "" {
		if err != nil || target != nil {
			t.Fatalf("open %q: target %v, err %v, want nil, nil", raw, target, err)
		}

		return
	}

	if target != nil {
		closeBackupTarget(t, target)
	}

	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("open %q: err = %v, want it to name %s", raw, err, wantErr)
	}

	if !strings.Contains(err.Error(), "dir:/path") {
		t.Fatalf("open %q: err = %v, want it to show the allowed forms", raw, err)
	}
}

// closeBackupTarget closes target when it holds a directory handle, so dir:
// targets release their handle while S3 targets need no close.
func closeBackupTarget(t *testing.T, target blob.Store) {
	t.Helper()

	if closer, ok := target.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			t.Errorf("close backup target: %v", err)
		}
	}
}

func TestOpenBackupTargetDir(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "backups")

	target, err := openBackupTarget(t.Context(), "dir:"+dir, "", "", nil)
	if err != nil {
		t.Fatalf("open dir target: %v", err)
	}

	if target == nil {
		t.Fatal("open dir target = nil, want a store")
	}

	t.Cleanup(func() {
		closeBackupTarget(t, target)
	})

	checkBackupTargetDir(t, dir)
	checkBackupTargetRoundTrip(t, target)
}

// checkBackupTargetDir proves dir exists owner-only, so the plain-text state
// in the target stays private.
func checkBackupTargetDir(t *testing.T, dir string) {
	t.Helper()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat backup target directory: %v", err)
	}

	if perm := info.Mode().Perm(); perm != blobDirPerm {
		t.Fatalf("backup target directory mode %04o, want %04o", perm, blobDirPerm)
	}
}

// checkBackupTargetRoundTrip proves the opened target answers and stores one object.
func checkBackupTargetRoundTrip(t *testing.T, target blob.Store) {
	t.Helper()

	ctx := t.Context()

	if err := target.Ping(ctx); err != nil {
		t.Fatalf("ping backup target: %v", err)
	}

	key, err := blob.ParseKey("backups/probe")
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	if err = target.Put(ctx, key, strings.NewReader("probe")); err != nil {
		t.Fatalf("put probe: %v", err)
	}

	reader, err := target.Get(ctx, key)
	if err != nil {
		t.Fatalf("get probe: %v", err)
	}

	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("close probe: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read probe: %v", err)
	}

	if string(got) != "probe" {
		t.Fatalf("probe = %q, want %q", got, "probe")
	}
}

func TestBackupOptions(t *testing.T) {
	t.Parallel()

	var f serveFlags

	if opts := f.backupOptions(nil); opts != nil {
		t.Fatalf("backup options without a target = %d options, want none", len(opts))
	}

	target, err := openBackupTarget(t.Context(), "dir:"+filepath.Join(t.TempDir(), "backups"), "", "", nil)
	if err != nil {
		t.Fatalf("open dir target: %v", err)
	}

	if target == nil {
		t.Fatal("open dir target = nil, want a store")
	}

	t.Cleanup(func() {
		closeBackupTarget(t, target)
	})

	f.backupTarget = "dir:/var/backups"

	if opts := f.backupOptions(target); len(opts) != 1 {
		t.Fatalf("backup options with a target = %d options, want one", len(opts))
	}
}

// restoreBackupID is the backup ID the restore tests take and load.
const restoreBackupID = backup.ID("bkp-restore-test")

// setRestoreEnv sets the key-encryption key for one restore run and clears
// the key file setting, so loadKeyEncryptionKey reads keyBase64. Callers set
// it before every run: the load clears the variable after the read.
func setRestoreEnv(t *testing.T, keyBase64 string) {
	t.Helper()

	if err := os.Unsetenv(keyFileEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyFileEnvVar, err)
	}

	t.Setenv(keyEnvVar, keyBase64)
}

// runRestoreCommand runs `restore` with args and returns its stdout and
// error. A nil error means exit code 0, a non-nil error means exit code 1.
func runRestoreCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := run(t.Context(), append([]string{"restore"}, args...), &stdout, &stderr)

	return stdout.String(), err
}

// restoreArgs returns the restore flags for id from target into database
// with blobDir and region.
func restoreArgs(target, id, database, blobDir, region string) []string {
	return []string{
		"--from", target,
		"--backup", id,
		"--database", database,
		"--blob-dir", blobDir,
		"--region", region,
	}
}

// seedRestoreState writes one finalized OpenTofu state version with a
// greeting output to the source store, so the backup holds one blob.
func seedRestoreState(
	ctx context.Context,
	t *testing.T,
	st *sqlite.Store,
	blobs *blobfs.Store,
	namespaces *namespace.Service,
) {
	t.Helper()

	nsName, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	wsName, err := workspace.ParseName("ws1")
	if err != nil {
		t.Fatalf("parse workspace: %v", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		Workspaces:         st,
		States:             st,
		Blobs:              blobs,
		Namespaces:         namespaces,
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	ws, err := workspaces.Create(ctx, workspace.Ref{Namespace: nsName, Name: wsName})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, err = workspaces.Lock(ctx, ws.ID, "seed", "seed"); err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	content := []byte(`{"version":4,"terraform_version":"1.11.0","serial":1,` +
		`"lineage":"seed-lineage","outputs":{"greeting":{"value":"hello","type":"string"}},"resources":[]}`)
	sum := md5.Sum(content) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	pending, err := workspaces.BeginState(ctx, ws.ID, "seed", workspace.NewState{
		Lineage: "seed-lineage",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: []byte(`{"greeting":{"sensitive":false,"value":"hello","type":"string"}}`),
		Serial:  1,
		Force:   false,
	})
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	if _, err = workspaces.Upload(ctx, pending.Version.ID, pending.Upload, bytes.NewReader(content)); err != nil {
		t.Fatalf("upload state: %v", err)
	}

	if _, err = workspaces.Unlock(ctx, ws.ID, "seed"); err != nil {
		t.Fatalf("unlock workspace: %v", err)
	}
}

// seedRestoreSource creates a SQLite source installation in dir with one
// namespace, one wrapped key and one finalized state version, then takes
// backup id into a target directory with key. It returns the target
// directory in dir:/path form without the scheme.
func seedRestoreSource(t *testing.T, dir string, id backup.ID, key []byte) string {
	t.Helper()

	ctx := t.Context()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create source directory: %v", err)
	}

	dbPath := filepath.Join(dir, "dustpan.db")
	seedMainKey(t, dbPath, key)

	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close source store: %v", closeErr)
		}
	}()

	srcBlobs, err := blobfs.Open(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("open source blobs: %v", err)
	}

	defer func() {
		if closeErr := srcBlobs.Close(); closeErr != nil {
			t.Errorf("close source blobs: %v", closeErr)
		}
	}()

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	seedRestoreState(ctx, t, st, srcBlobs, namespaces)

	targetDir := filepath.Join(dir, "target")

	target, err := blobfs.Open(targetDir)
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	defer func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	}()

	svc, err := backup.NewService(backup.Config{
		Exporter:    st,
		Registry:    st,
		Blobs:       srcBlobs,
		Target:      target,
		TargetName:  "dir:" + targetDir,
		Now:         nil,
		Version:     "",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	if _, err := svc.Run(ctx, id); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	return targetDir
}

// countRestoreRows returns the row count of each table in the SQLite
// database at path.
func countRestoreRows(t *testing.T, path string) map[string]int64 {
	t.Helper()

	st, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	}()

	summary, err := st.Export(t.Context(), io.Discard)
	if err != nil {
		t.Fatalf("export store: %v", err)
	}

	return summary.RowCounts
}

// listRestoreFiles returns the paths of every file under dir, relative to
// dir, so a test proves a refused restore wrote nothing.
func listRestoreFiles(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	var walk func(path string)

	walk = func(path string) {
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatalf("read directory %s: %v", path, err)
		}

		for _, entry := range entries {
			full := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				walk(full)
				continue
			}

			rel, err := filepath.Rel(dir, full)
			if err != nil {
				t.Fatalf("relative path of %s: %v", full, err)
			}

			out = append(out, rel)
		}
	}

	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		t.Fatalf("stat directory %s: %v", dir, err)
	}

	walk(dir)

	return out
}

// checkRestoreCounts proves that out holds the row count line of each pair:
// one line per table in backup.Tables order.
func checkRestoreCounts(t *testing.T, out string, counts map[string]int64) {
	t.Helper()

	for _, table := range backup.Tables {
		want := fmt.Sprintf("table %s: %d rows", table, counts[table])
		if !strings.Contains(out, want) {
			t.Fatalf("restore output lacks %q:\n%s", want, out)
		}
	}
}

//nolint:paralleltest // restore reads process environment, so this test sets it and runs its cases in order.
func TestRestore(t *testing.T) {
	setRestoreEnv(t, testKeyBase64())

	dir := t.TempDir()
	targetDir := seedRestoreSource(t, filepath.Join(dir, "src"), restoreBackupID, []byte(testKeyString))

	destDir := filepath.Join(dir, "dest")

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}

	destDB := "sqlite:" + filepath.Join(destDir, "dustpan.db")
	destBlobs := filepath.Join(destDir, "blobs")

	setRestoreEnv(t, testKeyBase64())

	out, err := runRestoreCommand(
		t,
		restoreArgs("dir:"+targetDir, restoreBackupID.String(), destDB, destBlobs, "local")...,
	)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	checkRestoreSuccess(t, out)

	counts := countRestoreRows(t, filepath.Join(destDir, "dustpan.db"))
	checkRestoreCounts(t, out, counts)
	checkRestoredTables(t, counts)

	if files := listRestoreFiles(t, destBlobs); len(files) == 0 {
		t.Fatal("restored blob directory holds no blob, want the state content")
	}

	checkRestoreRefusal(t, targetDir, destDB, destBlobs, destDir)
}

// checkRestoreSuccess proves that out holds every step line with the success
// line and no warning: the regions match.
func checkRestoreSuccess(t *testing.T, out string) {
	t.Helper()

	for _, want := range []string{
		"restore backup " + restoreBackupID.String(),
		"read manifest",
		"checked key",
		"copied blobs",
		"loaded SQL data",
		"restored backup " + restoreBackupID.String(),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("restore output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "warning:") {
		t.Fatalf("restore output holds a warning in the backup region:\n%s", out)
	}
}

// checkRestoredTables proves the restored database holds the seeded rows:
// one namespace, one wrapped key, one workspace, one state version and the
// backup row the take recorded.
func checkRestoredTables(t *testing.T, counts map[string]int64) {
	t.Helper()

	for table, want := range map[string]int64{
		"namespaces":     1,
		"secret_keys":    1,
		"workspaces":     1,
		"state_versions": 1,
		"backups":        1,
	} {
		if counts[table] != want {
			t.Fatalf("restored table %s holds %d rows, want %d", table, counts[table], want)
		}
	}
}

// checkRestoreRefusal proves a second restore into the same destination
// exits 1 with ErrNotEmpty, names the flags that fix it, and writes nothing:
// the row counts and the blob files stay as they were.
func checkRestoreRefusal(t *testing.T, targetDir, destDB, destBlobs, destDir string) {
	t.Helper()

	dbPath := filepath.Join(destDir, "dustpan.db")
	before := countRestoreRows(t, dbPath)
	beforeFiles := listRestoreFiles(t, destBlobs)

	setRestoreEnv(t, testKeyBase64())

	_, err := runRestoreCommand(
		t,
		restoreArgs("dir:"+targetDir, restoreBackupID.String(), destDB, destBlobs, "local")...,
	)
	if err == nil || !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("restore into a non-empty destination: err = %v, want ErrNotEmpty", err)
	}

	for _, want := range []string{"--database", "--blob-dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("restore refusal %q does not name %s", err, want)
		}
	}

	after := countRestoreRows(t, dbPath)

	for _, table := range backup.Tables {
		if after[table] != before[table] {
			t.Fatalf("refused restore changed table %s from %d to %d rows",
				table, before[table], after[table])
		}
	}

	afterFiles := listRestoreFiles(t, destBlobs)
	if strings.Join(afterFiles, "\n") != strings.Join(beforeFiles, "\n") {
		t.Fatalf("refused restore changed the blob directory from %q to %q", beforeFiles, afterFiles)
	}
}

//nolint:paralleltest // restore reads process environment, so this test sets it and runs its cases in order.
func TestRestoreRegionWarning(t *testing.T) {
	setRestoreEnv(t, testKeyBase64())

	dir := t.TempDir()
	targetDir := seedRestoreSource(t, filepath.Join(dir, "src"), restoreBackupID, []byte(testKeyString))

	destDB := "sqlite:" + filepath.Join(dir, "dest", "dustpan.db")
	destBlobs := filepath.Join(dir, "dest", "blobs")

	if err := os.MkdirAll(filepath.Join(dir, "dest"), 0o700); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}

	setRestoreEnv(t, testKeyBase64())

	out, err := runRestoreCommand(
		t,
		restoreArgs("dir:"+targetDir, restoreBackupID.String(), destDB, destBlobs, "other")...,
	)
	if err != nil {
		t.Fatalf("restore with another region: %v", err)
	}

	if !strings.Contains(out, `warning: region "other"`) {
		t.Fatalf("restore output lacks the region warning:\n%s", out)
	}

	if !strings.Contains(out, "restored backup "+restoreBackupID.String()) {
		t.Fatalf("restore output lacks the success line:\n%s", out)
	}
}

//nolint:paralleltest // restore reads process environment, so this test sets it and runs its cases in order.
func TestRestoreKeyRefusals(t *testing.T) {
	dir := t.TempDir()
	targetDir := seedRestoreSource(t, filepath.Join(dir, "src"), restoreBackupID, []byte(testKeyString))

	cases := []struct {
		name string
		key  string
	}{
		{name: "no key", key: ""},
		{name: "another key", key: base64.StdEncoding.EncodeToString([]byte(otherKeyString))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkRestoreKeyCase(t, dir, targetDir, tc.name, tc.key)
		})
	}
}

// checkRestoreKeyCase proves a restore with key into a fresh destination
// refuses with an error naming the key setting.
func checkRestoreKeyCase(t *testing.T, dir, targetDir, name, key string) {
	t.Helper()

	setRestoreKeyCase(t, key)

	destDB := "sqlite:" + filepath.Join(dir, "dest-"+name, "dustpan.db")
	destBlobs := filepath.Join(dir, "dest-"+name, "blobs")

	if err := os.MkdirAll(filepath.Join(dir, "dest-"+name), 0o700); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}

	_, err := runRestoreCommand(
		t,
		restoreArgs("dir:"+targetDir, restoreBackupID.String(), destDB, destBlobs, "local")...,
	)
	if err == nil || !strings.Contains(err.Error(), keyEnvVar) {
		t.Fatalf("restore with %s: err = %v, want it to name %s", name, err, keyEnvVar)
	}
}

// setRestoreKeyCase sets key for one key-refusal case, or clears both key
// settings for the missing-key case.
func setRestoreKeyCase(t *testing.T, key string) {
	t.Helper()

	if key != "" {
		setRestoreEnv(t, key)

		return
	}

	if err := os.Unsetenv(keyEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyEnvVar, err)
	}

	if err := os.Unsetenv(keyFileEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyFileEnvVar, err)
	}
}

//nolint:paralleltest // restore reads process environment, so this test sets it.
func TestRestoreNewerSchema(t *testing.T) {
	setRestoreEnv(t, testKeyBase64())

	path, dbVersion, binVersion := newerSchemaDatabase(t)

	_, err := runRestoreCommand(
		t,
		restoreArgs(
			"dir:"+filepath.Join(t.TempDir(), "target"),
			restoreBackupID.String(),
			"sqlite:"+path,
			filepath.Join(t.TempDir(), "blobs"),
			"local",
		)...,
	)
	if !isNewerSchemaError(t, err, dbVersion, binVersion) {
		t.Fatalf("restore on newer database: err = %v, want it to name %d, %d and %q",
			err, dbVersion, binVersion, version)
	}
}

//nolint:paralleltest // restore reads process environment, so this test clears it and runs its cases in order.
func TestRestoreUsage(t *testing.T) {
	if err := os.Unsetenv(keyEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyEnvVar, err)
	}

	if err := os.Unsetenv(keyFileEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyFileEnvVar, err)
	}

	if !strings.Contains(errUsage.Error(), "restore") {
		t.Fatalf("usage = %q, want it to name restore", errUsage.Error())
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing backup",
			args: []string{"--from", "dir:/tmp/backups"},
			want: "--backup",
		},
		{
			name: "missing source",
			args: []string{"--backup", "bkp-aaa"},
			want: "--from",
		},
		{
			name: "unknown backend",
			args: []string{"--from", "dir:/tmp/backups", "--backup", "bkp-aaa", "--database", "mysql://db"},
			want: "sqlite:",
		},
		{
			name: "unknown source form",
			args: []string{
				"--from", "file:/tmp/backups",
				"--backup", "bkp-aaa",
				"--database", "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db"),
			},
			want: "dir:/path",
		},
		{
			name: "extra argument",
			args: []string{"--from", "dir:/tmp/backups", "--backup", "bkp-aaa", "extra"},
			want: "usage:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := run(t.Context(), append([]string{"restore"}, tc.args...), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("restore %s: err = %v, want it to name %s", tc.name, err, tc.want)
			}
		})
	}
}

// s3Fake is a minimal S3-compatible server for the cmd tests: it holds objects
// in memory. When honor is true, a PUT with If-None-Match: * on an existing
// object answers 412. When honor is false, the PUT overwrites and answers 200,
// so the open probe finds no conditional writes.
type s3Fake struct {
	mu    sync.Mutex
	holds map[string][]byte
	honor bool
}

// newS3Fake returns an s3Fake that honors conditional writes when honor is true.
func newS3Fake(honor bool) *s3Fake {
	return &s3Fake{mu: sync.Mutex{}, holds: map[string][]byte{}, honor: honor}
}

func (f *s3Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, ok := s3FakeBody(w, r)
	if !ok {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		f.servePut(w, r, body)
	case http.MethodGet:
		f.serveGet(w, r)
	case http.MethodDelete:
		delete(f.holds, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// s3FakeBody reads the request body of the fake. It answers 500 when the read
// fails.
func s3FakeBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, bodyErr := io.ReadAll(r.Body)
	closeErr := r.Body.Close()

	if bodyErr != nil || closeErr != nil {
		w.WriteHeader(http.StatusInternalServerError)

		return nil, false
	}

	return body, true
}

// servePut answers one PUT of the fake, honoring If-None-Match when honor is true.
func (f *s3Fake) servePut(w http.ResponseWriter, r *http.Request, body []byte) {
	if _, exists := f.holds[r.URL.Path]; exists && f.honor && r.Header.Get("If-None-Match") == "*" {
		w.WriteHeader(http.StatusPreconditionFailed)

		return
	}

	f.holds[r.URL.Path] = body
	w.WriteHeader(http.StatusOK)
}

// serveGet answers one GET of the fake with the held object or NoSuchKey.
func (f *s3Fake) serveGet(w http.ResponseWriter, r *http.Request) {
	if held, ok := f.holds[r.URL.Path]; ok {
		w.WriteHeader(http.StatusOK)

		if _, err := w.Write(held); err != nil {
			return
		}

		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusNotFound)

	const missing = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code></Error>`

	if _, err := io.WriteString(w, missing); err != nil {
		return
	}
}

// setS3Env sets fake S3 credentials for a test that opens an s3:// store.
func setS3Env(t *testing.T) {
	t.Helper()

	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-cmd-1")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-cmd-1")
	t.Setenv("AWS_SESSION_TOKEN", "")
}

//nolint:usetesting // t.Setenv cannot unset a variable; the cleanup restores it.
func TestBlobStoreFlagPrecedence(t *testing.T) {
	for _, key := range []string{"DUSTPAN_BLOB_STORE", "DUSTPAN_S3_ENDPOINT", "DUSTPAN_S3_REGION"} {
		orig, had := os.LookupEnv(key)

		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}

		t.Cleanup(func() {
			var restoreErr error

			if had {
				restoreErr = os.Setenv(key, orig)
			} else {
				restoreErr = os.Unsetenv(key)
			}

			if restoreErr != nil {
				t.Errorf("restore %s: %v", key, restoreErr)
			}
		})
	}

	checkBlobStoreDefaults(t)

	t.Setenv("DUSTPAN_BLOB_STORE", "s3://env-bucket/env-prefix")
	t.Setenv("DUSTPAN_S3_ENDPOINT", "http://env-endpoint:9000")
	t.Setenv("DUSTPAN_S3_REGION", "eu-west-1")

	checkBlobStoreFromEnv(t)
	checkBlobStoreFlagWins(t)
}

// checkBlobStoreDefaults proves the storage flags default to fs with no
// endpoint or region.
func checkBlobStoreDefaults(t *testing.T) {
	t.Helper()

	var absent serveFlags

	if err := newTestFlagSet(&absent).Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if absent.blobStore != "fs" {
		t.Fatalf("blob store = %q, want fs", absent.blobStore)
	}

	if absent.s3Endpoint != "" || absent.s3Region != "" {
		t.Fatalf("s3 settings = %q %q, want empty defaults", absent.s3Endpoint, absent.s3Region)
	}
}

// checkBlobStoreFromEnv proves the storage flags read their variables.
func checkBlobStoreFromEnv(t *testing.T) {
	t.Helper()

	var fromEnv serveFlags

	if err := newTestFlagSet(&fromEnv).Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if fromEnv.blobStore != "s3://env-bucket/env-prefix" {
		t.Fatalf("blob store = %q, want the variable", fromEnv.blobStore)
	}

	if fromEnv.s3Endpoint != "http://env-endpoint:9000" || fromEnv.s3Region != "eu-west-1" {
		t.Fatalf("s3 settings = %q %q, want the variables", fromEnv.s3Endpoint, fromEnv.s3Region)
	}
}

// checkBlobStoreFlagWins proves the storage flags win over their variables.
func checkBlobStoreFlagWins(t *testing.T) {
	t.Helper()

	args := []string{
		"--blob-store", "s3://flag-bucket/flag-prefix",
		"--s3-endpoint", "http://flag-endpoint:9000",
		"--s3-region", "us-west-2",
	}

	var flagWins serveFlags

	if err := newTestFlagSet(&flagWins).Parse(args); err != nil {
		t.Fatalf("parse s3 flags: %v", err)
	}

	if flagWins.blobStore != "s3://flag-bucket/flag-prefix" {
		t.Fatalf("blob store = %q, want the flag to win", flagWins.blobStore)
	}

	if flagWins.s3Endpoint != "http://flag-endpoint:9000" || flagWins.s3Region != "us-west-2" {
		t.Fatalf("s3 settings = %q %q, want the flags to win", flagWins.s3Endpoint, flagWins.s3Region)
	}
}

//nolint:paralleltest // Serve reads process environment through setServeEnv.
func TestServeBlobStoreBadValue(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	path := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	err := serve(t.Context(), []string{"--database", path, "--blob-store", "bogus"}, io.Discard)
	if err == nil || !errors.Is(err, errBlobStore) {
		t.Fatalf("serve with a bad blob store: err = %v, want errBlobStore", err)
	}

	if !strings.Contains(err.Error(), "--blob-store") {
		t.Fatalf("serve with a bad blob store: err = %v, want it to name the flag", err)
	}
}

func TestParseS3Target(t *testing.T) {
	t.Parallel()

	for _, tc := range []parseS3Case{
		{name: "bucket only", raw: "s3://mybucket", bucket: "mybucket", pfx: "", ok: true},
		{name: "bucket and prefix", raw: "s3://mybucket/pfx/sub", bucket: "mybucket", pfx: "pfx/sub", ok: true},
		{name: "trims slashes", raw: "s3://mybucket//pfx//", bucket: "mybucket", pfx: "pfx", ok: true},
		{name: "missing bucket", raw: "s3://", bucket: "", pfx: "", ok: false},
		{name: "missing scheme", raw: "mybucket/pfx", bucket: "", pfx: "", ok: false},
		{name: "blank prefix segment", raw: "s3://mybucket/a//b", bucket: "", pfx: "", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkParseS3Target(t, tc)
		})
	}
}

// parseS3Case is one parse case of TestParseS3Target.
type parseS3Case struct {
	name   string
	raw    string
	bucket string
	pfx    string
	ok     bool
}

// checkParseS3Target proves parseS3Target splits raw into bucket and prefix,
// or fails with errBlobStore when the case wants an error.
func checkParseS3Target(t *testing.T, tc parseS3Case) {
	t.Helper()

	gotBucket, gotPrefix, err := parseS3Target(tc.raw, errBlobStore)
	if !tc.ok {
		if err == nil {
			t.Fatalf("parse %q succeeded, want an error", tc.raw)
		}

		if !errors.Is(err, errBlobStore) {
			t.Fatalf("parse %q: err = %v, want errBlobStore", tc.raw, err)
		}

		return
	}

	if err != nil {
		t.Fatalf("parse %q: %v", tc.raw, err)
	}

	if gotBucket != tc.bucket || gotPrefix != tc.pfx {
		t.Fatalf("parse %q = %q %q, want %q %q", tc.raw, gotBucket, gotPrefix, tc.bucket, tc.pfx)
	}
}

func TestS3EndpointWarning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		endpoint string
		wantWarn bool
	}{
		{name: "plain http warns", endpoint: "http://localhost:9000", wantWarn: true},
		{name: "https stays quiet", endpoint: "https://s3.example", wantWarn: false},
		{name: "empty stays quiet", endpoint: "", wantWarn: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&logs, nil))
			warnPlainHTTP(t.Context(), logger, tc.endpoint)

			got := strings.Contains(logs.String(), "plain HTTP")
			if got != tc.wantWarn {
				t.Fatalf("warn for %q = %v, want %v (%q)", tc.endpoint, got, tc.wantWarn, logs.String())
			}
		})
	}
}

func TestOpenS3MissingCredential(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")

	if err := os.Unsetenv("AWS_ACCESS_KEY_ID"); err != nil {
		t.Fatalf("unset AWS_ACCESS_KEY_ID: %v", err)
	}

	_, err := openS3Store(t.Context(), s3StoreConfig{
		raw:      "s3://mybucket/pfx",
		endpoint: "",
		region:   "",
		formErr:  errBlobStore,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "AWS_ACCESS_KEY_ID") {
		t.Fatalf("open without credentials: err = %v, want it to name AWS_ACCESS_KEY_ID", err)
	}

	for _, leaked := range []string{"test-access-cmd-1", "test-secret-cmd-1"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error %q holds credential material", err.Error())
		}
	}
}

//nolint:paralleltest // The test sets credentials through a helper that calls t.Setenv.
func TestOpenBackupTargetS3(t *testing.T) {
	setS3EnvForTest(t)

	fake := newS3Fake(true)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	target, err := openBackupTarget(t.Context(), "s3://cmd-bucket/backups", server.URL, "us-east-1", nil)
	if err != nil {
		t.Fatalf("open s3 backup target: %v", err)
	}

	if target == nil {
		t.Fatal("open s3 backup target = nil, want a store")
	}

	t.Cleanup(func() {
		closeBackupTarget(t, target)
	})

	checkBackupTargetRoundTrip(t, target)
}

// setS3EnvForTest sets fake credentials without touching the parallel-safe
// caller: tests that run in parallel set their own variables through it.
func setS3EnvForTest(t *testing.T) {
	t.Helper()

	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-cmd-1")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-cmd-1")
}

//nolint:paralleltest // Serve reads process environment and binds a port.
func TestServeWithS3BlobStore(t *testing.T) {
	setServeEnv(t, "")
	setS3Env(t)

	fake := newS3Fake(true)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	var logs lockedBuffer

	dbPath := filepath.Join(t.TempDir(), "dustpan.db")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- serve(ctx, []string{
			"--database", "sqlite:" + dbPath,
			"--listen", "127.0.0.1:0",
			"--blob-store", "s3://cmd-bucket/live",
			"--s3-endpoint", server.URL,
			"--s3-region", "us-east-1",
		}, &logs)
	}()

	ticker := time.NewTicker(serveStartPoll)
	defer ticker.Stop()

	timeout := time.NewTimer(serveStartTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-ticker.C:
			if strings.Contains(logs.String(), "listening") {
				cancel()

				if err := <-done; err != nil {
					t.Fatalf("serve with s3 blobs: %v", err)
				}

				if !strings.Contains(logs.String(), "plain HTTP") {
					t.Fatalf("s3 logs lack the plain HTTP warning:\n%s", logs.String())
				}

				return
			}
		case <-timeout.C:
			t.Fatal("server with s3 blobs did not start in time")
		}
	}
}

//nolint:paralleltest // Serve reads process environment and binds a port.
func TestServeRefusesS3WithoutConditionalWrites(t *testing.T) {
	setServeEnv(t, "")
	setS3Env(t)

	fake := newS3Fake(false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	dbPath := filepath.Join(t.TempDir(), "dustpan.db")

	err := serve(t.Context(), []string{
		"--database", "sqlite:" + dbPath,
		"--listen", "127.0.0.1:0",
		"--blob-store", "s3://cmd-bucket/live",
		"--s3-endpoint", server.URL,
		"--s3-region", "us-east-1",
	}, io.Discard)
	if err == nil {
		t.Fatal("serve with a store without conditional writes succeeded, want an error")
	}

	if !strings.Contains(err.Error(), "conditional writes") {
		t.Fatalf("serve without conditional writes: err = %v, want the probe error", err)
	}

	for _, leaked := range []string{"test-access-cmd-1", "test-secret-cmd-1"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error %q holds credential material", err.Error())
		}
	}
}

func TestOpenBackupTargetS3BadValue(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"s3://", "s3://mybucket/a//b"} {
		target, err := openBackupTarget(t.Context(), raw, "", "", nil)
		if target != nil {
			closeBackupTarget(t, target)
		}

		if err == nil || !errors.Is(err, errBackupTarget) {
			t.Fatalf("open backup target %q: err = %v, want errBackupTarget", raw, err)
		}

		if !strings.Contains(err.Error(), "--backup-target") {
			t.Fatalf("open backup target %q: err = %v, want it to name --backup-target", raw, err)
		}

		if strings.Contains(err.Error(), "--blob-store") {
			t.Fatalf("open backup target %q: err = %v, want no --blob-store", raw, err)
		}
	}
}

func TestCheckS3Endpoint(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"ftp://x", "file:///x", "http://", "https://", "http://   "} {
		err := checkS3Endpoint(endpoint)
		if !errors.Is(err, errS3Endpoint) {
			t.Fatalf("check endpoint %q: err = %v, want errS3Endpoint", endpoint, err)
		}

		if err == nil || !strings.Contains(err.Error(), "--s3-endpoint") {
			t.Fatalf("check endpoint %q: err = %v, want it to name --s3-endpoint", endpoint, err)
		}
	}

	for _, endpoint := range []string{"", "https://s3.example", "http://host", "http://host:9000/path"} {
		if err := checkS3Endpoint(endpoint); err != nil {
			t.Fatalf("check endpoint %q: %v, want no error", endpoint, err)
		}
	}
}

func TestOpenS3StoreBadEndpoint(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"ftp://x", "file:///x", "http://"} {
		_, err := openS3Store(t.Context(), s3StoreConfig{
			raw:      "s3://cmd-bucket/live",
			endpoint: endpoint,
			region:   "us-east-1",
			formErr:  errBlobStore,
		}, nil)
		if err == nil || !errors.Is(err, errS3Endpoint) {
			t.Fatalf("open s3 store with endpoint %q: err = %v, want errS3Endpoint", endpoint, err)
		}

		if !strings.Contains(err.Error(), "--s3-endpoint") {
			t.Fatalf("open s3 store with endpoint %q: err = %v, want it to name --s3-endpoint", endpoint, err)
		}
	}
}

func TestOpenBackupTargetS3BadEndpoint(t *testing.T) {
	t.Parallel()

	_, err := openBackupTarget(t.Context(), "s3://cmd-bucket/backups", "ftp://x", "us-east-1", nil)
	if err == nil || !errors.Is(err, errS3Endpoint) {
		t.Fatalf("open s3 backup target with a bad endpoint: err = %v, want errS3Endpoint", err)
	}

	if !strings.Contains(err.Error(), "--s3-endpoint") {
		t.Fatalf("open s3 backup target with a bad endpoint: err = %v, want it to name --s3-endpoint", err)
	}
}

func TestResolveS3RegionDefault(t *testing.T) {
	t.Parallel()

	if got := resolveS3Region(""); got != "us-east-1" {
		t.Fatalf("resolve empty region = %q, want us-east-1", got)
	}

	if got := resolveS3Region("   "); got != "us-east-1" {
		t.Fatalf("resolve blank region = %q, want us-east-1", got)
	}

	if got := resolveS3Region("eu-west-1"); got != "eu-west-1" {
		t.Fatalf("resolve eu-west-1 = %q, want eu-west-1", got)
	}
}

//nolint:paralleltest // The test sets credentials through a helper that calls t.Setenv.
func TestOpenS3StoreEmptyRegion(t *testing.T) {
	setS3EnvForTest(t)

	fake := newS3Fake(true)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	target, err := openBackupTarget(t.Context(), "s3://cmd-bucket/backups", server.URL, "", nil)
	if err != nil {
		t.Fatalf("open s3 backup target with an empty region: %v", err)
	}

	if target == nil {
		t.Fatal("open s3 backup target with an empty region = nil, want a store")
	}

	t.Cleanup(func() {
		closeBackupTarget(t, target)
	})

	checkBackupTargetRoundTrip(t, target)
}

// TestServeDeltaCutoffDefault proves the checkpoint delta cutoff defaults
// to 1 MiB, so the server advertises delta uploads unless configured
// otherwise.
func TestServeDeltaCutoffDefault(t *testing.T) {
	t.Parallel()

	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if f.checkpointDeltaCutoff != 1<<20 {
		t.Fatalf("checkpoint delta cutoff = %d, want %d", f.checkpointDeltaCutoff, 1<<20)
	}
}

// TestServeDeltaCutoffFlag proves --checkpoint-delta-cutoff sets the
// cutoff, 0 turns delta uploads off, and a negative value fails naming
// the flag.
//
//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
func TestServeDeltaCutoffFlag(t *testing.T) {
	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse([]string{"--checkpoint-delta-cutoff", "1"}); err != nil {
		t.Fatalf("parse cutoff: %v", err)
	}

	if f.checkpointDeltaCutoff != 1 {
		t.Fatalf("checkpoint delta cutoff = %d, want 1", f.checkpointDeltaCutoff)
	}

	var off serveFlags

	if err := newTestFlagSet(&off).Parse([]string{"--checkpoint-delta-cutoff", "0"}); err != nil {
		t.Fatalf("parse zero cutoff: %v", err)
	}

	if off.checkpointDeltaCutoff != 0 {
		t.Fatalf("checkpoint delta cutoff = %d, want 0", off.checkpointDeltaCutoff)
	}

	setServeEnv(t, testKeyBase64())

	database := "--database"
	path := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	err := serve(
		t.Context(),
		[]string{database, path, "--checkpoint-delta-cutoff", "-1"},
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "--checkpoint-delta-cutoff") {
		t.Fatalf("serve with a negative cutoff: err = %v, want it to name the flag", err)
	}
}
