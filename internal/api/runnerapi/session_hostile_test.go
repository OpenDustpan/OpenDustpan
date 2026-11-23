package runnerapi_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
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

			got := s.jobs.initChunks()
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
				checkLogRefusal(t, s, &entries[i], "cap-step", bytesOf)
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
// reason, the claim and the job, and holds no byte of the chunk.
func checkLogRefusal(t *testing.T, s *sessionEnv, e *audit.Entry, reason, chunk string) {
	t.Helper()

	if e.Outcome != audit.OutcomeDenied || e.Subject != s.runner.id || e.JobID == "" ||
		!strings.Contains(e.Reason, "reason "+reason+";") ||
		!strings.Contains(e.Reason, "claim "+s.work.Operation+" ") ||
		!strings.Contains(e.Reason, "job "+e.JobID+";") {
		t.Errorf("entry = %+v, want a denial of the runner that names the reason %s, the claim and the job",
			e, reason)
	}

	if strings.Contains(fmt.Sprintf("%+v", *e), chunk) {
		t.Errorf("entry holds the bytes of the chunk: %+v", e)
	}
}

// logRefusalReasons returns the reasons of the audit entry of a refused log
// chunk.
func logRefusalReasons() []string {
	return []string{
		"cap-chunk-number", "cap-step", "cap-phase", "chunk-too-large", "hash-header", "hash-mismatch",
		"out-of-order", "step-invalid", "name-or-number-invalid", "hash-body", "unknown",
		"body-unreadable",
	}
}

// TestLogRefusalOfAuditsAnUnknownRefusal proves that a cap or a chunk rule that
// logRefusalOf does not know is a refusal with the reason unknown, and that
// its answer stays the answer of the error of the recorder. With the default
// branches changed back to no refusal, the two cases fail.
func TestLogRefusalOfAuditsAnUnknownRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"an unknown cap", &job.LogCapError{Cap: "other-cap", Limit: 1, Phase: 0}},
		{"an unknown chunk rule", &job.ChunkError{Rule: "other-rule", Next: 0}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reason, cause := runnerapi.RecorderLogRefusal(fmt.Errorf("append: %w", tc.err), "init", 0)
			if reason != "unknown" || !errors.Is(cause, tc.err) {
				t.Fatalf("logRefusalOf(%s) = %q, %v; want unknown and the error", tc.name, reason, cause)
			}
		})
	}
}

// TestLogRefusalKeepsItsMessage proves that each log refusal keeps its own
// message, and writes one audit entry with its reason. The table names each
// reason of logRefusalReasons.
func TestLogRefusalKeepsItsMessage(t *testing.T) {
	t.Parallel()

	tests := logRefusalCases()

	for _, reason := range logRefusalReasons() {
		if !slices.ContainsFunc(tests, func(tc logRefusalCase) bool { return tc.reason == reason }) {
			t.Errorf("no case of the reason %s", reason)
		}
	}

	for _, b := range wiringBackends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkLogRefusalMessage(t, b, &tc)
			})
		}
	}
}

// checkLogRefusalMessage sends the hostile chunk of tc, and checks the answer
// and its message, and one audit entry with the reason of tc.
func checkLogRefusalMessage(t *testing.T, b backend, tc *logRefusalCase) {
	t.Helper()

	got := sendLogRefusal(t, b, tc)
	if got.status != tc.status || got.answer.Code != tc.code || got.answer.Message != tc.message {
		t.Fatalf("%s = %d %+v, want %d %s %q", tc.name, got.status, got.answer, tc.status, tc.code, tc.message)
	}

	if len(got.entries) != 1 || !strings.Contains(got.entries[0].Reason, "reason "+tc.reason+";") {
		t.Fatalf("entries of %s = %+v, want 1 with the reason %s", tc.name, got.entries, tc.reason)
	}
}

// TestSessionHostileLogRefusal sends one hostile chunk for each reason, and
// proves that its guard refuses it alone: the answer and the reason are the
// ones of the guard, the server keeps no byte of the chunk, the entry names the
// claim and the job and holds no byte of the chunk and no step name that is
// not valid, and a guard of the handler refuses the chunk before any call of
// the recorder. With the guard of a case removed, the chunk gets the answer or
// the reason of a later check, or is stored, and the case fails.
func TestSessionHostileLogRefusal(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		for _, tc := range logRefusalCases() {
			t.Run(b.name+"/"+tc.reason+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkHostileLogChunk(t, b, &tc)
			})
		}
	}
}

