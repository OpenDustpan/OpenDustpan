//go:build e2e

// This file covers the runner apply path. The persona is Sam, who reviews
// infrastructure changes. A pool named ci has one runner that joins and shows
// in the runner list of the pool. A plan job for the OpenTofu unit starts,
// the runner claims its phase, writes the run log, and uploads and commits
// the plan. Sam reads the run log while the phase runs and resumes it from a
// sequence number. The apply half of the path and the gate that waits for two
// approvers need features that main does not have, so those steps are skipped
// with the reason that names what each waits for.
//
// User-visible end checks: the runner pool page of the docs, the job log that
// the CLI and the API print, the stored plan of the job, and the run page of
// the console.

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// runnerApplyTest is the name of this test, used in the names of skipped steps.
const runnerApplyTest = "TestPathRunnerApply"

// TestPathRunnerApply runs the runner apply steps on each store. Each step
// that a feature of main supports runs for real, and each step that waits for
// a feature that main does not have is skipped with the reason that names
// what it waits for.
func TestPathRunnerApply(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runRunnerApplyOnStore(t, db)
		})
	}
}

// runRunnerApplyOnStore runs the steps in order on one store. The steps share
// one environment and one runner, so a later step sees the pool that the
// first one made.
func runRunnerApplyOnStore(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, e2e.NoCLI(), store)

	var runner *e2e.FakeRunner

	t.Run("pool_and_join", func(t *testing.T) {
		runner = runnerApplyPool(ctx, t, env)
	})

	t.Run("apply_job_starts", func(t *testing.T) {
		runnerApplyBlocked(t, env, "apply_job_starts",
			"blocked: this server starts plan jobs only: a start of an apply answers 422 job.unsupported")
	})

	t.Run("plan_runs_on_runner", func(t *testing.T) {
		runnerApplyPlanRun(ctx, t, env, runner, "path-runner-apply-plan")
	})

	t.Run("gate_waits_for_two", func(t *testing.T) {
		runnerApplyBlocked(t, env, "gate_waits_for_two",
			"blocked: the job service records no approval, and a plan phase gets no approval gate")
	})

	t.Run("stored_plan_applies", func(t *testing.T) {
		runnerApplyBlocked(t, env, "stored_plan_applies",
			"blocked: this server starts plan jobs only: no apply phase applies a stored plan")
	})

	t.Run("run_log_read", func(t *testing.T) {
		runnerApplyLogRun(ctx, t, env, runner, "path-runner-apply-log")
	})

	t.Run("decision_record_read", func(t *testing.T) {
		runnerApplyBlocked(t, env, "decision_record_read",
			"blocked: no route reads the decision record of a gate")
	})

	t.Run("console_run_page", func(t *testing.T) {
		runnerApplyBlocked(t, env, "console_run_page", "blocked: the console run page is not built")
	})
}

// runnerListDoc is the runner list of one pool.
type runnerListDoc struct {
	Runners []runnerOfPool `json:"runners"`
}

// runnerOfPool is one runner of the runner list of a pool.
type runnerOfPool struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Isolation string `json:"isolation"`
}

// runnerApplyPool makes the OpenTofu unit of the path, creates the runner
// pool ci, joins one fake runner to it, and proves that the runner list of
// the pool names the runner. It answers the runner that joined the pool.
func runnerApplyPool(ctx context.Context, t *testing.T, env *e2e.Env) *e2e.FakeRunner {
	t.Helper()

	var poolID string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools",
		Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			var pool struct {
				ID string `json:"id"`
			}

			if err := json.Unmarshal([]byte(body), &pool); err != nil || pool.ID == "" {
				return fmt.Errorf("%w: pool %s: %w", errJobDoc, body, err)
			}

			poolID = pool.ID

			return nil
		},
	})

	runner := env.JoinFakeRunner(ctx, t, e2e.Namespace, poolID, "sam-ci-1")

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "list the runners of the pool", Method: http.MethodGet,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools/" + poolID + "/runners",
		Body: "",
		Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			var doc runnerListDoc

			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("decode the runner list %s: %w", body, err)
			}

			for _, r := range doc.Runners {
				if r.Name == "sam-ci-1" && r.Kind == "machine" && r.Isolation == "gvisor" {
					return nil
				}
			}

			return fmt.Errorf("%w: the runner list %s names no machine runner sam-ci-1 in gvisor", errJobDoc, body)
		},
	})

	return runner
}

