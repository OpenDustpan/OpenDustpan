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

// restoreTestID is the restore ID the fake server starts.
const restoreTestID = "rst-test00000000000001"

// restoreBackupTestID is the backup ID the fake server restores.
const restoreBackupTestID = "bkp-test00000000000001"

// restorePreTestID is the backup taken before the fake restore.
const restorePreTestID = "bkp-pre000000000000001"

// restoreScript holds the scripted rows of one fake restore server: the
// status of the start and the states each read answers in order.
type restoreScript struct {
	startStatus int
	startBody   string
	states      []restoreStatusResponse
}

// restoreFakeServer answers the restore routes from script: POST starts the
// restore, GET reads the next scripted state. It checks the Bearer
// credential on every request and records the start body it saw.
type restoreFakeServer struct {
	server *httptest.Server
	reads  atomic.Int64
	start  atomic.Value
	script restoreScript
}

// newRestoreFakeServer starts the fake server for script, expecting token
// as the Bearer credential.
func newRestoreFakeServer(t *testing.T, token string, script restoreScript) *restoreFakeServer {
	t.Helper()

	fake := &restoreFakeServer{
		server: nil,
		reads:  atomic.Int64{},
		start:  atomic.Value{},
		script: script,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/restores", fake.startHandler(t, token))
	mux.HandleFunc("GET /api/server/restores/{id}", fake.readHandler(t, token))

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// startHandler answers the restore start from the script, checking the
// Bearer credential and recording the start body.
func (f *restoreFakeServer) startHandler(t *testing.T, token string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			writeBackupFakeError(t, w)

			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read start body: %v", err)
		}

		f.start.Store(string(body))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.script.startStatus)

		if _, err := io.WriteString(w, f.script.startBody); err != nil {
			t.Errorf("write start body: %v", err)
		}
	}
}

// readHandler answers the next scripted state, checking the Bearer
// credential and the restore ID.
func (f *restoreFakeServer) readHandler(t *testing.T, token string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			writeBackupFakeError(t, w)

			return
		}

		if got := r.PathValue("id"); got != restoreTestID {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		if len(f.script.states) == 0 {
			t.Error("no scripted state for a restore read")

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
			t.Errorf("write restore state: %v", err)
		}
	}
}

// succeededRestoreScript returns the script of a restore that checks, then
// succeeds with the pre-backup ID and a manifest holding sqlSize bytes.
func succeededRestoreScript(sqlSize int64) restoreScript {
	return restoreScript{
		startStatus: http.StatusAccepted,
		startBody:   `{"id":"` + restoreTestID + `"}`,
		states: []restoreStatusResponse{
			{
				ID:          restoreTestID,
				State:       "checking",
				BackupID:    restoreBackupTestID,
				PreBackupID: "",
				Error:       "",
				ErrorCode:   "",
				Manifest:    nil,
				Warnings:    nil,
				DevData:     nil,
			},
			{
				ID:          restoreTestID,
				State:       "applying",
				BackupID:    restoreBackupTestID,
				PreBackupID: restorePreTestID,
				Error:       "",
				ErrorCode:   "",
				Manifest:    nil,
				Warnings:    nil,
				DevData:     nil,
			},
			{
				ID:          restoreTestID,
				State:       "succeeded",
				BackupID:    restoreBackupTestID,
				PreBackupID: restorePreTestID,
				Error:       "",
				ErrorCode:   "",
				Manifest:    &restoreManifest{SQLSize: sqlSize},
				Warnings:    nil,
				DevData:     nil,
			},
		},
	}
}

// runRestoreClient runs `restore` against server with the token in
// DUSTPAN_ADMIN_TOKEN and returns its stdout and error. A nil error means
// exit code 0, a non-nil error means exit code 1.
func runRestoreClient(t *testing.T, server, token string, extra ...string) (string, error) {
	t.Helper()
	t.Setenv(adminTokenEnvVar, token)

	args := append([]string{"--server", server, "--backup", restoreBackupTestID}, extra...)

	var stdout, stderr bytes.Buffer

	err := restoreCommand(t.Context(), args, &stdout, &stderr)

	return stdout.String(), err
}

