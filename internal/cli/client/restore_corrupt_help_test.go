package client_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// serveFailedRestore answers a restore start with the ID rst-test and every
// read of that restore with a failed restore that holds errText. Every other
// request gets 404.
func serveFailedRestore(t *testing.T, errText string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/server/restores":
			w.WriteHeader(http.StatusAccepted)

			if _, err := w.Write([]byte(`{"id":"rst-test"}`)); err != nil {
				t.Errorf("write the answer: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/server/restores/rst-test":
			doc := map[string]string{"id": "rst-test", "state": "failed", "backupID": "bkp-aaa", "error": errText}
			if err := json.NewEncoder(w).Encode(doc); err != nil {
				t.Errorf("write the answer: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestRestoreOfCorruptDataNamesTheBackupCheck proves that a restore that
// fails on corrupt backup data tells the operator to run the backup check,
// and not to run the same restore again, which fails in the same way. Other
// failures keep the hint to run the restore again. The test sets the admin
// token, so it cannot run in parallel.
func TestRestoreOfCorruptDataNamesTheBackupCheck(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	for _, tc := range []struct {
		errText   string
		wantCheck bool
	}{
		{`backup is incomplete: object "blobs/a/b" is missing: backup data is corrupt`, true},
		{"backup data is corrupt", true},
		{"restore failed", false},
		{"backup not found", false},
	} {
		srv := serveFailedRestore(t, tc.errText)

		var stdout, stderr bytes.Buffer

		err := client.RestoreCommand(t.Context(),
			[]string{"--server", srv.URL, "--backup", "bkp-aaa", "--check-only"}, &stdout, &stderr)
		if err == nil || !errors.Is(err, client.ErrRestoreFailed) {
			t.Fatalf("%s: restore = %v, want ErrRestoreFailed", tc.errText, err)
		}

		check := "run opendustpan backup check --server " + srv.URL + " --backup bkp-aaa"
		again := "run opendustpan restore --server " + srv.URL + " --backup bkp-aaa again"

		if got := err.Error(); !strings.Contains(got, tc.errText) ||
			strings.Contains(got, check) != tc.wantCheck || strings.Contains(got, again) == tc.wantCheck {
			t.Fatalf("%s: restore = %q, want the error text and the backup check hint = %v", tc.errText, got,
				tc.wantCheck)
		}
	}
}
