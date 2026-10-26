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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
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

	seedWrappedKey(t, st, key)
}

// keySeedStore is what seedWrappedKey needs from a store adapter.
type keySeedStore interface {
	namespace.Store
	secret.KeyStore
}

// seedWrappedKey stores one wrapped key with key in st, through the secret
// service, as a server that encrypted one value does.
func seedWrappedKey(t *testing.T, st keySeedStore, key []byte) {
	t.Helper()

	err := st.CreateNamespace(t.Context(), namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
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

	_, err := startServerLogs(t, args...)

	return err
}

// startServerLogs runs serve as startServer does and answers its logs.
func startServerLogs(t *testing.T, args ...string) (string, error) {
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

				err := <-done

				return logs.String(), err
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

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")

	err := serve(t.Context(), []string{"--database", "sqlite:" + dbPath}, io.Discard)
	if err == nil || !errors.Is(err, errNoKey) {
		t.Fatalf("serve without a key on an empty database: err = %v, want errNoKey", err)
	}

	for _, want := range []string{
		"--key-encryption-key-file",
		"DUSTPAN_KEY_ENCRYPTION_KEY_FILE",
		keyEnvVar,
		"opendustpan key generate --out",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("serve without a key: err = %v, want it to name %s", err, want)
		}
	}

	if _, statErr := os.Stat(filepath.Join(dir, "dustpan.key")); !os.IsNotExist(statErr) {
		t.Fatal("serve without a key made a key file, want no file")
	}

	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatal("serve without a key opened the database, want it to stop before the database opens")
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

	app, err := f.appLogFor(io.Discard)
	if err != nil || app.logger == nil {
		t.Fatalf("logger for debug/text: logger %v, err %v", app.logger, err)
	}

	closeAppLog(t.Context(), &app, io.Discard)
}

func TestServeSizeFlagDefaults(t *testing.T) {
	t.Parallel()

	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse defaults: %v", err)
	}

	if f.maxCheckpointBytes != 0 || f.maxSecretBatchBytes != 0 || f.maxStateBytes != 0 || f.maxBodyBytesInFlight != 0 ||
		f.blobCacheBytes != 0 {
		t.Fatalf(
			"size flags = %d %d %d %d %d, want all zero for domain defaults",
			f.maxCheckpointBytes, f.maxSecretBatchBytes, f.maxStateBytes, f.maxBodyBytesInFlight, f.blobCacheBytes,
		)
	}

	sizeLimits := []int64{
		f.maxCheckpointBytes,
		f.maxSecretBatchBytes,
		f.maxStateBytes,
		f.maxBodyBytesInFlight,
		f.blobCacheBytes,
	}

	if err := checkSizeFlags(sizeLimits); err != nil {
		t.Fatalf("default size flags: %v", err)
	}

	if err := checkSizeFlags([]int64{1, 1, 1, 1, 1}); err != nil {
		t.Fatalf("positive size flags: %v", err)
	}

	if err := checkSizeFlags([]int64{0, 0, 0, 0, -1}); err == nil {
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

	// serve clears DUSTPAN_ADMIN_TOKEN and DUSTPAN_KEY_ENCRYPTION_KEY after
	// the read, so the second start in this process needs them again.
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	t.Setenv(keyEnvVar, testKeyBase64())

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

// setBinaryVersion sets the version of the binary for one test and restores
// it after. Only tests that run in order may call it.
func setBinaryVersion(t *testing.T, v string) {
	t.Helper()

	old := version
	version = v

	t.Cleanup(func() { version = old })
}

//nolint:paralleltest // serve reads process environment and the binary version, so this test sets them.
func TestServeRefusesBinaryBelowMinimum(t *testing.T) {
	setServeEnv(t, testKeyBase64())
	setBinaryVersion(t, "v0.1.0")

	path := newerSchemaDatabase(t, "v0.2.0")

	// A refusal returns at once. The bound keeps a missing refusal from
	// serving until the test times out.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	err := serve(ctx, []string{"--database", "sqlite:" + path}, io.Discard)
	if err == nil || !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("serve below the schema minimum: err = %v, want ErrBinaryBelowMinimum", err)
	}

	for _, want := range []string{"v0.1.0", "v0.2.0", "or restore a backup"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("serve below the schema minimum: error %q does not name %q", err, want)
		}
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeDevBuildWarnsOnSchemaMinimum(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	path := newerSchemaDatabase(t, "v9.0.0")

	logs, err := startServerLogs(t, "--database", "sqlite:"+path)
	if err != nil {
		t.Fatalf("serve a dev build on a newer schema: %v", err)
	}

	if !strings.Contains(logs, "dev build ignores the minimum binary version of the database schema") ||
		!strings.Contains(logs, "v9.0.0") {
		t.Fatalf("serve logs lack the dev build warning:\n%s", logs)
	}
}

// newerSchemaDatabase creates a SQLite database at the embedded migrations
// plus one future version row that sets minimum, and returns its path.
func newerSchemaDatabase(t *testing.T, minimum string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

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

	var binVersion int64

	row := conn.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`)
	if scanErr := row.Scan(&binVersion); scanErr != nil {
		t.Fatalf("read binary schema version: %v", scanErr)
	}

	dbVersion := binVersion + 1

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version) VALUES (?, ?, ?)`

	name := fmt.Sprintf("%04d_future.sql", dbVersion)

	if _, execErr := conn.ExecContext(t.Context(), record, dbVersion, name, minimum); execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}

	return path
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
// A ListObjectsV2 request gets the held objects under the requested prefix.
func (f *s3Fake) serveGet(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("list-type") == "2" {
		f.serveList(w, r)

		return
	}

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

// serveList answers one ListObjectsV2 request of the fake with the held
// objects under the requested prefix. Keys stay in URL-path form in the
// fake, so it strips the bucket segment before it compares the prefix.
func (f *s3Fake) serveList(w http.ResponseWriter, r *http.Request) {
	bucket, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	prefix := r.URL.Query().Get("prefix")

	var b strings.Builder

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated>`)

	for name := range f.holds {
		object, ok := strings.CutPrefix(strings.TrimPrefix(name, "/"), bucket+"/")
		if !ok || !strings.HasPrefix(object, prefix) {
			continue
		}

		b.WriteString(`<Contents><Key>`)
		b.WriteString(object)
		b.WriteString(`</Key><LastModified>2026-10-11T00:00:00Z</LastModified></Contents>`)
	}

	b.WriteString(`</ListBucketResult>`)

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)

	if _, err := io.WriteString(w, b.String()); err != nil {
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
	setServeEnv(t, testKeyBase64())
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
	setServeEnv(t, testKeyBase64())
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