// checkRestoreOutput proves that out names the restore ID, the state and the
// backup taken before it.
func checkRestoreOutput(t *testing.T, out, state string) {
	t.Helper()

	for _, want := range []string{
		"restore " + restoreTestID + " " + state,
		"backup before restore: " + restorePreTestID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("restore output lacks %q:\n%s", want, out)
		}
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandSuccess(t *testing.T) {
	fake := newRestoreFakeServer(t, "test-token", succeededRestoreScript(12345))

	out, err := runRestoreClient(t, fake.server.URL, "test-token")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	checkRestoreOutput(t, out, "succeeded")

	if !strings.Contains(out, "12345") {
		t.Fatalf("restore output lacks the SQL size:\n%s", out)
	}

	var seen restoreStartRequest

	raw, ok := fake.start.Load().(string)
	if !ok {
		t.Fatal("server saw no start body")
	}

	if err := json.Unmarshal([]byte(raw), &seen); err != nil {
		t.Fatalf("decode start body %q: %v", raw, err)
	}

	if seen.BackupID != restoreBackupTestID || seen.CheckOnly || seen.Force {
		t.Fatalf("start body = %+v, want only the backup ID", seen)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandCheckOnly(t *testing.T) {
	script := restoreScript{
		startStatus: http.StatusAccepted,
		startBody:   `{"id":"` + restoreTestID + `"}`,
		states: []restoreStatusResponse{
			{
				ID:          restoreTestID,
				State:       "checking",
				BackupID:    restoreBackupTestID,
				PreBackupID: "",
				Error:       "",
				ErrorCode:   "",
				Manifest:    nil,
				Warnings:    nil,
				DevData:     nil,
			},
			{
				ID:          restoreTestID,
				State:       "succeeded",
				BackupID:    restoreBackupTestID,
				PreBackupID: "",
				Error:       "",
				ErrorCode:   "",
				Manifest:    nil,
				Warnings:    nil,
				DevData:     nil,
			},
		},
	}
	fake := newRestoreFakeServer(t, "check-token", script)

	out, err := runRestoreClient(t, fake.server.URL, "check-token", "--check-only")
	if err != nil {
		t.Fatalf("restore --check-only: %v", err)
	}

	if !strings.Contains(out, "restore "+restoreTestID+" succeeded") {
		t.Fatalf("restore output lacks the succeeded state:\n%s", out)
	}

	if !strings.Contains(out, "check only") {
		t.Fatalf("restore output lacks the check-only line:\n%s", out)
	}

	if strings.Contains(out, "backup before restore") {
		t.Fatalf("check-only restore names a pre-backup:\n%s", out)
	}

	var seen restoreStartRequest

	raw, ok := fake.start.Load().(string)
	if !ok {
		t.Fatal("server saw no start body")
	}

	if err := json.Unmarshal([]byte(raw), &seen); err != nil {
		t.Fatalf("decode start body %q: %v", raw, err)
	}

	if !seen.CheckOnly || seen.Force {
		t.Fatalf("start body = %+v, want check-only without force", seen)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandForce(t *testing.T) {
	fake := newRestoreFakeServer(t, "force-token", succeededRestoreScript(7))

	out, err := runRestoreClient(t, fake.server.URL, "force-token", "--force")
	if err != nil {
		t.Fatalf("restore --force: %v", err)
	}

	checkRestoreOutput(t, out, "succeeded")

	var seen restoreStartRequest

	raw, ok := fake.start.Load().(string)
	if !ok {
		t.Fatal("server saw no start body")
	}

	if err := json.Unmarshal([]byte(raw), &seen); err != nil {
		t.Fatalf("decode start body %q: %v", raw, err)
	}

	if !seen.Force || seen.CheckOnly {
		t.Fatalf("start body = %+v, want force without check-only", seen)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandFailedRestore(t *testing.T) {
	script := restoreScript{
		startStatus: http.StatusAccepted,
		startBody:   `{"id":"` + restoreTestID + `"}`,
		states: []restoreStatusResponse{
			{
				ID:          restoreTestID,
				State:       "failed",
				BackupID:    restoreBackupTestID,
				PreBackupID: restorePreTestID,
				Error:       "backup is corrupt",
				ErrorCode:   "",
				Manifest:    nil,
				Warnings:    nil,
				DevData:     nil,
			},
		},
	}
	fake := newRestoreFakeServer(t, "test-token", script)

	out, err := runRestoreClient(t, fake.server.URL, "test-token")
	if err == nil {
		t.Fatal("failed restore succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), restoreTestID) || !strings.Contains(err.Error(), "backup is corrupt") {
		t.Fatalf("restore error = %v, want it to name the restore and its failure", err)
	}

	if !strings.Contains(err.Error(), "opendustpan restore") {
		t.Fatalf("restore error = %v, want it to name the one command that helps", err)
	}

	checkRestoreOutput(t, out, "failed")
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandActiveWorkNamesForce(t *testing.T) {
	const activeWork = "cannot enter maintenance mode while work is active: opentofu:acme/b"

	tests := []struct {
		name       string
		extra      []string
		row        string
		wantSuffix func(server string) string
	}{
		{
			name:  "active work without force",
			extra: nil,
			row:   activeWork,
			wantSuffix: func(server string) string {
				return ": end that work, or run opendustpan restore --server " + server +
					" --backup " + restoreBackupTestID + " --force to cancel it"
			},
		},
		{
			name:  "active work with force",
			extra: []string{"--force"},
			row:   activeWork,
			wantSuffix: func(server string) string {
				return ": run opendustpan restore --server " + server + " --backup " + restoreBackupTestID + " again"
			},
		},
		{
			name:  "other failure",
			extra: nil,
			row:   "backup is corrupt",
			wantSuffix: func(server string) string {
				return ": run opendustpan restore --server " + server + " --backup " + restoreBackupTestID + " again"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := restoreScript{
				startStatus: http.StatusAccepted,
				startBody:   `{"id":"` + restoreTestID + `"}`,
				states: []restoreStatusResponse{
					{
						ID:          restoreTestID,
						State:       "failed",
						BackupID:    restoreBackupTestID,
						PreBackupID: restorePreTestID,
						Error:       tt.row,
						ErrorCode:   "",
						Manifest:    nil,
						Warnings:    nil,
						DevData:     nil,
					},
				},
			}
			fake := newRestoreFakeServer(t, "test-token", script)

			_, err := runRestoreClient(t, fake.server.URL, "test-token", tt.extra...)
			if err == nil {
				t.Fatal("failed restore succeeded, want an error for exit code 1")
			}

			want := tt.wantSuffix(fake.server.URL)
			if !strings.HasSuffix(err.Error(), want) {
				t.Fatalf("restore error = %v, want it to end with %q", err, want)
			}
		})
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandStartConflict(t *testing.T) {
	script := restoreScript{
		startStatus: http.StatusConflict,
		startBody:   `{"code":409,"message":"a restore is already running"}`,
		states:      nil,
	}
	fake := newRestoreFakeServer(t, "test-token", script)

	_, err := runRestoreClient(t, fake.server.URL, "test-token")
	if err == nil {
		t.Fatal("conflicting start succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("restore error = %v, want it to name the running restore", err)
	}

	if strings.Contains(err.Error(), "again") {
		t.Fatalf("restore error = %v, want no retry hint for a refused request", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandNeedsToken(t *testing.T) {
	if err := os.Unsetenv(adminTokenEnvVar); err != nil {
		t.Fatalf("unset %s: %v", adminTokenEnvVar, err)
	}

	var stdout, stderr bytes.Buffer

	err := restoreCommand(
		t.Context(),
		[]string{"--server", "http://127.0.0.1:1", "--backup", restoreBackupTestID},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("restore without a token succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), adminTokenEnvVar) {
		t.Fatalf("restore error = %v, want it to name %s", err, adminTokenEnvVar)
	}

	if !strings.Contains(err.Error(), "opendustpan restore") {
		t.Fatalf("restore error = %v, want it to name the one command that helps", err)
	}
}

func TestRestoreCommandRejectsTokenFlag(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	var stdout, stderr bytes.Buffer

	err := restoreCommand(
		t.Context(),
		[]string{"--server", "http://127.0.0.1:1", "--backup", restoreBackupTestID, "--token", "test-token"},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("restore with --token succeeded, want an error: the token comes from the environment only")
	}
}

func TestRestoreCommandNeedsServer(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	if err := os.Unsetenv(backupServerEnvVar); err != nil {
		t.Fatalf("unset %s: %v", backupServerEnvVar, err)
	}

	var stdout, stderr bytes.Buffer

	err := restoreCommand(t.Context(), []string{"--backup", restoreBackupTestID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("restore without --server succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "--server") {
		t.Fatalf("restore error = %v, want it to name --server", err)
	}
}

func TestRestoreCommandNeedsBackup(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	var stdout, stderr bytes.Buffer

	err := restoreCommand(t.Context(), []string{"--server", "http://127.0.0.1:1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("restore without --backup succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "--backup") {
		t.Fatalf("restore error = %v, want it to name --backup", err)
	}
}

func TestRestoreCommandServerFromEnvironment(t *testing.T) {
	fake := newRestoreFakeServer(t, "test-token", succeededRestoreScript(7))

	t.Setenv(adminTokenEnvVar, "test-token")
	t.Setenv(backupServerEnvVar, fake.server.URL)

	var stdout, stderr bytes.Buffer

	if err := restoreCommand(t.Context(), []string{"--backup", restoreBackupTestID}, &stdout, &stderr); err != nil {
		t.Fatalf("restore with %s: %v", backupServerEnvVar, err)
	}

	checkRestoreOutput(t, stdout.String(), "succeeded")
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandBadCAFile(t *testing.T) {
	missing := "/nonexistent-dir/missing-ca.pem"

	_, err := runRestoreClient(t, "http://127.0.0.1:1", "test-token", "--ca-file", missing)
	if err == nil {
		t.Fatal("restore with a missing CA file succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("restore error = %v, want it to name %s", err, missing)
	}
}

// TestRestoreCommandRejectsOfflineFlags proves the offline flags are gone:
// the server reads its own target and stores, so --from, --database,
// --blob-dir and --blob-store are usage errors.
func TestRestoreCommandRejectsOfflineFlags(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--from", "--database", "--blob-dir", "--blob-store"} {
		var stdout, stderr bytes.Buffer

		err := restoreCommand(
			t.Context(),
			[]string{"--server", "http://127.0.0.1:1", "--backup", restoreBackupTestID, flag, "x"},
			&stdout,
			&stderr,
		)
		if err == nil {
			t.Fatalf("restore with %s succeeded, want a flag error", flag)
		}
	}
}

func TestRestoreCommandUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	err := restoreCommand(t.Context(), []string{"extra"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("restore with a positional argument succeeded, want the usage error")
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandReadMissingHasNoRetryHint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/restores", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)

		if _, err := io.WriteString(w, `{"id":"`+restoreTestID+`"}`); err != nil {
			t.Errorf("write start body: %v", err)
		}
	})
	mux.HandleFunc("GET /api/server/restores/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)

		body := `{"code": 404, "message": "restore not found"}`

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write 404 body: %v", err)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err := runRestoreClient(t, server.URL, "test-token")
	if err == nil {
		t.Fatal("restore with a missing row succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "read restore failed "+restoreTestID) {
		t.Fatalf("restore error = %v, want it to name the restore ID", err)
	}

	if strings.Contains(err.Error(), "again") {
		t.Fatalf("restore error = %v, want no retry hint for a 404 answer", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestRestoreCommandRetriesTransientMissing(t *testing.T) {
	succeeded := restoreStatusResponse{
		ID:          restoreTestID,
		State:       "succeeded",
		BackupID:    restoreBackupTestID,
		PreBackupID: restorePreTestID,
		Error:       "",
		ErrorCode:   "",
		Manifest:    nil,
		Warnings:    nil,
		DevData:     nil,
	}
	checking := restoreStatusResponse{
		ID:          restoreTestID,
		State:       "checking",
		BackupID:    restoreBackupTestID,
		PreBackupID: "",
		Error:       "",
		ErrorCode:   "",
		Manifest:    nil,
		Warnings:    nil,
		DevData:     nil,
	}

	var reads atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/restores", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)

		if _, err := io.WriteString(w, `{"id":"`+restoreTestID+`"}`); err != nil {
			t.Errorf("write start body: %v", err)
		}
	})
	mux.HandleFunc("GET /api/server/restores/{id}", func(w http.ResponseWriter, _ *http.Request) {
		n := reads.Add(1)

		w.Header().Set("Content-Type", "application/json")

		switch n {
		case 1:
			w.WriteHeader(http.StatusOK)

			if err := json.NewEncoder(w).Encode(checking); err != nil {
				t.Errorf("write checking state: %v", err)
			}
		case 2:
			w.WriteHeader(http.StatusNotFound)

			body := `{"code": 404, "message": "restore not found"}`

			if _, err := io.WriteString(w, body); err != nil {
				t.Errorf("write 404 body: %v", err)
			}
		default:
			w.WriteHeader(http.StatusOK)

			if err := json.NewEncoder(w).Encode(succeeded); err != nil {
				t.Errorf("write succeeded state: %v", err)
			}
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	out, err := runRestoreClient(t, server.URL, "test-token")
	if err != nil {
		t.Fatalf("restore with a transient missing row: %v", err)
	}

	checkRestoreOutput(t, out, "succeeded")
}

// TestRestoreCommandWarnsAboutADevBackup proves that the command reads the
// manifest first and prints the notice before it starts the restore, and that
// it prints the warnings and the dev data that the server answers.
func TestRestoreCommandWarnsAboutADevBackup(t *testing.T) {
	var order []string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server/backups/{id}", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "manifest")

		writeFake(t, w,
			`{"id":"`+restoreBackupTestID+`","state":"succeeded","manifest":{"installationKind":"dev"}}`,
		)
	})
	mux.HandleFunc("POST /api/server/restores", func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "start")

		w.WriteHeader(http.StatusAccepted)

		writeFake(t, w,
			`{"id":"`+restoreTestID+`","warnings":[{"code":"dev-backup-into-production","text":"first text"}]}`,
		)
	})
	mux.HandleFunc("GET /api/server/restores/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeFake(t, w,
			`{"id":"`+restoreTestID+`","state":"succeeded","backupID":"`+restoreBackupTestID+`",`+
				`"warnings":[{"code":"dev-backup-into-production","text":"first text"},{"code":"audit-append-failed","text":"second text"}],`+
				`"devData":{"principals":2,"classes":{"grants":3,"principals":2}}}`)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	t.Setenv(adminTokenEnvVar, "dev-token")

	var stdout, stderr bytes.Buffer

	args := []string{"--server", server.URL, "--backup", restoreBackupTestID}
	if err := restoreCommand(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if len(order) < 2 || order[0] != "manifest" || order[1] != "start" {
		t.Fatalf("calls = %v, want the manifest read before the start", order)
	}

	if !strings.Contains(stderr.String(), "this backup is from a dev installation") ||
		!strings.Contains(stderr.String(), "--unsafe-allow-restoring-dev-installation-backups-into-production") {
		t.Fatalf("stderr = %q, want the notice that names the flag", stderr.String())
	}

	out := stdout.String()
	if strings.Count(out, "warning: first text") != 1 || !strings.Contains(out, "warning: second text") ||
		!strings.Contains(out, "dev data: 2 principals; grants 3, principals 2") {
		t.Fatalf("stdout = %q, want each warning once and the dev data line", out)
	}
}

// writeFake writes the scripted body of a fake server answer.
func writeFake(t *testing.T, w io.Writer, body string) {
	t.Helper()

	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write fake answer: %v", err)
	}
}
