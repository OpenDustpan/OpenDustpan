package client

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// refusedMessage is the message of each error answer of the test server.
const refusedMessage = "the server refused the request"

// TestAnswerTextHintOnlyForServerFaults proves that each command prints the
// message of an error answer once. A 4xx answer has its cause in the request,
// so the text has no step to check the server log or to run the same command
// again. A 5xx answer keeps that step. For the read of a restore that started,
// the step says that the server resumes the restore.
func TestAnswerTextHintOnlyForServerFaults(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		status int
		hint   bool
	}{
		{status: http.StatusBadRequest, hint: false},
		{status: http.StatusNotFound, hint: false},
		{status: http.StatusConflict, hint: false},
		{status: http.StatusServiceUnavailable, hint: true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)

			if _, err := fmt.Fprintf(w, `{"code":%d,"message":%q}`, tc.status, refusedMessage); err != nil {
				t.Errorf("write answer: %v", err)
			}
		}))

		for name, err := range commandErrors(t, server) {
			text := fmt.Sprint(err)

			if strings.Count(text, refusedMessage) != 1 {
				t.Errorf("%s, status %d: error %q, want the server message once", name, tc.status, text)
			}

			hint := strings.Contains(text, " again") || strings.Contains(text, "resumes restore")
			if hint != tc.hint {
				t.Errorf("%s, status %d: error %q, want a retry hint %v", name, tc.status, text, tc.hint)
			}
		}

		server.Close()
	}
}

// commandErrors calls each command step that reads an answer of server and
// returns its error by the name of the step.
func commandErrors(t *testing.T, server *httptest.Server) map[string]error {
	t.Helper()

	ctx := t.Context()
	client := server.Client()

	_, namespaceErr := postNamespace(ctx, client, server.URL, "token", "acme")
	_, backupStartErr := startBackup(ctx, client, server.URL, "token")
	_, backupReadErr := readBackup(ctx, client, server.URL, "token", "bkp-test")
	_, backupCheckErr := readBackupCheck(ctx, client, server.URL, "token", "bkp-test")
	_, upgradeCheckErr := readUpgradeCheck(ctx, client, server.URL, "token")
	_, restoreStartErr := startRestore(ctx, startRestoreParams{
		client: client, server: server.URL, token: "token", backupID: "bkp-test", checkOnly: false, force: false,
	})
	_, restoreReadErr := readRestore(ctx, restoreReadParams{
		client: client, server: server.URL, token: "token", restoreID: "rst-test", backupID: "bkp-test",
	})

	listen := strings.TrimPrefix(server.URL, "http://")
	healthErr := RunHealth(ctx, []string{"--listen", listen}, io.Discard, io.Discard)

	return map[string]error{
		"namespace create": namespaceErr,
		"backup start":     backupStartErr,
		"backup read":      backupReadErr,
		"backup check":     backupCheckErr,
		"upgrade check":    upgradeCheckErr,
		"restore start":    restoreStartErr,
		"restore read":     restoreReadErr,
		"health":           healthErr,
	}
}