// checkHostileLogChunk sends the hostile chunk of tc, and checks that its
// guard alone refused it.
func checkHostileLogChunk(t *testing.T, b backend, tc *logRefusalCase) {
	t.Helper()

	got := sendLogRefusal(t, b, tc)
	if got.status != tc.status || got.answer.Code != tc.code {
		t.Fatalf("%s (guard: %s) = %d %+v, want %d %s", tc.name, tc.guard, got.status, got.answer,
			tc.status, tc.code)
	}

	if !maps.Equal(got.after, got.before) ||
		slices.Contains(slices.Collect(maps.Values(got.after)), refusedChunk) {
		t.Fatalf("stored chunks = %v, want %v and no byte of the refused chunk", got.after, got.before)
	}

	if len(got.entries) != 1 {
		t.Fatalf("entries of %s = %+v, want 1", tc.name, got.entries)
	}

	checkLogRefusal(t, got.s, &got.entries[0], tc.reason, refusedChunk)

	if strings.Contains(got.entries[0].Reason, hostileStep) {
		t.Fatalf("entry = %+v, holds the step name that is not valid", got.entries[0])
	}

	reached := slices.ContainsFunc(got.called, func(c string) bool { return strings.HasPrefix(c, "log:") })
	if !tc.recorder && reached {
		t.Fatalf("the hostile chunk reached the recorder past its guard %s: %v", tc.guard, got.called)
	}
}

// refusedChunk is the body of each hostile chunk. No stored log and no audit
// entry may hold it.
const refusedChunk = "refused-chunk-bytes"

// hostileStep is a step name that is not valid. No audit entry may hold it.
const hostileStep = "Init_SECRET"

// logSender sends the hostile chunk of a case and returns the answer.
type logSender func(t *testing.T, s *sessionEnv, l *launcher) (int, []byte)

// uploadLog sends body as the chunk at the path value seq of the step step.
func uploadLog(step, seq string, body []byte) logSender {
	return func(_ *testing.T, _ *sessionEnv, l *launcher) (int, []byte) {
		path := strings.NewReplacer("{step}", step, "{seq}", seq).Replace(protocol.PathSessionLog)

		return l.do(l.upload(path, body))
	}
}

// putLogSigned calls the handler of the chunk alone, with signed as the hash
// that the proof signed. The proof check refuses a header that is not a
// SHA-256 first, so only this call reaches the check of the handler.
func putLogSigned(signed []byte) logSender {
	return putLogBody(signed, strings.NewReader(refusedChunk))
}

// errInjectedBodyRead is the error of a chunk body that cannot be read.
var errInjectedBodyRead = errors.New("injected body read failure")

// putLogUnreadable calls the handler of the chunk alone, with a body that
// fails after the first bytes of the chunk, and a signed hash of the chunk.
func putLogUnreadable() logSender {
	signed := sha256.Sum256([]byte(refusedChunk))

	return putLogBody(signed[:], io.MultiReader(strings.NewReader(refusedChunk), iotest.ErrReader(errInjectedBodyRead)))
}

// putLogBody calls the handler of the chunk alone, with signed as the hash
// that the proof signed and body as the body.
func putLogBody(signed []byte, body io.Reader) logSender {
	return func(t *testing.T, s *sessionEnv, _ *launcher) (int, []byte) {
		t.Helper()

		sess := runner.Session{
			ID: "", Kind: runner.SessionKindLauncher, Operation: s.work.Operation, Token: s.work.Token,
			Runner: s.runner.id, Key: runner.Key{Public: nil, CreatedAt: time.Time{}}, CreatedAt: time.Time{},
		}

		return runnerapi.PutLog(t.Context(), s.handler, sess, runnerapi.LogPut{
			Step: "init", Seq: "0", Signed: signed, Body: body,
		})
	}
}

