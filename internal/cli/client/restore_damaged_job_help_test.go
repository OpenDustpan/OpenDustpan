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
	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreOfADamagedJobNamesTheCancel proves that a restore that stopped at
// a damaged job row prints the text of the row and the action that helps: the
// cancel of the job on the server of the backup. The text keeps its tail, the
// corrupt sentinel, and the help is not the hint of a missing object. The test
// sets the admin token, so it cannot run in parallel.
func TestRestoreOfADamagedJobNamesTheCancel(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	errText := backup.NewDamagedJobError("job-dmg1", "ws-1", "the body is not JSON").Error()
	srv := serveFailedRestore(t, errText)

	var stdout, stderr bytes.Buffer

	err := client.RestoreCommand(t.Context(),
		[]string{"--server", srv.URL, "--backup", "bkp-aaa", "--check-only"}, &stdout, &stderr)
	if err == nil || !errors.Is(err, client.ErrRestoreFailed) {
		t.Fatalf("restore = %v, want ErrRestoreFailed", err)
	}

	got := err.Error()
	for _, want := range []string{errText, "cancel the damaged job", "/jobs/{job_id}/cancel"} {
		if !strings.Contains(got, want) {
			t.Fatalf("restore = %q, want it to hold %q", got, want)
		}
	}

	if strings.Contains(got, "to find each missing") {
		t.Fatalf("restore = %q, want no hint of a missing object", got)
	}
}

// TestBackupCheckOfADamagedJobNamesTheCancel proves that backup check prints
// the damaged job row that the server reports, and the help for it. The test
// sets the admin token, so it cannot run in parallel.
func TestBackupCheckOfADamagedJobNamesTheCancel(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	errText := "check backup bkp-aaa: " + backup.NewDamagedJobError("job-dmg1", "ws-1", "the body is not JSON").Error()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dustpan/v1/backups/bkp-aaa/check" {
			http.NotFound(w, r)

			return
		}

		doc := map[string]any{"id": "bkp-aaa", "complete": false, "error": errText}
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Errorf("write the answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer

	err := client.BackupCheck(t.Context(), []string{"--server", srv.URL, "--backup", "bkp-aaa"}, &stdout, &stderr)
	if err == nil || !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	for _, want := range []string{"job-dmg1", "ws-1", "the body is not JSON", "cancel the damaged job"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("check = %q, want it to hold %q", err, want)
		}
	}
}

// TestBackupCheckTextWithTheMarkerInsideGetsNoCancelHelp proves that only a
// text that starts with the damaged job marker gets the help of a damaged job.
// A text that holds the marker in another place does not. The test sets the
// admin token, so it cannot run in parallel.
func TestBackupCheckTextWithTheMarkerInsideGetsNoCancelHelp(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	errText := "check backup bkp-aaa: object " + backup.DamagedJobPrefix + "x is missing"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		doc := map[string]any{"id": "bkp-aaa", "complete": false, "error": errText}
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Errorf("write the answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	var stdout, stderr bytes.Buffer

	err := client.BackupCheck(t.Context(), []string{"--server", srv.URL, "--backup", "bkp-aaa"}, &stdout, &stderr)
	if err == nil || !errors.Is(err, backup.ErrCorrupt) || strings.Contains(err.Error(), "cancel the damaged job") {
		t.Fatalf("check = %v, want ErrCorrupt with no cancel help", err)
	}
}
