//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// haNoWorkerWait bounds the wait until every instance counts the worker
	// as absent: 2 lease periods from the start of the instance, and a margin.
	haNoWorkerWait = 2*haLease + haBackupGrace
	// haNoWorkerCode is the error code of a start that no worker can run.
	haNoWorkerCode = "worker.absent"
	// haNoWorkerHelp is the text that names the command that helps.
	haNoWorkerHelp = "--components worker"
)

// haNoWorkerRoute is a route of the installation API that inserts a durable
// operation and needs no other setup than the token.
type haNoWorkerRoute struct {
	name   string
	method string
	path   string
}

// haNoWorkerRoutes returns the routes that the step calls.
func haNoWorkerRoutes() []haNoWorkerRoute {
	return []haNoWorkerRoute{
		{"start a backup", http.MethodPost, "/api/server/backups"},
		{"approve an audit chain change", http.MethodPost, "/api/server/audit/chain"},
	}
}

// haNoWorker restarts every instance with only the api component. Once
// each instance counts the worker as absent, a backup request and an audit
// chain approval must answer 503 with the error code and the text that names
// `--components worker`, and no backup may start.
func haNoWorker(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)

	env.Recorder.SetStep("8 no worker")

	cluster.StopAll(t)
	cluster.SetComponents("api")
	cluster.StartAll(ctx, t)

	haWaitNoWorker(ctx, t, env, cluster.Size())

	// Two rounds of the proxy reach every instance at least once.
	for round := range 2 * cluster.Size() {
		for _, route := range haNoWorkerRoutes() {
			haCheckNoWorker(ctx, t, env, route, round)
		}
	}

	haCheckNoBackupRuns(ctx, t, env)
}

// haCheckNoWorker calls route and fails unless it answers 503 with the code
// and the text of a start with no worker.
func haCheckNoWorker(ctx context.Context, t *testing.T, env *e2e.Env, route haNoWorkerRoute, round int) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, route.method, route.path, "")
	if err != nil {
		t.Fatalf("%s, request %d: %v", route.name, round, err)
	}

	if status != http.StatusServiceUnavailable {
		t.Fatalf("%s, request %d answers %d, want 503 (%s)", route.name, round, status, body)
	}

	var doc struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("%s, request %d: decode %q: %v", route.name, round, body, err)
	}

	if doc.ErrorCode != haNoWorkerCode || !strings.Contains(doc.Message, haNoWorkerHelp) {
		t.Fatalf("%s, request %d answers %q, want the code %s and the text %q",
			route.name, round, body, haNoWorkerCode, haNoWorkerHelp)
	}
}

// haBackupRow is a row of the list of backups.
type haBackupRow struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// haBackupList is the answer of the list of backups.
type haBackupList struct {
	Backups []haBackupRow `json:"backups"`
}

// haCheckNoBackupRuns fails when the list of backups shows a backup that
// runs: the refused requests must start none.
func haCheckNoBackupRuns(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET backups answers %d: %v (%s)", status, err, body)
	}

	var listed haBackupList

	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	for _, b := range listed.Backups {
		if b.State == "running" {
			t.Fatalf("backup %s runs although no worker runs", b.ID)
		}
	}
}

// haWaitNoWorker waits until the last size answers of the worker report, which
// the proxy gives to the instances in turn, all say that the worker is stuck.
func haWaitNoWorker(ctx context.Context, t *testing.T, env *e2e.Env, size int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, haNoWorkerWait)
	defer cancel()

	for stuck := 0; stuck < size; {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/worker", "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("GET worker answers %d: %v (%s)", status, err, body)
		}

		var report struct {
			Stuck bool `json:"stuck"`
		}

		if err := json.Unmarshal([]byte(body), &report); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}

		if report.Stuck {
			stuck++

			continue
		}

		stuck = 0

		select {
		case <-ctx.Done():
			t.Fatalf("the instances do not count the worker as absent: %v (%s)", ctx.Err(), body)
		case <-time.After(haPollEvery):
		}
	}
}
