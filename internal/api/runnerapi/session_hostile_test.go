package runnerapi_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
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

// TestSessionHostileSessionKeyOnRunnerPath proves the other side of the
// credential split: a session key proof never signs a request of the runner.
// The poll and the heartbeat answer 401 bad-proof and take no claim.
func TestSessionHostileSessionKeyOnRunnerPath(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()

			for _, path := range []string{protocol.PathPoll, protocol.PathHeartbeat} {
				status, body := l.do(l.post(path, nil))
				if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorBadProof {
					t.Errorf("%s with a session key = %d %s, want 401 %s", path, status, body, protocol.ErrorBadProof)
				}
			}

			if got := s.jobs.called(); len(got) != 0 {
				t.Fatalf("a refused request reached the recorder: %v", got)
			}
		})
	}
}

// newAuditedSessionEnv is newSessionEnv that returns the audit log of the
// handler too. A non-nil limiter replaces the denial limiter of the handler.
func newAuditedSessionEnv(t *testing.T, b backend, limiter *denylimit.Limiter) (*sessionEnv, *testAudit) {
	t.Helper()

	var written *testAudit

	s := newSessionEnvWith(t, b, func(cfg *runnerapi.Config) {
		if a, ok := cfg.Audit.(*testAudit); ok {
			written = a
		}

		if limiter != nil {
			cfg.DenialLimiter = limiter
		}
	})

	if written == nil {
		t.Fatal("the handler has no test audit log")
	}

	return s, written
}

// logRefusals returns the audit entries of the log chunks that a cap refused.
func (a *testAudit) logRefusals() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	var out []audit.Entry

	for i := range a.entries {
		if a.entries[i].Action == "runner.log.refused" {
			out = append(out, a.entries[i])
		}
	}

	return out
}

// TestLogCapRefusalIsAudited proves the audit of a log cap refusal through the
// shared denial limiter: ten refusals of one runner in one window write one
// entry, and the first refusal of the next window writes an entry that carries
// the count of the nine others. Each entry names the claim, the job and the
// cap, is denied, and holds no byte of the chunk.
func TestLogCapRefusalIsAudited(t *testing.T) {
	t.Parallel()

	const (
		window   = time.Minute
		refusals = 10
		bytesOf  = "chunk-bytes-that-no-entry-holds"
	)

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			clock := newFakeClock()
			limiter := denylimit.New(denylimit.Config{
				Window: window, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: clock.Now,
			})

			s, log := newAuditedSessionEnv(t, b, limiter)
			s.jobs.seedLog("init", 1, protocol.MaxLogBytesPerStep)
			l := s.launch()

			refuseChunks(t, l, refusals, bytesOf)

			if entries := log.logRefusals(); len(entries) != 1 {
				t.Fatalf("entries after %d refusals in one window = %d, want 1", refusals, len(entries))
			}

			clock.Advance(window + time.Second)
			refuseChunks(t, l, 1, bytesOf)

			entries := log.logRefusals()
			if len(entries) != 2 {
				t.Fatalf("entries after the next window = %d, want 2", len(entries))
			}

			counted := fmt.Sprintf("; %d further refusals since the last entry", refusals-1)
			if !slices.ContainsFunc(entries, func(e audit.Entry) bool { return strings.Contains(e.Reason, counted) }) {
				t.Fatalf("entries = %+v, want one that carries %q", entries, counted)
			}

			for i := range entries {
				checkLogRefusal(t, s, &entries[i], bytesOf)
			}
		})
	}
}

// refuseChunks sends chunk 1 of the step init n times, and checks that the step
// byte cap refuses each one with invalid.
func refuseChunks(t *testing.T, l *launcher, n int, chunk string) {
	t.Helper()

	for range n {
		l.expect(t, "a chunk above the step byte cap", l.upload(logPath(1), []byte(chunk)),
			http.StatusBadRequest, protocol.ErrorInvalid)
	}
}

// checkLogRefusal checks that e is a denial of the runner of s that names the
// claim, the job and the step byte cap, and holds no byte of the chunk.
func checkLogRefusal(t *testing.T, s *sessionEnv, e *audit.Entry, chunk string) {
	t.Helper()

	if e.Outcome != audit.OutcomeDenied || e.Subject != s.runner.id || e.JobID == "" ||
		!strings.Contains(e.Reason, "claim "+s.work.Operation+" ") ||
		!strings.Contains(e.Reason, "job "+e.JobID+";") ||
		!strings.Contains(e.Reason, "the cap "+string(job.LogCapStepBytes)+" ") {
		t.Errorf("entry = %+v, want a denial of the runner that names the claim, the job and the cap", e)
	}

	if strings.Contains(fmt.Sprintf("%+v", *e), chunk) {
		t.Errorf("entry holds the bytes of the chunk: %+v", e)
	}
}