// logRefusalCases returns one hostile chunk for each reason of a log refusal,
// with the guard that refuses it.
func logRefusalCases() []logRefusalCase {
	const capMessage = "the log of the step or of the phase is at its cap"

	none := func(*testing.T, *sessionEnv, *launcher) {}
	chunk := []byte(refusedChunk)
	tooLarge := bytes.Repeat(chunk, job.MaxLogChunk/len(chunk)+1)
	// otherHash is a SHA-256 that is not the hash of the chunk.
	otherHash := sha256.Sum256([]byte("another body"))

	return []logRefusalCase{
		{
			name: "a chunk number at the step cap", guard: "putLog, the chunk number cap",
			prepare: none, send: uploadLog("init", strconv.Itoa(protocol.MaxLogChunksPerStep), chunk),
			recorder: false, status: http.StatusBadRequest, code: protocol.ErrorInvalid, message: capMessage,
			reason: "cap-chunk-number",
		},
		{
			name: "a chunk above the step byte cap", guard: "LogCaps.PlanChunk, the step byte cap",
			prepare: func(_ *testing.T, s *sessionEnv, _ *launcher) {
				s.jobs.seedLog("init", 1, protocol.MaxLogBytesPerStep)
			},
			send: uploadLog("init", "1", chunk), recorder: true,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid, message: capMessage, reason: "cap-step",
		},
		{
			name: "a chunk above the phase chunk cap", guard: "LogCaps.PlanChunk, the phase chunk cap",
			prepare: func(_ *testing.T, s *sessionEnv, _ *launcher) {
				s.jobs.seedLog("other", protocol.MaxLogChunksPerPhase, 0)
			},
			send: uploadLog("init", "0", chunk), recorder: true,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid, message: capMessage, reason: "cap-phase",
		},
		{
			name: "a chunk above 1 MiB", guard: "putLog, the size of a chunk",
			prepare: none, send: uploadLog("init", "0", tooLarge), recorder: false,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: fmt.Sprintf("a log chunk has at most %d bytes", job.MaxLogChunk), reason: "chunk-too-large",
		},
		{
			name: "a signed hash that is not a SHA-256", guard: "putLog, the length of the signed hash",
			prepare: none, send: putLogSigned([]byte("short")), recorder: false,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: fmt.Sprintf("the %s header is not a SHA-256 in hex", protocol.HeaderContentSHA256),
			reason:  "hash-header",
		},
		{
			name: "chunk 0 with another hash", guard: "PlanChunk, the hash of a stored chunk",
			prepare: func(t *testing.T, _ *sessionEnv, l *launcher) {
				t.Helper()
				l.expect(t, "chunk 0", l.upload(logPath(0), []byte("first")), http.StatusNoContent, "")
			},
			send: uploadLog("init", "0", chunk), recorder: true,
			status: http.StatusConflict, code: protocol.ErrorConflict,
			message: "chunk 0 of step init has another hash", reason: "hash-mismatch",
		},
		{
			name: "a chunk after the next one", guard: "PlanChunk, the order of the chunks",
			prepare: func(t *testing.T, _ *sessionEnv, l *launcher) {
				t.Helper()
				l.expect(t, "chunk 0", l.upload(logPath(0), []byte("first")), http.StatusNoContent, "")
			},
			send: uploadLog("init", "3", chunk), recorder: true,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: "chunk 3 of step init is not the next one, which is 1", reason: "out-of-order",
		},
		{
			name: "a step name that is not valid", guard: "stepOf",
			prepare: none, send: uploadLog(hostileStep, "0", chunk), recorder: false,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: fmt.Sprintf("the step name is 1 to %d lowercase letters, digits and '-'", job.MaxStepName),
			reason:  "step-invalid",
		},
		{
			name: "a negative chunk number", guard: "putLog, the chunk number",
			prepare: none, send: uploadLog("init", "-1", chunk), recorder: false,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: fmt.Sprintf("the number of a log chunk is 0 to %d", protocol.MaxLogChunksPerStep-1),
			reason:  "name-or-number-invalid",
		},
		{
			name: "a name or number that the recorder refuses", guard: "job.CheckChunkKey in the recorder",
			prepare: func(_ *testing.T, s *sessionEnv, _ *launcher) {
				// The recorder refuses the key, as if the checks of the handler
				// failed.
				s.jobs.failWith("log", job.CheckChunkKey("", 0))
			},
			send: uploadLog("init", "0", chunk), recorder: true,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: "the step name or the number of the log chunk is not valid", reason: "name-or-number-invalid",
		},
		{
			name: "a body with another hash than the signed one", guard: "putLog, the signed hash of the body",
			prepare: none, send: putLogSigned(otherHash[:]), recorder: false,
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
			message: runner.ErrBadProof.Error(), reason: "hash-body",
		},
		{
			name: "a body that cannot be read", guard: "putLog, the read of the body",
			prepare: none, send: putLogUnreadable(), recorder: false,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: runner.ErrInvalid.Error(), reason: "body-unreadable",
		},
		{
			name: "a cap that the handler does not know", guard: "logRefusalOf, the unknown cap",
			prepare: func(_ *testing.T, s *sessionEnv, _ *launcher) {
				s.jobs.failWith("log", &job.LogCapError{Cap: "other-cap", Limit: 1, Phase: 0})
			},
			send: uploadLog("init", "0", chunk), recorder: true,
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			message: job.ErrInvalid.Error(), reason: "unknown",
		},
	}
}

