package client_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// serveBackupRow answers a backup start with an ID and every read of the
// backup with row.
func serveBackupRow(t *testing.T, row string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)

			_, _ = w.Write([]byte(`{"id":"b1"}`)) //nolint:errcheck // The client may close early.

			return
		}

		_, _ = w.Write([]byte(row)) //nolint:errcheck // The client may close early on an oversized body.
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestBackupReadsTheManifestWithItsOwnCap proves the cap of the backup row
// read: the row embeds the full manifest, so it may go above
// MaxClientBodyBytes, up to MaxManifestAnswerBytes, and a larger answer is
// refused. The test sets the admin token, so it cannot run in parallel.
func TestBackupReadsTheManifestWithItsOwnCap(t *testing.T) {
	const head = `{"id":"b1","state":"succeeded","manifest":{"pad":"`

	// rowOf makes a backup row of exactly size bytes.
	rowOf := func(size int) string {
		return head + strings.Repeat("x", size-len(head)-len(`"}}`)) + `"}}`
	}

	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "above the small cap", size: 2 * client.MaxClientBodyBytes, wantErr: false},
		{name: "exactly the manifest cap", size: client.MaxManifestAnswerBytes, wantErr: false},
		{name: "manifest cap plus one byte", size: client.MaxManifestAnswerBytes + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := rowOf(tt.size)

			srv := serveBackupRow(t, row)

			t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

			var stdout, stderr bytes.Buffer

			err := client.BackupCommand(t.Context(), []string{"--server", srv.URL}, &stdout, &stderr)

			tooLarge := errors.Is(err, client.ErrResponseTooLarge) && errors.Is(err, client.ErrBackupRead)
			if tooLarge != tt.wantErr || (err != nil) != tt.wantErr {
				t.Fatalf("backup = %.200v; want too large = %v", err, tt.wantErr)
			}
		})
	}
}

// TestBackupReportRegistryFiles proves the registry file report of the
// backup command: the row holds the registry file sections of the manifest,
// and the command prints the count of the copied files and their total bytes.
// A manifest with no section prints the report of zero files. The test sets
// the admin token, so it cannot run in parallel.
func TestBackupReportRegistryFiles(t *testing.T) {
	tests := []struct {
		name string
		row  string
		want string
	}{
		{
			name: "one file",
			row: `{"id":"b1","state":"succeeded","manifest":{"sqlSize":10,` +
				`"registryFiles":{"ns-1":{"version":1,"files":[` +
				`{"sha256":"a","size":7,"release":"net","version":"1.0.0"}]}}}}`,
			want: "registry files: 1 files, 7 bytes\n",
		},
		{
			name: "shared and empty namespaces",
			row: `{"id":"b1","state":"succeeded","manifest":{"sqlSize":10,` +
				`"registryFiles":{"ns-1":{"version":1,"files":[` +
				`{"sha256":"a","size":7,"release":"net","version":"1.0.0"},` +
				`{"sha256":"b","size":3,"release":"net","version":"1.0.0"}]},` +
				`"ns-2":{"version":1,"files":[]}}}}`,
			want: "registry files: 2 files, 10 bytes\n",
		},
		{
			name: "no section",
			row:  `{"id":"b1","state":"succeeded","manifest":{"sqlSize":10}}`,
			want: "registry files: 0 files, 0 bytes\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serveBackupRow(t, tt.row)

			t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

			var stdout, stderr bytes.Buffer

			if err := client.BackupCommand(t.Context(), []string{"--server", srv.URL}, &stdout, &stderr); err != nil {
				t.Fatalf("backup: %v\n%s", err, stderr.String())
			}

			if !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("backup output %q lacks %q", stdout.String(), tt.want)
			}
		})
	}
}

// TestBackupReadFailureSaysTheBackupContinues proves that a backup command
// that started a backup and then cannot read it does not tell the operator to
// run the backup again: the server continues the backup, and a second start
// answers 409 while it runs. The text names the read of its state. The test
// sets the admin token, so it cannot run in parallel.
func TestBackupReadFailureSaysTheBackupContinues(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"connection closed": closeConnection,
		"server error": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
		"not JSON": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json")) //nolint:errcheck // The client may close early.
		},
	}

	for name, read := range tests {
		t.Run(name, func(t *testing.T) {
			srv := serveStartThenRead(t, read)

			t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

			var stdout, stderr bytes.Buffer

			err := client.BackupCommand(t.Context(), []string{"--server", srv.URL}, &stdout, &stderr)
			if err == nil || !errors.Is(err, client.ErrBackupRead) {
				t.Fatalf("backup = %v, want ErrBackupRead", err)
			}

			text := err.Error()
			if !strings.Contains(text, "continues backup bkp-run1 on its own") ||
				!strings.Contains(text, "/api/dustpan/v1/backups/bkp-run1") || strings.Contains(text, "again") {
				t.Errorf("error = %q, want the text that the server continues the backup, with no rerun", text)
			}
		})
	}
}

// closeConnection closes the connection of a request with no answer.
func closeConnection(w http.ResponseWriter, _ *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}

	if conn, _, err := hj.Hijack(); err == nil {
		_ = conn.Close() //nolint:errcheck // The test closes the connection on purpose.
	}
}

// serveStartThenRead answers a backup start with the ID bkp-run1 and every
// read of the backup with read.
func serveStartThenRead(t *testing.T, read http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			read(w, r)

			return
		}

		w.WriteHeader(http.StatusAccepted)

		_, _ = w.Write([]byte(`{"id":"bkp-run1"}`)) //nolint:errcheck // The client may close early.
	}))
	t.Cleanup(srv.Close)

	return srv
}
