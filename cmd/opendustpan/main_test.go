package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
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
	if !errors.Is(err, secret.ErrWrapperMismatch) {
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

//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
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

	var out bytes.Buffer

	err := createNamespace(t.Context(), []string{"--database", "sqlite:" + path, "acme"}, &out, io.Discard)
	if !isNewerSchemaError(t, err, dbVersion, binVersion) {
		t.Fatalf("namespace create on newer database: err = %v, want it to name %d, %d and %q",
			err, dbVersion, binVersion, version)
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

	conn, connErr := sql.Open("sqlite3", "file:"+path)
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

// runUpgradeCheck runs `upgrade check` on database and returns its stdout and
// error. A nil error means exit code 0, a non-nil error means exit code 1.
func runUpgradeCheck(t *testing.T, database string) (string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := run(t.Context(), []string{"upgrade", "check", "--database", database}, &stdout, &stderr)

	return stdout.String(), err
}

// wantUpgradeOutput holds the expected report of one upgrade check run:
// the database and binary schema versions, whether pending migrations are
// listed, and the verdict.
type wantUpgradeOutput struct {
	dbVersion  int64
	binVersion int64
	pending    bool
	verdict    string
}

// checkUpgradeOutput proves that out names the binary version, the database
// and binary schema versions, the pending state and the verdict.
func checkUpgradeOutput(t *testing.T, out string, want wantUpgradeOutput) {
	t.Helper()

	for _, line := range []string{
		"binary version: " + version,
		fmt.Sprintf("database schema version: %d", want.dbVersion),
		fmt.Sprintf("binary schema version: %d", want.binVersion),
		"verdict: " + want.verdict,
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s", line, out)
		}
	}

	if want.pending {
		checkUpgradePending(t, out)
	} else {
		checkUpgradeNoPending(t, out)
	}
}

// checkUpgradePending proves that out lists pending migrations with the
// backup advice.
func checkUpgradePending(t *testing.T, out string) {
	t.Helper()

	if !strings.Contains(out, "pending migrations:") || !strings.Contains(out, ".sql") {
		t.Fatalf("upgrade check output lists no pending migration:\n%s", out)
	}

	for _, line := range []string{
		"Back up the database before the upgrade.",
		"No downgrade path exists.",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s", line, out)
		}
	}
}

// checkUpgradeNoPending proves that out holds `pending migrations: none`
// without backup advice.
func checkUpgradeNoPending(t *testing.T, out string) {
	t.Helper()

	if !strings.Contains(out, "pending migrations: none") {
		t.Fatalf("upgrade check output lacks `pending migrations: none`:\n%s", out)
	}

	if strings.Contains(out, "Back up the database") {
		t.Fatalf("upgrade check output gives backup advice without pending migrations:\n%s", out)
	}
}

// checkUpgradeUnchanged proves that upgrade check wrote nothing: the database
// file at path is byte-identical to before.
func checkUpgradeUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()

	after, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file after check: %v", err)
	}

	if !bytes.Equal(after, before) {
		t.Fatal("upgrade check changed the database file, want it unchanged")
	}
}

// oldFixtureMaxVersion is the newest version in the fixed old-database
// fixture. The fixture stays below the binary version, so the check reports
// pending migrations. Bump it only with the fixture name and assertions.
const oldFixtureMaxVersion = 8

// seedOldDatabase creates a SQLite database at path with schema versions 1
// through oldFixtureMaxVersion recorded, so upgrade check sees an old database.
func seedOldDatabase(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty database: %v", err)
	}

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	const createTable = `CREATE TABLE schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := conn.ExecContext(t.Context(), createTable); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`
	for v := int64(1); v <= oldFixtureMaxVersion; v++ {
		if _, err := conn.ExecContext(t.Context(), record, v, fmt.Sprintf("%04d_old.sql", v)); err != nil {
			t.Fatalf("record version %d: %v", v, err)
		}
	}
}

