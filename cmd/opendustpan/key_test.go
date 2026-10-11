package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// clearKeyEnv clears every key-encryption key setting, so a bootstrap test
// starts with no key and no key file.
func clearKeyEnv(t *testing.T) {
	t.Helper()

	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

	if err := os.Unsetenv(keyEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyEnvVar, err)
	}

	if err := os.Unsetenv(keyFileEnvVar); err != nil {
		t.Fatalf("unset %s: %v", keyFileEnvVar, err)
	}

	if err := os.Unsetenv(generateKeyEnvVar); err != nil {
		t.Fatalf("unset %s: %v", generateKeyEnvVar, err)
	}
}

// startServerWithIO runs serveWithIO with a fake standard input until it logs
// listening, then ends it. A nil error means the start-up checks passed.
func startServerWithIO(
	t *testing.T, stdin string, terminal bool, logs *lockedBuffer, args ...string,
) error {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- serveWithIO(ctx, args, strings.NewReader(stdin), logs, terminal)
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

// readMadeKey reads the base64 key at path and answers its raw bytes.
func readMadeKey(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads the key file it made.
	if err != nil {
		t.Fatalf("read made key file: %v", err)
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decode made key: %v", err)
	}

	if len(key) != 32 {
		t.Fatalf("made key holds %d bytes, want 32", len(key))
	}

	return key
}

// checkMadeKeyFile proves path is an owner-only file with 32 random bytes in
// base64.
func checkMadeKeyFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat made key file: %v", err)
	}

	if perm := info.Mode().Perm(); perm != keyFileMode {
		t.Fatalf("made key file mode %04o, want %04o", perm, keyFileMode)
	}

	readMadeKey(t, path)
}

//nolint:paralleltest // serve reads process environment, so this test sets it and runs alone.
func TestServeKeyPromptYesMakesFileAndStarts(t *testing.T) {
	clearKeyEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")
	wantPath := filepath.Join(dir, "dustpan.key")

	var logs lockedBuffer

	args := []string{"--database", "sqlite:" + dbPath, "--listen", "127.0.0.1:0"}

	if err := startServerWithIO(t, "yes\n", true, &logs, args...); err != nil {
		t.Fatalf("serve with yes: %v", err)
	}

	checkMadeKeyFile(t, wantPath)

	out := logs.String()

	for _, want := range []string{wantPath, "loses", "nobody can recover"} {
		if !strings.Contains(out, want) {
			t.Fatalf("serve output lacks %q:\n%s", want, out)
		}
	}

	keyBase64 := base64.StdEncoding.EncodeToString(readMadeKey(t, wantPath))

	if strings.Contains(out, keyBase64) {
		t.Fatal("serve output holds the key")
	}

	if !strings.Contains(out, "key-encryption key file") {
		t.Fatalf("serve output lacks the key file log line:\n%s", out)
	}
}

//nolint:paralleltest // serve reads process environment, so this test runs alone.
func TestServeKeyPromptNoStops(t *testing.T) {
	clearKeyEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")
	wantPath := filepath.Join(dir, "dustpan.key")

	var logs lockedBuffer

	promptErr := serveWithIO(
		t.Context(),
		[]string{"--database", "sqlite:" + dbPath},
		strings.NewReader("no\n"),
		&logs,
		true,
	)
	if promptErr == nil || !errors.Is(promptErr, errKeyPromptDeclined) {
		t.Fatalf("serve with no: err = %v, want errKeyPromptDeclined", promptErr)
	}

	if _, statErr := os.Stat(wantPath); !os.IsNotExist(statErr) {
		t.Fatal("serve with no made the key file, want no file")
	}

	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatal("serve with no opened the database, want it to stop before the database opens")
	}
}

//nolint:paralleltest // serve reads process environment, so this test runs alone.
func TestServeKeyExistingFileIsNeverOverwritten(t *testing.T) {
	clearKeyEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")
	keyPath := filepath.Join(dir, "dustpan.key")

	before := []byte(testKeyBase64())

	if writeErr := os.WriteFile(keyPath, before, keyFileMode); writeErr != nil {
		t.Fatalf("write existing key file: %v", writeErr)
	}

	var logs lockedBuffer

	args := []string{
		"--database", "sqlite:" + dbPath,
		"--listen", "127.0.0.1:0",
		"--generate-key-encryption-key",
	}

	if startErr := startServerWithIO(t, "yes\n", true, &logs, args...); startErr != nil {
		t.Fatalf("serve with an existing key file: %v", startErr)
	}

	after, readErr := os.ReadFile(keyPath) //nolint:gosec // The test reads the key file it wrote.
	if readErr != nil {
		t.Fatalf("read key file after start: %v", readErr)
	}

	if !bytes.Equal(after, before) {
		t.Fatal("serve overwrote the existing key file, want it unchanged")
	}
}

