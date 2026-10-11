package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// backupTestID is the backup ID the fake server starts.
const backupTestID = "bkp-test00000000000001"

// backupScript holds the scripted rows of one fake backup server: the
// status of the start and the states each read answers in order.
type backupScript struct {
	startStatus int
	startBody   string
	states      []backupStatusResponse
}

// backupFakeServer answers the backup routes from script: POST starts the
// backup, GET reads the next scripted state. It checks the Bearer
// credential on every request and records the token it saw.
type backupFakeServer struct {
	server *httptest.Server
	token  atomic.Value
	reads  atomic.Int64
	script backupScript
}

// newBackupFakeServer starts the fake server for script, expecting token
// as the Bearer credential.
func newBackupFakeServer(t *testing.T, token string, script backupScript) *backupFakeServer {
	t.Helper()

	fake := &backupFakeServer{
		server: nil,
		token:  atomic.Value{},
		reads:  atomic.Int64{},
		script: script,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/backups", fake.startHandler(t, token))
	mux.HandleFunc("GET /api/server/backups/{id}", fake.readHandler(t, token))

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// writeBackupFakeError answers 401 with the one JSON error shape.
func writeBackupFakeError(t *testing.T, w http.ResponseWriter) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	if err := json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "unauthenticated"}); err != nil {
		t.Errorf("write 401 body: %v", err)
	}
}

// startHandler answers the backup start from the script, checking the
// Bearer credential.
func (f *backupFakeServer) startHandler(t *testing.T, token string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		f.token.Store(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))

		if r.Header.Get("Authorization") != "Bearer "+token {
			writeBackupFakeError(t, w)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.script.startStatus)

		if _, err := io.WriteString(w, f.script.startBody); err != nil {
			t.Errorf("write start body: %v", err)
		}
	}
}

