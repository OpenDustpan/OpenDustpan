//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Names of the unit, the pool and the runner of the claim steps. The pool
// holds the jobs of these steps only, so a claim finds no other job.
const (
	claimUnit   = "claim-network"
	claimPool   = "claims"
	claimRunner = "e2e-runner"
)

// claimStartBody returns the body of a plan job on the unit of the claim steps.
func claimStartBody() string {
	return fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":"plan","pool":%q,`+
		`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"e2e"}`,
		claimUnit, claimPool)
}

// claimJobDoc is the part of the job document that the claim steps read.
type claimJobDoc struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// aGateStopsAClaim proves that the gates of a unit hold at the claim of a
// phase, not only at the start of its job: a job starts with no gate, an
// enforced gate at state.write is saved, and the claim of the phase by the
// job claimer of the server fails the job with the name of the gate and a
// denied runner:claim entry. A gate in evaluate mode then lets the claim of
// the next job go on.
func aGateStopsAClaim(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	// The steps save gates and claim through the in-process server.
	if e2e.UseBinaryServer() {
		t.Log("skip the claim gate steps: they need the in-process server")

		return
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "claim gate: create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + claimUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "claim gate: create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + e2e.Namespace + "/pools", Body: `{"name":"` + claimPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	scope, err := gate.UnitScope(namespace.Name(e2e.Namespace), "opentofu", claimUnit)
	if err != nil {
		t.Fatalf("unit scope: %v", err)
	}

	first := startClaimJob(ctx, t, env, "claim gate: start a job with no gate")

	enforced := claimGate(scope, "no-claims", gate.ModeEnforce)
	must(t, env.PutGate(ctx, t, enforced))

	if a, claimErr := env.ClaimJobPhase(ctx, claimRunner, first.PoolID); !errors.Is(claimErr, durable.ErrNoWork) {
		t.Fatalf("claim with a gate saved after the start = job %s, %v; want durable.ErrNoWork", a.Job.ID, claimErr)
	}

	checkClaimRefused(ctx, t, env, first.ID)
	must(t, env.DeleteGate(ctx, t, scope, enforced.ID))

	// A gate in evaluate mode stops nothing.
	second := startClaimJob(ctx, t, env, "claim gate: start a job after the refused claim")
	watch := claimGate(scope, "watch-claims", gate.ModeEvaluate)
	must(t, env.PutGate(ctx, t, watch))

	a, err := env.ClaimJobPhase(ctx, claimRunner, second.PoolID)
	if err != nil || string(a.Job.ID) != second.ID || a.Job.State != job.StateRunning {
		t.Fatalf("claim with a gate in evaluate mode = job %s (%s), %v; want job %s running",
			a.Job.ID, a.Job.State, err, second.ID)
	}

	must(t, env.DeleteGate(ctx, t, scope, watch.ID))
}

// claimGate returns a deny gate at state.write of scope in mode.
func claimGate(scope gate.Scope, id string, mode gate.Mode) gate.Gate {
	return gate.Gate{
		Scope: scope, ID: gate.ID(id), Version: 0, DecisionPoint: gate.PointStateWrite, Condition: "", Unless: "",
		Outcome: gate.OutcomeDeny, Message: "Plans are frozen.", Mode: mode, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: nil,
	}
}

// startClaimJob starts a plan job on the unit of the claim steps and returns
// its document. The job waits dispatched for the claim of a runner.
func startClaimJob(ctx context.Context, t *testing.T, env *e2e.Env, name string) jobDoc {
	t.Helper()

	var doc jobDoc

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: name, Method: http.MethodPost, Path: jobsRoute, Body: claimStartBody(),
		Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("decode job: %w", err)
			}

			if doc.State != "dispatched" || doc.PoolID == "" {
				return fmt.Errorf("%w: state %s, pool %q; want dispatched in a pool", errJobDoc, doc.State, doc.PoolID)
			}

			return nil
		},
	})

	return doc
}

// checkClaimRefused checks that the job id failed with the name of the gate,
// and that the refused claim wrote a denied runner:claim entry.
func checkClaimRefused(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "claim gate: the job failed at the claim", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, id), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			var doc claimJobDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("decode job: %w", err)
			}

			if doc.State != "failed" || !strings.Contains(doc.Reason, "no-claims at state.write") {
				return fmt.Errorf("%w: state %s, reason %q; want failed by the gate no-claims", errJobDoc,
					doc.State, doc.Reason)
			}

			return nil
		},
	})

	entries, err := env.AuditEntries(ctx, namespace.Name(e2e.Namespace))
	if err != nil {
		t.Fatalf("audit entries: %v", err)
	}

	for i := range entries {
		e := &entries[i]
		if e.Action == string(job.ActionRunnerClaim) && e.JobID == id && e.Outcome == audit.OutcomeDenied &&
			e.Subject == claimRunner && strings.Contains(e.Reason, "no-claims") {
			return
		}
	}

	t.Fatalf("no denied runner:claim entry of job %s among %d entries", id, len(entries))
}