// TestMakeKeyFileLostRaceUsesExistingFile proves a lost creation race answers
// with the file that won: makeKeyFile reads the existing file instead of
// failing when the file appears between the existence check and creation.
func TestMakeKeyFileLostRaceUsesExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "dustpan.key")

	before := []byte(testKeyBase64())

	if writeErr := os.WriteFile(keyPath, before, keyFileMode); writeErr != nil {
		t.Fatalf("write existing key file: %v", writeErr)
	}

	var logs bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&logs, nil))

	if _, err := makeKeyFile(keyPath, logger); err != nil {
		t.Fatalf("make key file with a concurrent file: %v", err)
	}

	after, readErr := os.ReadFile(keyPath) //nolint:gosec // The test reads the key file it wrote.
	if readErr != nil {
		t.Fatalf("read key file after make: %v", readErr)
	}

	if !bytes.Equal(after, before) {
		t.Fatal("make overwrote the existing key file, want it unchanged")
	}

	if !strings.Contains(logs.String(), keyPath) {
		t.Fatalf("make output lacks the key file path %q:\n%s", keyPath, logs.String())
	}

	if strings.Contains(logs.String(), strings.TrimSpace(string(before))) {
		t.Fatal("make output holds the key")
	}
}

// TestPromptForKeyFileOverLongAnswerIsNo proves the prompt read is capped: an
// answer longer than the limit counts as not yes.
func TestPromptForKeyFileOverLongAnswerIsNo(t *testing.T) {
	t.Parallel()

	agreed, err := promptForKeyFile(
		strings.NewReader(strings.Repeat("y", maxKeyAnswerBytes+1)),
		io.Discard,
		"/tmp/dustpan.key",
	)
	if err != nil {
		t.Fatalf("prompt with an over-long answer: %v", err)
	}

	if agreed {
		t.Fatal("prompt with an over-long answer agreed, want not yes")
	}
}

func TestServeKeyOptInWithoutTerminal(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv(generateKeyEnvVar, "true")

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")
	wantPath := filepath.Join(dir, "dustpan.key")

	var logs lockedBuffer

	args := []string{"--database", "sqlite:" + dbPath, "--listen", "127.0.0.1:0"}

	if err := startServerWithIO(t, "", false, &logs, args...); err != nil {
		t.Fatalf("serve with the opt-in: %v", err)
	}

	checkMadeKeyFile(t, wantPath)

	if !strings.Contains(logs.String(), wantPath) {
		t.Fatalf("serve output lacks the key file path %q:\n%s", wantPath, logs.String())
	}
}

//nolint:paralleltest // serve reads process environment, so this test runs alone.
func TestServeKeyNoTerminalNoOptIn(t *testing.T) {
	clearKeyEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")

	var logs lockedBuffer

	serveErr := serveWithIO(
		t.Context(),
		[]string{"--database", "sqlite:" + dbPath},
		strings.NewReader(""),
		&logs,
		false,
	)
	if serveErr == nil || !errors.Is(serveErr, errKeyGenerateRequired) {
		t.Fatalf("serve without a terminal or the opt-in: err = %v, want errKeyGenerateRequired", serveErr)
	}

	for _, want := range []string{
		"--generate-key-encryption-key",
		"DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY",
		"--key-encryption-key-file",
		"DUSTPAN_KEY_ENCRYPTION_KEY_FILE",
		"DUSTPAN_KEY_ENCRYPTION_KEY",
	} {
		if !strings.Contains(serveErr.Error(), want) {
			t.Fatalf("serve error = %q, want it to name %s", serveErr, want)
		}
	}

	if _, statErr := os.Stat(filepath.Join(dir, "dustpan.key")); !os.IsNotExist(statErr) {
		t.Fatal("serve made the key file silently, want no file")
	}

	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatal("serve opened the database, want it to stop before the database opens")
	}
}

