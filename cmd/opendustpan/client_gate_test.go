package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
)

// errGateStoreOpened reports a store open outside serve.
var errGateStoreOpened = errors.New("a command other than serve opened the database")

// gateRun runs args through run and discards the output. The error is
// ignored: a refusal and a success both prove the command never opened a
// store. Only the swapped opener fails the test.
func gateRun(t *testing.T, args []string) {
	t.Helper()

	//nolint:errcheck // A refusal and a success both prove the command never opened a store.
	_ = run(t.Context(), args, io.Discard, io.Discard)
}

// gateHealthServer starts a fake readiness probe that answers ready.
func gateHealthServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)

		if _, err := io.WriteString(w, "ready"); err != nil {
			t.Errorf("write readiness body: %v", err)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

// TestNonServeCommandsOpenNoStore proves every command except serve and
// host never opens a store: it swaps the store opener that only serve and
// host may use for one that fails the test, then runs every other command
// in process against fake servers or with flags that fail early. Serve opens
// the store to run the server, and host opens it to recover a lost administrator
// without the public listener: both have their own tests.
func TestNonServeCommandsOpenNoStore(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	previous := openStore
	openStore = func(_ context.Context, _ string, _ *slog.Logger, _ openMode, _ installation.Kind) (store, error) {
		t.Error(errGateStoreOpened.Error())

		return nil, errGateStoreOpened
	}
	t.Cleanup(func() { openStore = previous })

	health := gateHealthServer(t)
	namespace := newNamespaceFakeServer(
		t,
		"test-token",
		http.StatusCreated,
		`{"name":"acme","primaryInstallation":"test","failoverVersion":1,"createdAt":"2026-10-11T12:00:00Z"}`,
	)
	upgrade := newCheckFakeServer(t, "/api/dustpan/v1/upgrade-check", http.StatusOK, upgradeCheckBody(t, nil))

	const backupID = "bkp-test00000000000001"

	backupCheck := newCheckFakeServer(
		t,
		"/api/server/backups/"+backupID+"/check",
		http.StatusOK,
		backupCheckBody(t, backupID, true, ""),
	)
	backup := newBackupFakeServer(t, "test-token", succeededScript(1))
	restore := newRestoreFakeServer(t, "test-token", succeededRestoreScript(1))

	gateRun(t, []string{"--version"})
	gateRun(t, []string{"health", "--listen", healthListenOf(t, health.URL)})
	gateRun(t, []string{"health", "--listen", "127.0.0.1:1"})
	gateRun(t, []string{"namespace", "create", "--server", namespace.server.URL, "acme"})
	gateRun(t, []string{"upgrade", "check", "--server", upgrade.server.URL})
	gateRun(t, []string{"backup", "check", "--server", backupCheck.server.URL, "--backup", backupID})
	gateRun(t, []string{"backup", "--server", backup.server.URL})
	gateRun(t, []string{"restore", "--server", restore.server.URL, "--backup", restoreBackupTestID})
	gateRun(t, []string{"bogus"})
	gateRun(t, []string{"namespace"})
	gateRun(t, []string{"namespace", "create"})
	gateRun(t, []string{"upgrade"})
	gateRun(t, []string{"backup", "no-such"})
}