// migratedDatabase opens and closes the SQLite store at path, so the database
// holds every embedded migration, and returns the binary schema version.
func migratedDatabase(t *testing.T, path string) int64 {
	t.Helper()

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	var newest int64

	row := conn.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`)
	if scanErr := row.Scan(&newest); scanErr != nil {
		t.Fatalf("read binary schema version: %v", scanErr)
	}

	return newest
}

func TestUpgradeCheck(t *testing.T) {
	t.Parallel()

	t.Run("fresh database can upgrade", func(t *testing.T) {
		t.Parallel()
		checkUpgradeFresh(t)
	})

	t.Run("old database can upgrade", func(t *testing.T) {
		t.Parallel()
		checkUpgradeOld(t)
	})

	t.Run("up-to-date database is current", func(t *testing.T) {
		t.Parallel()
		checkUpgradeCurrent(t)
	})

	t.Run("newer database cannot start", func(t *testing.T) {
		t.Parallel()
		checkUpgradeNewer(t)
	})
}

// checkUpgradeFresh proves upgrade check on an empty file exits with
// `upgrade possible` and leaves the file unchanged.
func checkUpgradeFresh(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty database: %v", err)
	}

	before, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}

	out, checkErr := runUpgradeCheck(t, "sqlite:"+path)
	if checkErr != nil {
		t.Fatalf("upgrade check on a fresh database: %v", checkErr)
	}

	binVersion := migratedSchemaVersion(t)
	checkUpgradeOutput(
		t,
		out,
		wantUpgradeOutput{dbVersion: 0, binVersion: binVersion, pending: true, verdict: verdictUpgradePossible},
	)
	checkUpgradeUnchanged(t, path, before)
}

// checkUpgradeOld proves upgrade check on versions 1 through
// oldFixtureMaxVersion exits with `upgrade possible` and leaves the file unchanged.
func checkUpgradeOld(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	seedOldDatabase(t, path)

	before, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}

	out, checkErr := runUpgradeCheck(t, "sqlite:"+path)
	if checkErr != nil {
		t.Fatalf("upgrade check on an old database: %v", checkErr)
	}

	binVersion := migratedSchemaVersion(t)
	checkUpgradeOutput(
		t,
		out,
		wantUpgradeOutput{
			dbVersion:  oldFixtureMaxVersion,
			binVersion: binVersion,
			pending:    true,
			verdict:    verdictUpgradePossible,
		},
	)
	checkUpgradeUnchanged(t, path, before)
}

// checkUpgradeCurrent proves upgrade check on a migrated database exits with
// `up to date` and leaves the file unchanged.
func checkUpgradeCurrent(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	binVersion := migratedDatabase(t, path)

	before, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}

	out, checkErr := runUpgradeCheck(t, "sqlite:"+path)
	if checkErr != nil {
		t.Fatalf("upgrade check on an up-to-date database: %v", checkErr)
	}

	checkUpgradeOutput(
		t,
		out,
		wantUpgradeOutput{dbVersion: binVersion, binVersion: binVersion, pending: false, verdict: verdictUpToDate},
	)
	checkUpgradeUnchanged(t, path, before)
}

// checkUpgradeNewer proves upgrade check on a database with one future
// version exits with ErrSchemaNewer and leaves the file unchanged.
func checkUpgradeNewer(t *testing.T) {
	t.Helper()

	path, dbVersion, binVersion := newerSchemaDatabase(t)

	before, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}

	out, checkErr := runUpgradeCheck(t, "sqlite:"+path)
	if checkErr == nil || !errors.Is(checkErr, schema.ErrSchemaNewer) {
		t.Fatalf("upgrade check on a newer database: err = %v, want ErrSchemaNewer", checkErr)
	}

	checkUpgradeOutput(
		t,
		out,
		wantUpgradeOutput{dbVersion: dbVersion, binVersion: binVersion, pending: false, verdict: verdictCannotStart},
	)
	checkUpgradeUnchanged(t, path, before)

	for _, want := range []string{
		strconv.FormatInt(dbVersion, 10),
		strconv.FormatInt(binVersion, 10),
		version,
	} {
		if !strings.Contains(checkErr.Error(), want) {
			t.Fatalf("upgrade check error %q does not name %q", checkErr, want)
		}
	}
}

// migratedSchemaVersion returns the newest embedded migration version by
// migrating a scratch database.
func migratedSchemaVersion(t *testing.T) int64 {
	t.Helper()

	return migratedDatabase(t, filepath.Join(t.TempDir(), "probe.db"))
}

func TestUpgradeCheckUsage(t *testing.T) {
	t.Parallel()

	if !strings.Contains(errUsage.Error(), "upgrade check") {
		t.Fatalf("usage = %q, want it to name upgrade check", errUsage.Error())
	}

	cases := []struct {
		name string
		args []string
	}{
		{"upgrade without check", []string{"upgrade"}},
		{"upgrade check with a name", []string{"upgrade", "check", "--database", "sqlite:x.db", "extra"}},
		{"unknown backend", []string{"upgrade", "check", "--database", "mysql://db"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			if err := run(t.Context(), tc.args, &stdout, &stderr); err == nil {
				t.Fatalf("run %v: err = nil, want an error", tc.args)
			}
		})
	}

	t.Run("missing file names the path", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "missing.db")

		_, err := runUpgradeCheck(t, "sqlite:"+missing)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("upgrade check on a missing file: err = %v, want it to name %q", err, missing)
		}

		if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
			t.Fatalf("stat missing file: err = %v, want file to stay absent", statErr)
		}
	})
}
