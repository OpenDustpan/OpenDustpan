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
