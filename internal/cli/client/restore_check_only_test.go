package client_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// serveRestoreState answers a restore start with the ID rst-test and every
// read of that restore with a check-only restore in the state state. Every
// other request gets 404.
func serveRestoreState(t *testing.T, state string) *httptest.Server {
	t.Helper()

	write := func(w http.ResponseWriter, body string) {
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write the answer: %v", err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/dustpan/v1/restores":
			w.WriteHeader(http.StatusAccepted)
			write(w, `{"id":"rst-test"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/dustpan/v1/restores/rst-test":
			errText := ""
			if state == "failed" {
				errText = "backup data is corrupt"
			}

			write(w, `{"id":"rst-test","state":"`+state+`","backupID":"bkp-aaa","error":"`+errText+`"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestRestoreCheckOnlyPrintsNoChangeOnlyOnSuccess proves that a check-only
// restore prints "check only: no data changed" when the check succeeded, and
// not when it failed, so the output of a failed check has no line that reads
// as a success. The test sets the admin token, so it cannot run in parallel.
func TestRestoreCheckOnlyPrintsNoChangeOnlyOnSuccess(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "test-token")

	for state, want := range map[string]bool{"succeeded": true, "failed": false} {
		srv := serveRestoreState(t, state)

		var stdout, stderr bytes.Buffer

		err := client.RestoreCommand(t.Context(),
			[]string{"--server", srv.URL, "--backup", "bkp-aaa", "--check-only"}, &stdout, &stderr)
		if (err == nil) != want {
			t.Fatalf("%s: restore --check-only = %v", state, err)
		}

		if got := strings.Contains(stdout.String(), "check only: no data changed"); got != want {
			t.Fatalf("%s: the output holds the no-change line = %v, want %v:\n%s", state, got, want, stdout.String())
		}
	}
}