// logRefusalCase is one hostile log chunk: the guard that refuses it, the calls
// before it, how to send it, and the answer and the audit reason that it must
// get. recorder is true when the recorder refuses the chunk; a guard of the
// handler refuses it before any call of the recorder.
type logRefusalCase struct {
	name     string
	guard    string
	prepare  func(t *testing.T, s *sessionEnv, l *launcher)
	send     logSender
	recorder bool
	status   int
	code     string
	message  string
	reason   string
}

// logRefusalResult is what a hostile chunk left: the answer, the audit entries
// of log refusals, the chunks of the step init before and after it, and the
// calls of the recorder.
type logRefusalResult struct {
	s       *sessionEnv
	status  int
	answer  protocol.Error
	entries []audit.Entry
	before  map[int]string
	after   map[int]string
	called  []string
}

// sendLogRefusal sends the hostile chunk of tc on a new session.
func sendLogRefusal(t *testing.T, b backend, tc *logRefusalCase) logRefusalResult {
	t.Helper()

	s, log := newAuditedSessionEnv(t, b, nil)
	l := s.launch()
	tc.prepare(t, s, l)

	before := s.jobs.initChunks()
	called := len(s.jobs.called())

	status, body := tc.send(t, s, l)

	var e protocol.Error

	decode(t, body, &e)

	return logRefusalResult{
		s: s, status: status, answer: e, entries: log.logRefusals(), before: before, after: s.jobs.initChunks(),
		called: s.jobs.called()[called:],
	}
}

// TestSessionHostilePlanCommitOtherParts proves the rule of a repeated plan
// commit at the handler: the same parts again answer 204, and other parts
// after a commit answer conflict and leave the stored plan. It runs the flow
// two times: with a double of the plan recorder, and with the real recorder
// over the SQLite store, the real job claimer and a real dispatched plan job.
// The real case reads the plan of the phase back from the store, and checks
// that the conflict comes from the recorder's check of a phase that has a
// plan with other parts.
func TestSessionHostilePlanCommitOtherParts(t *testing.T) {
	t.Parallel()

	want := []job.PlanCommitPart{
		{Kind: job.OutputPlanDisplay, Size: int64(len(planDisplayText)), SHA256: textHash(planDisplayText)},
		{Kind: job.OutputPlanApply, Size: int64(len(planApplyText)), SHA256: textHash(planApplyText)},
	}

	commitTwiceThenOther := func(t *testing.T, l *launcher) {
		t.Helper()

		l.expect(t, "upload display", l.upload(planUploadPath("plan-display"), []byte(planDisplayText)), 204, "")
		l.expect(t, "upload apply", l.upload(planUploadPath("plan-apply"), []byte(planApplyText)), 204, "")
		l.expect(t, "commit", l.post(protocol.PathSessionPlanCommit, commitOf()), 204, "")
		l.expect(t, "same commit", l.post(protocol.PathSessionPlanCommit, commitOf()), 204, "")

		other := commitOf()
		other.Parts[0].SHA256 = textHash("another plan")

		l.expect(t, "other parts after the commit", l.post(protocol.PathSessionPlanCommit, other),
			http.StatusConflict, protocol.ErrorConflict)
	}

	for _, b := range wiringBackends() {
		t.Run(b.name+"/double", func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, nil)
			commitTwiceThenOther(t, s.launch())

			if got := s.plans.storedParts(); !slices.Equal(got, want) {
				t.Fatalf("stored plan parts = %+v, want the first commit", got)
			}
		})
	}

	t.Run("sqlite/real recorder", func(t *testing.T) {
		t.Parallel()

		s := newDurablePlanEnv(t)
		commitTwiceThenOther(t, s.launch())

		checkRealRecommitConflict(t, s)

		if got := s.storedPlanParts(t); !slices.Equal(got, want) {
			t.Fatalf("stored plan parts = %+v, want the first commit", got)
		}
	})
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
