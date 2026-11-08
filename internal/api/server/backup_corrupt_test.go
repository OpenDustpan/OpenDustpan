package server_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/blob"
)

// deleteManifest deletes the manifest of the backup id in the target of held
// and returns its key.
func deleteManifest(t *testing.T, held checkBackupStores, id string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey("backups/" + id + "/manifest.json")
	if err != nil {
		t.Fatalf("parse manifest key: %v", err)
	}

	if err := held.stores.target.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete the manifest: %v", err)
	}

	return key
}

// putManifest returns a change that writes body in place of the manifest of a
// backup.
func putManifest(body string) func(t *testing.T, held checkBackupStores, id string) {
	return func(t *testing.T, held checkBackupStores, id string) {
		t.Helper()

		key := deleteManifest(t, held, id)
		if err := held.stores.target.Put(t.Context(), key, strings.NewReader(body)); err != nil {
			t.Fatalf("write the manifest: %v", err)
		}
	}
}

// TestGetBackupCorruptManifest proves that the read of a succeeded backup
// whose manifest is cut, empty, of another format or gone answers 409 with
// backup.corrupt, the backup ID and the cause that the check route gives after
// its prefix, not 500.
func TestGetBackupCorruptManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(t *testing.T, held checkBackupStores, id string)
	}{
		{name: "cut", change: putManifest(`{"format":1,"id":"bkp-`)},
		{name: "empty", change: putManifest("")},
		{name: "other format", change: putManifest(`{"format":3}`)},
		{name: "gone", change: func(t *testing.T, held checkBackupStores, id string) {
			t.Helper()
			deleteManifest(t, held, id)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			held := openCheckFixture(t)
			id := startBackup(t, held.fixture.handler)
			pollBackupSucceeded(t, held.fixture.handler, id)
			tc.change(t, held, id)

			_, check := readBackupCheck(t, held.fixture, id)

			status, body := serveBackup(t, held.fixture.handler, http.MethodGet, "/api/dustpan/v1/backups/"+id)
			if status != http.StatusConflict {
				t.Fatalf("GET backup %s answers %d, want %d: %s", id, status, http.StatusConflict, body)
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, []byte(body),
				errcodetest.Want{Code: errcode.BackupCorrupt, Args: map[string]any{"backup": id}, RequestBody: nil})

			if doc := decodeError(t, body); doc.Message == "" || !strings.HasSuffix(check.Error, doc.Message) {
				t.Fatalf("message = %q, want the cause of the check %q", doc.Message, check.Error)
			}
		})
	}
}

// errSecretText is an error whose text the log must not hold.
var errSecretText = errors.New("dsn host=secret-name")

// TestInternalErrorLogNamesTheCause proves that the log line of a 500 names
// the status and a cause from the closed list, and never the text of the
// error.
func TestInternalErrorLogNamesTheCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		cause string
	}{
		{
			name: "path error",
			err: fmt.Errorf(
				"read manifest: %w",
				&fs.PathError{Op: "open", Path: "/srv/secret-name", Err: fs.ErrPermission},
			),
			cause: "file_permission",
		},
		{name: "text error", err: errSecretText, cause: "unclassified"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/backups/x", http.NoBody)
			rec := httptest.NewRecorder()
			apiserver.WriteErrorLogForTest(rec, r, tc.err, slog.New(slog.NewTextHandler(&buf, nil)))

			line := buf.String()
			if !strings.Contains(line, "status=500") ||
				!strings.Contains(line, "cause="+tc.cause) ||
				strings.Contains(line, "secret-name") {
				t.Fatalf("log = %q, want the status and the cause %s, and no text of the error", line, tc.cause)
			}
		})
	}
}
