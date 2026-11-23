package runnerapi_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestSessionHostileRunnerKeyOnLogAndStep proves the credential split of the
// log and step paths: a runner key proof never signs a request on a job
// session, also not one that a session key may sign. Each refusal changes
// nothing.
func TestSessionHostileRunnerKeyOnLogAndStep(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()
			now := time.Now().UTC()

			calls := []struct {
				name string
				call lcall
			}{
				{"step begin", l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil)},
				{"step end", l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
					ExitCode: 0, StartedAt: now, FinishedAt: now, LogChunks: 0, LogSHA256: "",
				})},
				{"log chunk", l.upload(logPath(0), []byte("x"))},
				{"plan part", l.upload(planUploadPath("plan-display"), []byte("x"))},
				{"plan commit", l.post(protocol.PathSessionPlanCommit, commitOf())},
			}

			for _, c := range calls {
				// The runner key proves the request, and names the runner as
				// the subject. No session belongs to it.
				c.call.subject, c.call.key = s.runner.id, s.runner.priv

				status, body := l.do(c.call)
				if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorBadProof {
					t.Errorf("%s with a runner key = %d %s, want 401 %s", c.name, status, body,
						protocol.ErrorBadProof)
				}
			}

			if got := s.jobs.called(); len(got) != 0 {
				t.Fatalf("a refused request reached the recorder: %v", got)
			}
		})
	}
}

// TestSessionHostileTakeoverDuringLogWrite proves the fence of a log chunk: a
// chunk of the session of a replaced claim is refused with claim-lost and the
// server keeps no byte of it.
func TestSessionHostileTakeoverDuringLogWrite(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()

			l.expect(t, "step begin", l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil), 204, "")
			l.expect(t, "chunk 0", l.upload(logPath(0), []byte("before")), 204, "")

			s.takeOver()

			l.expect(t, "chunk 1 after the takeover", l.upload(logPath(1), []byte("after")),
				http.StatusConflict, protocol.ErrorClaimLost)

			got := s.jobs.logChunks("init")
			if len(got) != 1 || got[0] != "before" {
				t.Fatalf("stored chunks = %v, want only chunk 0", got)
			}
		})
	}
}

// TestSessionHostilePlanCommitOtherParts proves the rule of a repeated plan
// commit: the same parts again answer 204, and other parts after a commit
// answer conflict and leave the stored plan.
func TestSessionHostilePlanCommitOtherParts(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, nil)
			l := s.launch()

			l.expect(t, "upload display", l.upload(planUploadPath("plan-display"), []byte(planDisplayText)), 204, "")
			l.expect(t, "upload apply", l.upload(planUploadPath("plan-apply"), []byte(planApplyText)), 204, "")
			l.expect(t, "commit", l.post(protocol.PathSessionPlanCommit, commitOf()), 204, "")
			l.expect(t, "same commit", l.post(protocol.PathSessionPlanCommit, commitOf()), 204, "")

			other := commitOf()
			other.Parts[0].SHA256 = textHash("another plan")

			l.expect(t, "other parts after the commit", l.post(protocol.PathSessionPlanCommit, other),
				http.StatusConflict, protocol.ErrorConflict)

			want := []job.PlanCommitPart{
				{Kind: job.OutputPlanDisplay, Size: int64(len(planDisplayText)), SHA256: textHash(planDisplayText)},
				{Kind: job.OutputPlanApply, Size: int64(len(planApplyText)), SHA256: textHash(planApplyText)},
			}
			if got := s.plans.storedParts(); !slices.Equal(got, want) {
				t.Fatalf("stored plan parts = %+v, want the first commit", got)
			}
		})
	}
}

// TestSessionHostileNamespaceSweepPool proves the purpose rule of a namespace
// pool: the management API refuses the sweep purpose, because a sweep never
// runs on a user runner, and it creates no pool.
func TestSessionHostileNamespaceSweepPool(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)

			status, body := s.admin("/api/dustpan/v1/namespaces/acme/runner-pools",
				map[string]any{"name": "scan", "purpose": "sweeps"})
			if status != http.StatusBadRequest {
				t.Fatalf("a namespace pool for sweeps = %d %s, want 400", status, body)
			}

			var e struct {
				Code      int    `json:"code"`
				ErrorCode string `json:"errorCode"`
			}
			decode(t, body, &e)
			if e.ErrorCode != "runner.invalid" {
				t.Fatalf("a namespace pool for sweeps = %d %s, want runner.invalid", status, body)
			}

			pools, err := s.store.runners.Pools(t.Context(), "acme")
			if err != nil {
				t.Fatalf("list pools: %v", err)
			}

			if len(pools) != 1 || pools[0].Name != "prod" || pools[0].Purpose != runner.PurposeJobs {
				t.Fatalf("pools = %+v, want only the jobs pool", pools)
			}
		})
	}
}