func TestServeKeyRestartOpensWrappedKeys(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv(generateKeyEnvVar, "true")

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")
	keyPath := filepath.Join(dir, "dustpan.key")

	var first lockedBuffer

	args := []string{"--database", "sqlite:" + dbPath, "--listen", "127.0.0.1:0"}

	if err := startServerWithIO(t, "", false, &first, args...); err != nil {
		t.Fatalf("first serve with the opt-in: %v", err)
	}

	key := readMadeKey(t, keyPath)
	seedMainKey(t, dbPath, key)

	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

	if err := os.Unsetenv(generateKeyEnvVar); err != nil {
		t.Fatalf("unset %s: %v", generateKeyEnvVar, err)
	}

	var second lockedBuffer

	if err := startServerWithIO(t, "", false, &second, args...); err != nil {
		t.Fatalf("restart with the made file: %v", err)
	}

	if !strings.Contains(second.String(), keyPath) {
		t.Fatalf("restart output lacks the key file path %q:\n%s", keyPath, second.String())
	}

	keyBase64 := base64.StdEncoding.EncodeToString(key)

	if strings.Contains(second.String(), keyBase64) {
		t.Fatal("restart output holds the key")
	}
}

func TestUpgradeCheckWarnsWithoutKeyWhenStateExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")

	seedUpgradeOpenTofuState(t, dir, dbPath)
	unsetUpgradeKeyEnv(t)

	out, checkErr := runUpgradeCheck(t, "sqlite:"+dbPath)
	if checkErr != nil {
		t.Fatalf("upgrade check with state and no key: %v", checkErr)
	}

	for _, want := range []string{
		"OpenTofu state",
		"--generate-key-encryption-key",
		"DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY",
		"--key-encryption-key-file",
		keyFileEnvVar,
		keyEnvVar,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("upgrade check output lacks %q:\n%s", want, out)
		}
	}

	t.Setenv(keyEnvVar, testKeyBase64())

	keyedOut, keyedErr := runUpgradeCheck(t, "sqlite:"+dbPath)
	if keyedErr != nil {
		t.Fatalf("upgrade check with state and a key: %v", keyedErr)
	}

	if strings.Contains(keyedOut, "OpenTofu state") {
		t.Fatalf("upgrade check with a key warns about state:\n%s", keyedOut)
	}
}

// seedUpgradeOpenTofuState stores one wrapped key and one finalized OpenTofu
// state version in the SQLite database at dbPath with blobs in dir.
func seedUpgradeOpenTofuState(t *testing.T, dir, dbPath string) {
	t.Helper()

	seedMainKey(t, dbPath, []byte(testKeyString))

	st, err := sqlite.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	}()

	blobs, blobErr := blobfs.Open(filepath.Join(dir, "blobs"))
	if blobErr != nil {
		t.Fatalf("open blobs: %v", blobErr)
	}

	defer func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blobs: %v", closeErr)
		}
	}()

	namespaces, nsErr := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "local"})
	if nsErr != nil {
		t.Fatalf("namespace service: %v", nsErr)
	}

	seedRestoreState(t.Context(), t, st, blobs, namespaces)
}

// unsetUpgradeKeyEnv clears the key settings, so the upgrade check sees an
// installation with OpenTofu state and no key.
func unsetUpgradeKeyEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{keyEnvVar, keyFileEnvVar} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
}

func TestDefaultKeyFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, database, flag, want string
	}{
		{"explicit flag wins", "sqlite:/data/dustpan.db", "/custom/dustpan.key", "/custom/dustpan.key"},
		{"sqlite next to file", "sqlite:/data/dustpan.db", "", "/data/dustpan.key"},
		{"sqlite bare filename", "sqlite:dustpan.db", "", "dustpan.key"},
		{"postgres uses working directory", "postgres://user@localhost/db", "", "dustpan.key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := resolveKeyFile(tc.database, tc.flag); got != tc.want {
				t.Fatalf("resolveKeyFile(%q, %q) = %q, want %q", tc.database, tc.flag, got, tc.want)
			}
		})
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it and runs alone.
func TestServeKeyFlagsHelp(t *testing.T) {
	clearKeyEnv(t)

	var stderr bytes.Buffer

	err := serveWithIO(t.Context(), []string{"--help"}, strings.NewReader(""), &stderr, false)
	if err == nil {
		t.Fatal("serve --help succeeded, want the help request")
	}

	out := stderr.String()

	for _, want := range []string{
		"generate-key-encryption-key",
		"key-encryption-key-file",
		"loses",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("serve --help lacks %q:\n%s", want, out)
		}
	}
}
