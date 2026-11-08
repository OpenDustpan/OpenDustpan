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

	return serveCodedRestore(t, errText, "")
}

// serveCodedRestore is serveFailedRestore with an error code in the answer
// of the restore.
func serveCodedRestore(t *testing.T, errText, code string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/server/restores":
			w.WriteHeader(http.StatusAccepted)

			if _, err := w.Write([]byte(`{"id":"rst-test"}`)); err != nil {
				t.Errorf("write the answer: %v", err)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/server/restores/rst-test":
			doc := map[string]string{
				"id": "rst-test", "state": "failed", "backupID": "bkp-aaa", "error": errText, "errorCode": code,
			}
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

// TestRestoreStoppedAtLiveBlobsNamesTheRepair proves that the CLI chooses
// its help from the error code of the restore, not from its text. A restore
// that stopped at damaged or missing live blobs prints the text of the
// server, which names the keys, and the help for the live blob store, and not
// the check of the backup, which is not at fault. A key in the text that
// holds the words of the other refusal does not change the help. It covers
// the refusal that would leave too few working break-glass accounts too. The
// test sets the admin token, so it cannot run in parallel.
func TestRestoreStoppedAtLiveBlobsNamesTheRepair(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	damagedHelp := "put the good bytes back at the keys above from a copy of the blob store, then run opendustpan " +
		"restore --server %s --backup bkp-aaa again"
	missingHelp := "run opendustpan store check --server %s --target blobs to check the blob store setting of the " +
		"server, then run opendustpan restore --server %s --backup bkp-aaa again"
	breakGlassHelp := "restore a backup that holds at least 2 working break-glass accounts"

	for _, tc := range []struct {
		name    string
		errText string
		code    string
		help    string
	}{
		{
			name: "damaged", code: "restore.live-blobs-damaged", help: damagedHelp,
			errText: `the backup before the restore found 1 live blobs whose bytes do not match their records or ` +
				`do not open: "namespaces/ns-a/tofu/ws/sv/a1": live blobs do not match their records: ` +
				`backup data is corrupt`,
		},
		{
			name: "missing", code: "restore.live-blobs-missing", help: missingHelp,
			errText: `the backup before the restore found 2 of 2 live blobs missing: too many live blobs are ` +
				`missing: backup data is corrupt`,
		},
		{
			name: "missing with a key that holds the words of the damaged refusal", code: "restore.live-blobs-missing",
			help: missingHelp,
			errText: `the backup before the restore found 11 of 11 live blobs missing, first ` +
				`"namespaces/ns-a/live blobs do not match their records": too many live blobs are missing: ` +
				`backup data is corrupt`,
		},
		{
			name: "too few working break-glass accounts", code: "principal.no-working-break-glass", help: breakGlassHelp,
			errText: `restore leaves too few working break-glass accounts: the change leaves too few working ` +
				`break-glass accounts`,
		},
	} {
		srv := serveCodedRestore(t, tc.errText, tc.code)

		var stdout, stderr bytes.Buffer

		err := client.RestoreCommand(t.Context(),
			[]string{"--server", srv.URL, "--backup", "bkp-aaa"}, &stdout, &stderr)
		if err == nil || !errors.Is(err, client.ErrRestoreFailed) {
			t.Fatalf("%s: restore = %v, want ErrRestoreFailed", tc.name, err)
		}

		help := strings.ReplaceAll(tc.help, "%s", srv.URL)
		if got := err.Error(); !strings.Contains(got, tc.errText) || !strings.HasSuffix(got, help) ||
			strings.Contains(got, "backup check") {
			t.Fatalf("%s: restore = %q, want the error text and %q", tc.name, got, help)
		}
	}
}

// TestRestoreReadFailureSaysTheServerResumes proves that a restore that
// started and that the command cannot read any more, for example while the
// database of the server is down, names the restore and says that the server
// resumes it, and not to run the restore again. The test sets the admin
// token, so it cannot run in parallel.
func TestRestoreReadFailureSaysTheServerResumes(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/server/restores" {
			w.WriteHeader(http.StatusAccepted)

			if _, err := w.Write([]byte(`{"id":"rst-test"}`)); err != nil {
				t.Errorf("write the answer: %v", err)
			}

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)

		if _, err := w.Write([]byte(`{"code":503,"message":"the database is not available"}`)); err != nil {
			t.Errorf("write the answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer

	err := client.RestoreCommand(t.Context(), []string{"--server", srv.URL, "--backup", "bkp-aaa"}, &stdout, &stderr)
	if err == nil || !errors.Is(err, client.ErrRestoreRead) {
		t.Fatalf("restore = %v, want ErrRestoreRead", err)
	}

	want := "the server resumes restore rst-test on its own and it needs no second run: read its state with GET " +
		srv.URL + "/api/server/restores/rst-test"
	if got := err.Error(); !strings.Contains(got, want) || strings.Contains(got, " again") {
		t.Fatalf("restore = %q, want %q and no hint to run it again", got, want)
	}
}