// readHandler answers the next scripted state, checking the Bearer
// credential and the backup ID.
func (f *backupFakeServer) readHandler(t *testing.T, token string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		f.token.Store(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))

		if r.Header.Get("Authorization") != "Bearer "+token {
			writeBackupFakeError(t, w)

			return
		}

		if got := r.PathValue("id"); got != backupTestID {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		if len(f.script.states) == 0 {
			t.Error("no scripted state for a backup read")

			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		n := f.reads.Add(1) - 1
		state := f.script.states[len(f.script.states)-1]

		if int(n) < len(f.script.states) {
			state = f.script.states[n]
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if err := json.NewEncoder(w).Encode(state); err != nil {
			t.Errorf("write backup state: %v", err)
		}
	}
}

// succeededScript returns the script of a backup that runs once, then
// succeeds with a manifest holding sqlSize bytes.
func succeededScript(sqlSize int64) backupScript {
	return backupScript{
		startStatus: http.StatusAccepted,
		startBody:   `{"id":"` + backupTestID + `"}`,
		states: []backupStatusResponse{
			{ID: backupTestID, State: "running", Error: "", Manifest: nil},
			{ID: backupTestID, State: "succeeded", Error: "", Manifest: &backupManifest{SQLSize: sqlSize}},
		},
	}
}

// runBackupCommand runs `backup` against server with the token in
// DUSTPAN_ADMIN_TOKEN and returns its stdout and error. A nil error means
// exit code 0, a non-nil error means exit code 1.
func runBackupCommand(t *testing.T, server, token string, extra ...string) (string, error) {
	t.Helper()
	t.Setenv(adminTokenEnvVar, token)

	args := append([]string{"--server", server}, extra...)

	var stdout, stderr bytes.Buffer

	err := backupCommand(t.Context(), args, &stdout, &stderr)

	return stdout.String(), err
}

// checkBackupOutput proves that out names the backup ID, the state and
// the key and plain-text warnings.
func checkBackupOutput(t *testing.T, out, state string) {
	t.Helper()

	for _, want := range []string{
		"backup " + backupTestID + " " + state,
		"DUSTPAN_KEY_ENCRYPTION_KEY",
		"plain text",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("backup output lacks %q:\n%s", want, out)
		}
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCommandSuccess(t *testing.T) {
	fake := newBackupFakeServer(t, "test-token", succeededScript(12345))

	out, err := runBackupCommand(t, fake.server.URL, "test-token")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	checkBackupOutput(t, out, "succeeded")

	if !strings.Contains(out, "12345") {
		t.Fatalf("backup output lacks the SQL size:\n%s", out)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCommandUsesFirstToken(t *testing.T) {
	fake := newBackupFakeServer(t, "first-token", succeededScript(99))

	out, err := runBackupCommand(t, fake.server.URL, "first-token,second-token")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}

	checkBackupOutput(t, out, "succeeded")

	got, ok := fake.token.Load().(string)
	if !ok || got != "first-token" {
		t.Fatalf("server saw token %q, want the first entry first-token", got)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCommandFailedBackup(t *testing.T) {
	script := backupScript{
		startStatus: http.StatusAccepted,
		startBody:   `{"id":"` + backupTestID + `"}`,
		states: []backupStatusResponse{
			{ID: backupTestID, State: "failed", Error: "target is gone", Manifest: nil},
		},
	}
	fake := newBackupFakeServer(t, "test-token", script)

	out, err := runBackupCommand(t, fake.server.URL, "test-token")
	if err == nil {
		t.Fatal("failed backup succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), backupTestID) || !strings.Contains(err.Error(), "target is gone") {
		t.Fatalf("backup error = %v, want it to name the backup and its failure", err)
	}

	if !strings.Contains(err.Error(), "opendustpan backup") {
		t.Fatalf("backup error = %v, want it to name the one command that helps", err)
	}

	checkBackupOutput(t, out, "failed")
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCommandStartConflict(t *testing.T) {
	script := backupScript{
		startStatus: http.StatusConflict,
		startBody:   `{"code":409,"message":"a backup is already running"}`,
		states:      nil,
	}
	fake := newBackupFakeServer(t, "test-token", script)

	_, err := runBackupCommand(t, fake.server.URL, "test-token")
	if err == nil {
		t.Fatal("conflicting start succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("backup error = %v, want it to name the running backup", err)
	}

	if !strings.Contains(err.Error(), "opendustpan backup") {
		t.Fatalf("backup error = %v, want it to name the one command that helps", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCommandNeedsToken(t *testing.T) {
	if err := os.Unsetenv(adminTokenEnvVar); err != nil {
		t.Fatalf("unset %s: %v", adminTokenEnvVar, err)
	}

	var stdout, stderr bytes.Buffer

	err := backupCommand(
		t.Context(),
		[]string{"--server", "http://127.0.0.1:1"},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("backup without a token succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), adminTokenEnvVar) {
		t.Fatalf("backup error = %v, want it to name %s", err, adminTokenEnvVar)
	}

	if !strings.Contains(err.Error(), "opendustpan backup") {
		t.Fatalf("backup error = %v, want it to name the one command that helps", err)
	}
}

func TestBackupCommandRejectsTokenFlag(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	var stdout, stderr bytes.Buffer

	err := backupCommand(
		t.Context(),
		[]string{"--server", "http://127.0.0.1:1", "--token", "test-token"},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("backup with --token succeeded, want an error: the token comes from the environment only")
	}
}

func TestBackupCommandNeedsServer(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	if err := os.Unsetenv(backupServerEnvVar); err != nil {
		t.Fatalf("unset %s: %v", backupServerEnvVar, err)
	}

	var stdout, stderr bytes.Buffer

	err := backupCommand(t.Context(), nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("backup without --server succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "--server") {
		t.Fatalf("backup error = %v, want it to name --server", err)
	}
}

func TestBackupCommandServerFromEnvironment(t *testing.T) {
	fake := newBackupFakeServer(t, "test-token", succeededScript(7))

	t.Setenv(adminTokenEnvVar, "test-token")
	t.Setenv(backupServerEnvVar, fake.server.URL)

	var stdout, stderr bytes.Buffer

	if err := backupCommand(t.Context(), nil, &stdout, &stderr); err != nil {
		t.Fatalf("backup with %s: %v", backupServerEnvVar, err)
	}

	checkBackupOutput(t, stdout.String(), "succeeded")
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestBackupCommandBadCAFile(t *testing.T) {
	missing := "/nonexistent-dir/missing-ca.pem"

	_, err := runBackupCommand(t, "http://127.0.0.1:1", "test-token", "--ca-file", missing)
	if err == nil {
		t.Fatal("backup with a missing CA file succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("backup error = %v, want it to name %s", err, missing)
	}
}

func TestBackupCommandUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	err := backupCommand(t.Context(), []string{"extra"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("backup with a positional argument succeeded, want the usage error")
	}
}
