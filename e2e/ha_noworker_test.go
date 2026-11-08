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
// operation. call sends the request: the approval of an audit chain change
// needs a console session with a recent sign-in, the other routes take the
// token.
type haNoWorkerRoute struct {
	name string
	call func(ctx context.Context) (int, string, error)
}

// haNoWorkerRoutes returns the routes that the step calls.
func haNoWorkerRoutes(env *e2e.Env, console func(e2e.SessionRequest) (int, string)) []haNoWorkerRoute {
	return []haNoWorkerRoute{
		{"start a backup", func(ctx context.Context) (int, string, error) {
			return env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
		}},
		{"start a plan job", func(ctx context.Context) (int, string, error) {
			return env.APIStatus(ctx, http.MethodPost, jobsRoute, startBody(jobsPool, "plan"))
		}},
		{"approve an audit chain change", func(context.Context) (int, string, error) {
			status, body := console(
				e2e.SessionRequest{Method: http.MethodPost, Path: "/api/server/audit/chain", Body: ""},
			)

			return status, body, nil
		}},
	}
}

// haNoWorker restarts every instance with only the api component. Once
// each instance counts the worker as absent, a backup request, a job start
// and an audit chain approval must answer 503 with the error code and the
// text that names `--components worker`, and no backup or job may start.
func haNoWorker(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	cluster := env.HA(t)

	env.Recorder.SetStep("8 no worker")

	cluster.StopAll(t)
	cluster.SetComponents("api")
	cluster.StartAll(ctx, t)

	haWaitNoWorker(ctx, t, env, cluster.Size())

	// The sign-in is recent when the requests start.
	routes := haNoWorkerRoutes(env, env.FreshAdminSession(ctx, t))

	before := haCountNoWorkerWork(ctx, t, env)

	// Two rounds of the proxy reach every instance at least once.
	for round := range 2 * cluster.Size() {
		for _, route := range routes {
			haCheckNoWorker(ctx, t, route, round)
		}
	}

	after := haCountNoWorkerWork(ctx, t, env)
	if after.jobs != before.jobs {
		t.Fatalf("the jobs of the namespace are %d after the refused starts, want %d", after.jobs, before.jobs)
	}

	if after.operations > before.operations {
		t.Fatalf("the durable operations are %d after the refused starts, want at most %d",
			after.operations, before.operations)
	}

	haCheckNoBackupRuns(ctx, t, env)
}

// haCheckNoWorker calls route and fails unless it answers 503 with the code
// and the text of a start with no worker.
func haCheckNoWorker(ctx context.Context, t *testing.T, route haNoWorkerRoute, round int) {
	t.Helper()

	status, body, err := route.call(ctx)
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

// haNoWorkerWork is the work that the refused starts must not add: the jobs
// of the namespace and the durable operations that no worker claimed.
type haNoWorkerWork struct {
	jobs       int
	operations int
}

// haCountNoWorkerWork counts the jobs of the namespace and the durable
// operations that the installation lists.
func haCountNoWorkerWork(ctx context.Context, t *testing.T, env *e2e.Env) haNoWorkerWork {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, jobsRoute, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET jobs answers %d: %v (%s)", status, err, body)
	}

	var listed jobListDoc

	if err = json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode the jobs %q: %v", body, err)
	}

	// The worker report lists each durable operation that no worker claimed.
	status, body, err = env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/worker", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET worker answers %d: %v (%s)", status, err, body)
	}

	var report struct {
		Operations []json.RawMessage `json:"operations"`
	}

	if err = json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatalf("decode the worker report %q: %v", body, err)
	}

	return haNoWorkerWork{jobs: len(listed.Jobs), operations: len(report.Operations)}
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
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/dustpan/v1/worker", "")
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