// runnerApplyPlanRun starts a plan job, claims its phase with the runner of
// the pool, writes one step of the run log, uploads and commits the two plan
// parts, reads the job with its stored plan, and finishes the phase.
func runnerApplyPlanRun(
	ctx context.Context, t *testing.T, env *e2e.Env, runner *e2e.FakeRunner, key string,
) {
	t.Helper()

	run := runnerApplyClaim(ctx, t, env, runner, key)
	step := run.launcher.Assignment.Steps[0].Name

	run.writesTheStep(ctx, t, step, []byte("plan output of the unit\n"))

	display := planPartOf("plan-display", []byte(runnerApplyPlanDisplay))
	apply := planPartOf("plan-apply", []byte(runnerApplyPlanApply+run.job))

	wantNoContent(t, "upload the display part", func() (int, []byte) {
		return run.launcher.PutLog(ctx, t, planPartPath(protocol.PathSessionPlanUpload, "plan-display"),
			[]byte(runnerApplyPlanDisplay))
	})
	wantNoContent(t, "upload the apply part", func() (int, []byte) {
		return run.launcher.PutLog(ctx, t, planPartPath(protocol.PathSessionPlanUpload, "plan-apply"),
			[]byte(runnerApplyPlanApply+run.job))
	})
	wantNoContent(t, "commit the plan", func() (int, []byte) {
		return run.launcher.Post(ctx, t, protocol.PathSessionPlanCommit,
			protocol.PlanCommit{Parts: []protocol.PlanPart{display, apply}})
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "read the job with its plan", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, run.job), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			for _, want := range []string{display.SHA256, apply.SHA256} {
				if !strings.Contains(body, want) {
					return fmt.Errorf("%w: the job holds no part with the hash %s: %s", errJobDoc, want, body)
				}
			}

			return nil
		},
	})

	run.finishesTwice(ctx, t, "the plan phase ended")
}

// runnerApplyLogRun starts a plan job, claims its phase with the runner of
// the pool, and reads the run log while the phase runs: each entry arrives
// before the phase ends, the log of the apply phase answers 404 because that
// phase did not start, and a read from a sequence number resumes the log.
func runnerApplyLogRun(
	ctx context.Context, t *testing.T, env *e2e.Env, runner *e2e.FakeRunner, key string,
) {
	t.Helper()

	run := runnerApplyClaim(ctx, t, env, runner, key)
	step := run.launcher.Assignment.Steps[0].Name
	first, second := []byte("first plan line\n"), []byte("second plan line\n")

	// The apply phase did not start, so it has no log. The refusal names the
	// reason the apply steps of this path are skipped.
	status, body, err := env.APIStatus(ctx, http.MethodGet, jobPath(e2e.Namespace, run.job)+"/logs/apply", "")
	if err != nil || status != http.StatusNotFound || !strings.Contains(body, "job.log-not-available") {
		t.Fatalf("log of the apply phase = %d %s %v, want 404 job.log-not-available", status, body, err)
	}

	run.streamsWhileTheStepRuns(ctx, t, step, first, second)

	resumed := openLogStream(ctx, t, env, run.job, "?after=1")
	defer closeStream(t, resumed)

	if line := resumed.next(t); line.Seq != 2 || line.Text != string(second) {
		t.Fatalf("resumed entry = %+v, want seq 2 %q", line, second)
	}

	if line := resumed.next(t); !line.Done {
		t.Fatalf("resumed last line = %+v, want done", line)
	}
}

// runnerApplyClaim starts a plan job with the Idempotency-Key key, claims its
// phase with the runner of the pool, and opens the session of the claim.
func runnerApplyClaim(
	ctx context.Context, t *testing.T, env *e2e.Env, runner *e2e.FakeRunner, key string,
) *launched {
	t.Helper()

	var started jobDoc

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start a plan job", Method: http.MethodPost, Path: jobsRoute,
		Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			doc, err := checkStarted(body)
			started = doc

			return err
		},
	}, map[string]string{"Idempotency-Key": key})

	work := runner.Poll(ctx, t, pollWait)
	if work == nil || work.Job != started.ID {
		t.Fatalf("poll = %+v, want the phase of the job %s", work, started.ID)
	}

	handoff := runner.Handoff(ctx, t, work.Operation)

	launcher := env.OpenSession(ctx, t, handoff)
	if launcher.Assignment.Job != started.ID || len(launcher.Assignment.Steps) == 0 {
		t.Fatalf("assignment = %+v, want the steps of the job %s", launcher.Assignment, started.ID)
	}

	return &launched{env: env, job: started.ID, handoff: handoff, launcher: launcher}
}

// Contents of the plan parts of the path steps. The apply part stands for a
// plan file that can hold a secret value.
const (
	runnerApplyPlanDisplay = `{"changes": 3}`
	runnerApplyPlanApply   = "plan-secret-value-of-"
)

// runnerApplyBlocked records that the step name does not run in this mode, and
// skips it. The reason names the missing capability.
func runnerApplyBlocked(t *testing.T, env *e2e.Env, name, reason string) {
	t.Helper()

	env.SkipScenario(t, runnerApplyTest+"/"+name, reason)
	t.Skip(reason)
}
