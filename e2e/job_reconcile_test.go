//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// reconcileWait is the longest wait for the schedule job.reconcile. The
// schedule runs once each minute, and the first tick can come before the job
// exists, so two ticks and a margin fit.
const reconcileWait = 150 * time.Second

// reconcilePoll is the time between two reads of the job.
const reconcilePoll = 2 * time.Second

// TestJobReconcile proves the schedule job.reconcile on the real server binary
// and each store: a plan job that a manual lock queued is dispatched by the
// schedule after the unlock, with no request that touches the job. The worker
// of the server binary runs the schedule, so the test needs the binary.
func TestJobReconcile(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary: the worker of the server binary runs the schedule job.reconcile")
	}

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			// The runner management API needs the public URL, which the sign-in setup gives.
			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			reconcileScenario(t, env)
		})
	}
}

func reconcileScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	lock := "/api/inventory/unit-lock?unit=" + url.QueryEscape("opentofu:"+e2e.Namespace+"/"+jobsUnit)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + e2e.Namespace + "/pools", Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "lock the workspace", Method: http.MethodPut, Path: lock,
		Body: `{"reason":"change window closed"}`, Want: http.StatusOK, Scheme: "", Check: nil,
	})

	var queued jobDoc

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start a plan job on a locked workspace", Method: http.MethodPost,
		Path: jobsRoute, Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &queued); err != nil {
				return fmt.Errorf("decode job: %w", err)
			}

			if queued.State != "queued" || queued.Wait != "locked" {
				return fmt.Errorf("%w: the job is %s and waits for %q, want queued and locked", errJobDoc,
					queued.State, queued.Wait)
			}

			return nil
		},
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "unlock the workspace", Method: http.MethodDelete, Path: lock, Body: "",
		Want: http.StatusNoContent, Scheme: "", Check: nil,
	})

	waitsForDispatch(ctx, t, env, queued.ID)
}

// waitsForDispatch reads the job id until the schedule job.reconcile has
// dispatched it, and fails t when it has not within reconcileWait.
func waitsForDispatch(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, reconcileWait)
	defer cancel()

	tick := time.NewTicker(reconcilePoll)
	defer tick.Stop()

	for {
		status, body, err := env.APIStatus(ctx, http.MethodGet, jobPath(e2e.Namespace, id), "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("read job %s = status %d, %v", id, status, err)
		}

		var doc jobDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode job %s: %v", id, err)
		}

		if doc.State == "dispatched" {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("job %s is %s after %s, want dispatched by the schedule job.reconcile", id, doc.State,
				reconcileWait)
		case <-tick.C:
		}
	}
}