// TestLogRefusalKeepsItsMessage proves that only a cap of the log gets the cap
// message and an audit entry: a chunk out of order keeps the message of the
// sequence rule, and a chunk with another hash for a stored number keeps the
// conflict. Neither writes a refusal entry.
func TestLogRefusalKeepsItsMessage(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s, log := newAuditedSessionEnv(t, b, nil)
			l := s.launch()

			status, body := l.do(l.upload(logPath(2), []byte("x")))

			var e protocol.Error

			decode(t, body, &e)

			if status != http.StatusBadRequest || e.Code != protocol.ErrorInvalid ||
				strings.Contains(e.Message, "cap") {
				t.Fatalf("a chunk out of order = %d %s, want 400 %s without the cap message", status, body,
					protocol.ErrorInvalid)
			}

			l.expect(t, "chunk 0", l.upload(logPath(0), []byte("first")), http.StatusNoContent, "")

			status, body = l.do(l.upload(logPath(0), []byte("other")))
			if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorConflict {
				t.Fatalf("chunk 0 with another hash = %d %s, want 409 %s", status, body, protocol.ErrorConflict)
			}

			if got := log.logRefusals(); len(got) != 0 {
				t.Fatalf("refusals that are not caps wrote cap entries: %+v", got)
			}
		})
	}
}

// TestSessionHostilePlanCommitOtherParts proves the rule of a repeated plan
// commit at the handler: the same parts again answer 204, and other parts
// after a commit answer conflict and leave the stored plan. The plan recorder
// here is a double. The rule of the real recorder on each SQL store, that the
// first plan stays, is the conformance case "plan parts: a second commit with
// the same parts is the same plan, with other parts ErrConflict" (testCommitTwice
// in pkg/job/jobtest), which TestRecorderConformance runs on SQLite and
// PostgreSQL.
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

// requireSweepPool checks that the claim of s is in a namespace pool with the
// sweep purpose.
func requireSweepPool(t *testing.T, s *sessionEnv) {
	t.Helper()

	pools, err := s.store.runners.Pools(t.Context(), "acme")
	if err != nil {
		t.Fatalf("list pools: %v", err)
	}

	if !slices.ContainsFunc(pools, func(p runner.Pool) bool {
		return p.ID == s.pool && p.Purpose == runner.PurposeSweeps
	}) {
		t.Fatalf("pools = %+v, want the claim in a namespace pool with the sweep purpose", pools)
	}
}

// TestSessionHostileNamespaceSweepPool proves the purpose rule of the session
// paths: a claim of a namespace pool with the sweep purpose gets no job
// session. The exchange refuses it and opens no session. A session that the
// store opened anyway (as if the exchange check failed) gets no step, log or
// finish, and the recorder sees no call.
func TestSessionHostileNamespaceSweepPool(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSweepEnv(t, b)
			requireSweepPool(t, s)

			x := s.newExchange(s.runner.handoff(s.work.Operation))
			if status, body := s.sendExchange(x); status != http.StatusBadRequest ||
				errorCode(t, body) != protocol.ErrorInvalid {
				t.Fatalf("job session for a sweep pool = %d %s, want 400 %s", status, body, protocol.ErrorInvalid)
			}

			// A second claim of the pool, whose session the store opens, as if
			// the check of the exchange failed.
			s = newSweepEnv(t, b)
			l := s.launchSweep()
			now := time.Now().UTC()

			calls := []struct {
				name string
				call lcall
			}{
				{"step begin", l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil)},
				{"log chunk", l.upload(logPath(0), []byte("x"))},
				{"step end", l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
					ExitCode: 0, StartedAt: now, FinishedAt: now, LogChunks: 0, LogSHA256: "",
				})},
			}

			for _, c := range calls {
				l.expect(t, c.name+" of a sweep pool claim", c.call, http.StatusBadRequest, protocol.ErrorInvalid)
			}

			if got := s.jobs.called(); len(got) != 0 {
				t.Fatalf("a claim of a sweep pool reached the recorder: %v", got)
			}
		})
	}
}
