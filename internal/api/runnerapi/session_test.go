package runnerapi_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// sessionRoute is a route of a job session, with a valid request for it.
type sessionRoute struct {
	name string
	// build returns the valid request of the route.
	build func(l *launcher) lcall
	// status is the status of a valid request.
	status int
	// call is the call that the recorder sees for a valid request, or empty.
	call string
}

func sessionRoutes() []sessionRoute {
	return []sessionRoute{
		{
			name: "renew", status: http.StatusOK, call: "",
			build: func(l *launcher) lcall { return l.post(protocol.PathSessionRenew, nil) },
		},
		{
			name: "step begin", status: http.StatusNoContent, call: "begin:init",
			build: func(l *launcher) lcall {
				return l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil)
			},
		},
		{
			name: "step end", status: http.StatusNoContent, call: "end:init",
			build: func(l *launcher) lcall {
				now := time.Now().UTC()

				return l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
					ExitCode: 0, StartedAt: now, FinishedAt: now, LogChunks: 0, LogSHA256: "",
				})
			},
		},
		{
			name: "log", status: http.StatusNoContent, call: "log:init:0",
			build: func(l *launcher) lcall { return l.upload(logPath(0), []byte("hello\n")) },
		},
		{
			name: "finish", status: http.StatusNoContent, call: "",
			build: func(l *launcher) lcall {
				return l.post(protocol.PathSessionFinish, protocol.Finish{Succeeded: true, Message: ""})
			},
		},
	}
}

// sessionScenario changes the state or the request of a valid call, and says
// what the server answers.
type sessionScenario struct {
	name string
	// setup changes the state, or the request c, before the call.
	setup  func(s *sessionEnv, l *launcher, c *lcall)
	status int
	code   string
	// requestOnly marks a scenario that the server decides from the request
	// alone, before any read of the store: the dialect of the store does not
	// change the answer, so the scenario runs on the first backend only.
	requestOnly bool
}

func sessionScenarios() []sessionScenario {
	other := func(t *testing.T) ed25519.PrivateKey {
		t.Helper()

		_, priv := newKeyPair(t)

		return priv
	}

	return []sessionScenario{
		{name: "valid session", setup: nil, status: 0, code: "", requestOnly: false},
		{
			name: "wrong token: a proof of another key",
			setup: func(s *sessionEnv, _ *launcher, c *lcall) {
				c.key = other(s.t)
			},
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
			requestOnly: true,
		},
		{
			name: "wrong token: an unknown session",
			setup: func(_ *sessionEnv, _ *launcher, c *lcall) {
				c.subject = runner.SessionIDPrefix + "unknown"
			},
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof, requestOnly: false,
		},
		{
			name: "wrong token: the runner key",
			setup: func(s *sessionEnv, _ *launcher, c *lcall) {
				c.subject, c.key = s.runner.id, s.runner.priv
			},
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
			requestOnly: true,
		},
		{
			name: "wrong token: a proof of another body",
			setup: func(_ *sessionEnv, _ *launcher, c *lcall) {
				c.signedBody = []byte("another body")
			},
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
			requestOnly: true,
		},
		{
			name: "wrong token: a proof from two minutes ago",
			setup: func(s *sessionEnv, _ *launcher, c *lcall) {
				c.at = s.clock.now().Add(-2 * time.Minute)
			},
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
			requestOnly: true,
		},
		{
			name:   "no release header",
			setup:  func(_ *sessionEnv, _ *launcher, c *lcall) { c.release = "-" },
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
			requestOnly: true,
		},
		{
			name:   "release outside the skew",
			setup:  func(_ *sessionEnv, _ *launcher, c *lcall) { c.release = "9.9.9" },
			status: http.StatusUpgradeRequired, code: "",
			requestOnly: true,
		},
		{
			name: "lost claim: another runner took the phase",
			setup: func(s *sessionEnv, _ *launcher, _ *lcall) {
				s.takeOver()
			},
			status: http.StatusConflict, code: protocol.ErrorClaimLost, requestOnly: false,
		},
		{
			name: "expired session: the lease passed",
			setup: func(s *sessionEnv, _ *launcher, _ *lcall) {
				s.lapse(s.work.Operation)
			},
			status: http.StatusConflict, code: protocol.ErrorClaimLost, requestOnly: false,
		},
		{
			name: "lost claim: the operation ended",
			setup: func(s *sessionEnv, _ *launcher, _ *lcall) {
				op := s.opState()
				if err := s.store.operations.Complete(s.t.Context(), op.Claim); err != nil {
					s.t.Fatalf("complete: %v", err)
				}
			},
			status: http.StatusConflict, code: protocol.ErrorClaimLost, requestOnly: false,
		},
	}
}

// routeRun is one route of a job session with one scenario on one backend.
type routeRun struct {
	b  backend
	rt sessionRoute
	sc sessionScenario
}

// routeRuns returns every route with every scenario on every backend.
func routeRuns() []routeRun {
	bs := backends()
	out := make([]routeRun, 0, len(bs)*len(sessionRoutes())*len(sessionScenarios()))

	for _, b := range bs {
		for _, rt := range sessionRoutes() {
			for _, sc := range sessionScenarios() {
				if sc.requestOnly && b.name != bs[0].name {
					continue
				}

				out = append(out, routeRun{b: b, rt: rt, sc: sc})
			}
		}
	}

	return out
}

// TestSessionRoutes sends every route of a job session each scenario: a valid
// session, and each way that a request fails. A refused request reaches
// neither the recorder nor the claimer.
func TestSessionRoutes(t *testing.T) {
	t.Parallel()

	for _, run := range routeRuns() {
		t.Run(run.b.name+"/"+run.rt.name+"/"+run.sc.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, run.b)
			l := s.launch()
			c := run.rt.build(l)

			if run.sc.setup != nil {
				run.sc.setup(s, l, &c)
			}

			status, body := l.do(c)

			if run.sc.status == 0 {
				run.checkValid(t, s, status, body)

				return
			}

			run.checkRefused(t, s, status, body)
		})
	}
}

// checkValid checks the answer to a valid request and the call that the
// recorder saw.
func (run routeRun) checkValid(t *testing.T, s *sessionEnv, status int, body []byte) {
	t.Helper()

	if status != run.rt.status {
		t.Fatalf("status = %d %s, want %d", status, body, run.rt.status)
	}

	wantCalls := []string(nil)
	if run.rt.call != "" {
		wantCalls = []string{run.rt.call}
	}

	if got := s.jobs.called(); !slices.Equal(got, wantCalls) {
		t.Fatalf("recorder calls = %v, want %v", got, wantCalls)
	}
}

// checkRefused checks the refusal of a request, and that it reached neither
// the recorder nor the claimer.
func (run routeRun) checkRefused(t *testing.T, s *sessionEnv, status int, body []byte) {
	t.Helper()

	if status != run.sc.status || (run.sc.code != "" && errorCode(t, body) != run.sc.code) {
		t.Fatalf("status = %d %s, want %d %s", status, body, run.sc.status, run.sc.code)
	}

	if got := s.jobs.called(); len(got) != 0 {
		t.Fatalf("a refused request reached the recorder: %v", got)
	}

	if n := s.claimer.finished.Load(); n != 0 {
		t.Fatalf("a refused request finished the phase %d times", n)
	}
}

// exchangeCase is one session exchange, with the part that it changes.
type exchangeCase struct {
	name   string
	change func(s *sessionEnv, x *exchange)
	status int
	code   string
}

// exchangeCases returns the exchanges of TestOpenSession: a valid one, and one
// for each wrong token, key, proof or state.
func exchangeCases() []exchangeCase {
	return []exchangeCase{
		{name: "valid exchange", change: nil, status: http.StatusOK, code: ""},
		{
			name:   "challenge signed by another key",
			change: func(s *sessionEnv, x *exchange) { _, x.priv = newKeyPair(s.t) },
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
		},
		{
			name:   "proof signed by another key",
			change: func(s *sessionEnv, x *exchange) { _, x.signer = newKeyPair(s.t) },
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
		},
		{
			name:   "proof of another subject",
			change: func(_ *sessionEnv, x *exchange) { x.subject = sha256Hex("another token") },
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
		},
		{
			name:   "challenge for another handoff token",
			change: func(_ *sessionEnv, x *exchange) { x.challenge = "dph_other" },
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
		},
		{
			name:   "proof from two minutes ago",
			change: func(s *sessionEnv, x *exchange) { x.at = s.clock.now().Add(-2 * time.Minute) },
			status: http.StatusUnauthorized, code: protocol.ErrorBadProof,
		},
		{
			name:   "release outside the skew",
			change: func(_ *sessionEnv, x *exchange) { x.release = "9.9.9" },
			status: http.StatusUpgradeRequired, code: "",
		},
		{
			name:   "key that is not an Ed25519 key",
			change: func(_ *sessionEnv, x *exchange) { x.pub = []byte("short") },
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
		},
		{
			name:   "token that is not a handoff token",
			change: func(_ *sessionEnv, x *exchange) { x.token = "dpj_" + strings.Repeat("a", 64) },
			status: http.StatusBadRequest, code: protocol.ErrorInvalid,
		},
		{
			name:   "unknown handoff token",
			change: func(_ *sessionEnv, x *exchange) { x.token = runner.HandoffTokenPrefix + strings.Repeat("a", 64) },
			status: http.StatusNotFound, code: protocol.ErrorNotFound,
		},
		{
			name: "expired handoff token",
			change: func(s *sessionEnv, x *exchange) {
				s.clock.advance(runner.HandoffTokenTTL + time.Second)
				x.at = s.clock.now()
			},
			status: http.StatusUnauthorized, code: protocol.ErrorTokenExpired,
		},
		{
			name:   "lost claim: the lease passed",
			change: func(s *sessionEnv, _ *exchange) { s.lapse(s.work.Operation) },
			status: http.StatusConflict, code: protocol.ErrorClaimLost,
		},
	}
}

// TestOpenSession proves the session exchange: a valid exchange answers the
// assignment, and each wrong token, key, proof or state fails with its code.
func TestOpenSession(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		for _, tc := range exchangeCases() {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				tc.run(t, newSessionEnv(t, b))
			})
		}
	}
}

// run sends the exchange of the case and checks the answer.
func (tc exchangeCase) run(t *testing.T, s *sessionEnv) {
	t.Helper()

	x := s.newExchange(s.runner.handoff(s.work.Operation))

	if tc.change != nil {
		tc.change(s, &x)
	}

	status, body := s.sendExchange(x)
	if status != tc.status || (tc.code != "" && errorCode(t, body) != tc.code) {
		t.Fatalf("exchange = %d %s, want %d %s", status, body, tc.status, tc.code)
	}

	if tc.status == http.StatusOK {
		checkSessionResponse(t, s, body)
	}
}

// checkSessionResponse checks the answer to a valid exchange.
func checkSessionResponse(t *testing.T, s *sessionEnv, body []byte) {
	t.Helper()

	var resp protocol.SessionResponse

	decode(t, body, &resp)

	if !strings.HasPrefix(resp.Session, runner.SessionIDPrefix) || resp.AssignmentVersion != runner.AssignmentVersion {
		t.Fatalf("session = %q version %d, want a session ID and version %d", resp.Session, resp.AssignmentVersion,
			runner.AssignmentVersion)
	}

	if resp.Lease.Std() != runner.DefaultLease || !resp.ExpiresAt.Equal(s.opState().Claim.ExpiresAt.UTC()) {
		t.Fatalf("lease = %v expires %v, want the lease and the expiry of the claim", resp.Lease.Std(), resp.ExpiresAt)
	}

	checkAssignment(t, &resp.Assignment)

	// The runner now sees the session and stops renewing.
	if renewed := s.runner.renew(s.work.Operation); !renewed.SessionOpen {
		t.Fatalf("renew by the runner after the exchange = %+v, want sessionOpen", renewed)
	}
}

// checkAssignment checks the assignment of the plan phase of the test job.
func checkAssignment(t *testing.T, a *protocol.Assignment) {
	t.Helper()

	if a.Namespace != "acme" || a.Tool != "opentofu" || a.Job != phaseKey || a.Phase != string(job.PhasePlan) ||
		a.ServerURL != serverURL || a.GitCredentials != string(runner.GitCredentialsNone) ||
		a.Profile != "prf-default" {
		t.Fatalf("assignment = %+v, want the plan phase of the unit with git credentials none and the profile", a)
	}

	names := make([]string, 0, len(a.Steps))
	for _, st := range a.Steps {
		names = append(names, st.Name)
	}

	if !slices.Equal(names, []string{"init", "plan"}) {
		t.Fatalf("steps = %v, want the steps of the plan phase only", names)
	}

	if a.Source != nil || a.Plan != nil {
		t.Fatalf("assignment has a source or a plan: %+v", a)
	}
}

// TestOpenSessionUsesTheTokenOnce proves that a second exchange of one
// handoff token fails with token-used, and that a replay of one proof fails
// with bad-proof and opens nothing.
func TestOpenSessionUsesTheTokenOnce(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			token := s.runner.handoff(s.work.Operation)
			first := s.newExchange(token)

			if status, body := s.sendExchange(first); status != http.StatusOK {
				t.Fatalf("first exchange = %d %s, want 200", status, body)
			}

			status, body := s.sendExchange(first)
			if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorBadProof {
				t.Fatalf("replayed exchange = %d %s, want 401 bad-proof", status, body)
			}

			second := s.newExchange(token)

			status, body = s.sendExchange(second)
			if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorTokenUsed {
				t.Fatalf("second exchange = %d %s, want 401 token-used", status, body)
			}
		})
	}
}

// TestSessionRenewExtendsTheLease proves that a renewal moves the expiry of
// the claim, and that a renewal during a cancel says so and still extends it.
func TestSessionRenewExtendsTheLease(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()

			// Shorten the lease so that a renewal has a later expiry to show.
			short, err := s.store.operations.Extend(t.Context(), s.opState().Claim, time.Minute)
			if err != nil {
				t.Fatalf("extend: %v", err)
			}

			resp := renewOf(t, l)
			if !resp.ExpiresAt.After(short.ExpiresAt) || !resp.ExpiresAt.Equal(s.opState().Claim.ExpiresAt.UTC()) ||
				resp.Canceled || resp.SessionOpen {
				t.Fatalf("renew = %+v after %v, want the extended expiry of the store", resp, short.ExpiresAt)
			}

			if err = s.store.operations.Cancel(t.Context(), durable.ID(s.work.Operation)); err != nil {
				t.Fatalf("cancel: %v", err)
			}

			resp = renewOf(t, l)
			if !resp.Canceled || !resp.ExpiresAt.Equal(s.opState().Claim.ExpiresAt.UTC()) {
				t.Fatalf("renew during a cancel = %+v, want canceled with the extended expiry", resp)
			}
		})
	}
}

// renewOf renews the claim of the launcher and returns the answer.
func renewOf(t *testing.T, l *launcher) protocol.RenewResponse {
	t.Helper()

	status, body := l.do(l.post(protocol.PathSessionRenew, nil))
	if status != http.StatusOK {
		t.Fatalf("renew = %d %s, want 200", status, body)
	}

	var resp protocol.RenewResponse

	decode(t, body, &resp)

	return resp
}

// TestFinishTwice proves that a finish ends the phase once, and that a second
// finish fails with claim-lost and changes nothing.
func TestFinishTwice(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()
			finish := protocol.Finish{Succeeded: false, Message: "the plan failed"}

			l.expect(t, "finish", l.post(protocol.PathSessionFinish, finish), http.StatusNoContent, "")
			l.expect(t, "second finish", l.post(protocol.PathSessionFinish, finish), http.StatusConflict,
				protocol.ErrorClaimLost)

			if n := s.claimer.finished.Load(); n != 1 {
				t.Fatalf("phase finished %d times, want 1", n)
			}

			if o := s.claimer.outcome.Load(); o == nil || o.Succeeded || o.Message != finish.Message {
				t.Fatalf("outcome = %+v, want the failed outcome of the launcher", o)
			}

			// A step write after the end is refused: the claim is gone.
			begin := l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil)
			l.expect(t, "step begin after the finish", begin, http.StatusConflict, protocol.ErrorClaimLost)
		})
	}
}

// expect sends the call and fails unless the answer has the status and,
// when code is not empty, the code.
func (l *launcher) expect(t *testing.T, what string, c lcall, wantStatus int, code string) {
	t.Helper()

	status, body := l.do(c)
	if status != wantStatus || (code != "" && errorCode(t, body) != code) {
		t.Fatalf("%s = %d %s, want %d %s", what, status, body, wantStatus, code)
	}
}

// TestStepWriteWithALostClaim proves the fence of a step write: after another
// runner took the phase, no write of the old session reaches the recorder,
// also a write that carries a valid proof of the old session key.
func TestStepWriteWithALostClaim(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()

			l.expect(t, "step begin", l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil), 204, "")
			s.takeOver()

			writes := []lcall{
				l.post(stepPath(protocol.PathSessionStepBegin, "plan"), nil),
				l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
					ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: 0, LogSHA256: "",
				}),
				l.upload(logPath(0), []byte("late output")),
				l.post(protocol.PathSessionFinish, protocol.Finish{Succeeded: true, Message: ""}),
			}

			for _, c := range writes {
				l.expect(t, c.method+" "+c.path+" after the takeover", c, http.StatusConflict,
					protocol.ErrorClaimLost)
			}

			if got := s.jobs.called(); !slices.Equal(got, []string{"begin:init"}) {
				t.Fatalf("recorder calls = %v, want only the write before the takeover", got)
			}
		})
	}
}

// TestRecorderErrorsAnswerTheirCodes proves the code of each error that the
// recorder returns for the step and log paths.
func TestRecorderErrorsAnswerTheirCodes(t *testing.T) {
	t.Parallel()

	errors := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"canceled", job.ErrCanceled, http.StatusConflict, protocol.ErrorCanceled},
		{"step done", durable.ErrStepDone, http.StatusConflict, protocol.ErrorConflict},
		{"chunk conflict", job.ErrConflict, http.StatusConflict, protocol.ErrorConflict},
		{"invalid", job.ErrInvalid, http.StatusBadRequest, protocol.ErrorInvalid},
		{"claim lost", durable.ErrClaimLost, http.StatusConflict, protocol.ErrorClaimLost},
		{"job claim lost", job.ErrClaimLost, http.StatusConflict, protocol.ErrorClaimLost},
		{"stale fence", namespace.ErrFenced, http.StatusServiceUnavailable, protocol.ErrorUnavailable},
		{"unknown job", job.ErrNotFound, http.StatusNotFound, protocol.ErrorNotFound},
	}

	routes := []struct {
		name   string
		method string
		build  func(l *launcher) lcall
	}{
		{"begin", "begin", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil)
		}},
		{"end", "end", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
				ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: 0, LogSHA256: "",
			})
		}},
		{"log", "log", func(l *launcher) lcall { return l.upload(logPath(0), []byte("x")) }},
	}

	for _, b := range wiringBackends() {
		for _, rt := range routes {
			for _, tc := range errors {
				t.Run(b.name+"/"+rt.name+"/"+tc.name, func(t *testing.T) {
					t.Parallel()

					s := newSessionEnv(t, b)
					l := s.launch()

					s.jobs.failWith(rt.method, fmt.Errorf("record: %w", tc.err))

					status, body := l.do(rt.build(l))
					if status != tc.status || errorCode(t, body) != tc.code {
						t.Fatalf("answer = %d %s, want %d %s", status, body, tc.status, tc.code)
					}
				})
			}
		}
	}
}

// TestLogAndStepEnd proves a whole step: the begin, the chunks with a retry,
// and the end with the count and the hash of the log.
func TestLogAndStepEnd(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()
			chunks := [][]byte{[]byte("first\n"), []byte("second\n")}

			l.expect(t, "begin", l.post(stepPath(protocol.PathSessionStepBegin, "init"), nil), 204, "")

			for i, chunk := range chunks {
				l.expect(t, "chunk", l.upload(logPath(i), chunk), 204, "")
			}

			// A retry of a chunk changes nothing. A known number with another
			// body fails with conflict.
			l.expect(t, "retry of chunk 0", l.upload(logPath(0), chunks[0]), 204, "")
			l.expect(t, "chunk 0 with another body", l.upload(logPath(0), []byte("changed\n")),
				http.StatusConflict, protocol.ErrorConflict)

			checkStepEnd(t, l, chunks)

			// The three refused ends write nothing. Only the last end writes.
			want := []string{
				"begin:init", "log:init:0", "log:init:1", "log:init:0", "log:init:0", "refused:init", "refused:init",
				"refused:init", "end:init",
			}
			if got := s.jobs.called(); !slices.Equal(got[:len(want)], want) {
				t.Fatalf("recorder calls = %v, want %v first", got, want)
			}
		})
	}
}

// checkStepEnd ends the step init with a count or a hash of the log that is
// not the log that the server holds, and then with the right ones.
func checkStepEnd(t *testing.T, l *launcher, chunks [][]byte) {
	t.Helper()

	whole := sha256.Sum256(bytes.Join(chunks, nil))
	end := protocol.StepEnd{
		ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: len(chunks),
		LogSHA256: hex.EncodeToString(whole[:]),
	}

	badHash := end
	badHash.LogSHA256 = strings.Repeat("0", sha256.Size*2)

	badCount := end
	badCount.LogChunks = len(chunks) + 1

	path := stepPath(protocol.PathSessionStepEnd, "init")

	l.expect(t, "end with another log hash", l.post(path, badHash), http.StatusConflict, protocol.ErrorConflict)

	// The text names the cause: the log differs, not the job.
	if status, body := l.do(l.post(path, badHash)); status != http.StatusConflict ||
		!strings.Contains(string(body), "the log differs from the stored chunks") {
		t.Fatalf("end with another log hash = %d %s, want the text about the log", status, body)
	}
	l.expect(t, "end with another chunk count", l.post(path, badCount), http.StatusConflict,
		protocol.ErrorConflict)
	l.expect(t, "end", l.post(path, end), 204, "")
}

// requestCheck is a request of a valid session that fails a check of the
// handler.
type requestCheck struct {
	name  string
	build func(l *launcher) lcall
	code  string
}

// requestChecks returns the requests that TestSessionRequestsAreChecked sends.
func requestChecks() []requestCheck {
	big := bytes.Repeat([]byte("a"), job.MaxLogChunk+1)

	return []requestCheck{
		{"step name with a capital", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "Init"), nil)
		}, protocol.ErrorInvalid},
		{"step name with a leading dash", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "-init"), nil)
		}, protocol.ErrorInvalid},
		{"step name with a NUL", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "init%00"), nil)
		}, protocol.ErrorInvalid},
		{"step name with a CR", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "init%0D"), nil)
		}, protocol.ErrorInvalid},
		{"step name with an LF", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "init%0A"), nil)
		}, protocol.ErrorInvalid},
		{"step name with an encoded slash", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepBegin, "a%2Fb"), nil)
		}, protocol.ErrorInvalid},
		{"step name of an end with a NUL", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepEnd, "init%00"), protocol.StepEnd{
				ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: 0, LogSHA256: "",
			})
		}, protocol.ErrorInvalid},
		{"step name of a chunk with a CR", func(l *launcher) lcall {
			return l.upload(strings.NewReplacer("{step}", "init%0D", "{seq}", "0").Replace(protocol.PathSessionLog),
				[]byte("x"))
		}, protocol.ErrorInvalid},
		{"step name of a chunk with an encoded slash", func(l *launcher) lcall {
			return l.upload(strings.NewReplacer("{step}", "a%2Fb", "{seq}", "0").Replace(protocol.PathSessionLog),
				[]byte("x"))
		}, protocol.ErrorInvalid},
		{"exit code above 255", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
				ExitCode: 256, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: 0, LogSHA256: "",
			})
		}, protocol.ErrorInvalid},
		{"negative number of chunks", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
				ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: -1, LogSHA256: "",
			})
		}, protocol.ErrorInvalid},
		{"log hash that is not a hash", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepEnd, "init"), protocol.StepEnd{
				ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, LogChunks: 1, LogSHA256: "nope",
			})
		}, protocol.ErrorInvalid},
		{"step end with an unknown field", func(l *launcher) lcall {
			return l.post(stepPath(protocol.PathSessionStepEnd, "init"), map[string]any{"exit": 1})
		}, protocol.ErrorInvalid},
		{
			"chunk number below 0",
			func(l *launcher) lcall { return l.upload("/runner/v1/session/steps/init/logs/-1", []byte("x")) },
			protocol.ErrorInvalid,
		},
		{"chunk number past the cap", func(l *launcher) lcall {
			return l.upload(logPath(protocol.MaxLogChunksPerStep), []byte("x"))
		}, protocol.ErrorInvalid},
		{"chunk number that is not a number", func(l *launcher) lcall {
			return l.upload("/runner/v1/session/steps/init/logs/one", []byte("x"))
		}, protocol.ErrorInvalid},
		{"chunk above one MiB", func(l *launcher) lcall { return l.upload(logPath(0), big) }, protocol.ErrorInvalid},
		{"finish reason with a control character", func(l *launcher) lcall {
			return l.post(protocol.PathSessionFinish,
				protocol.Finish{Succeeded: false, Message: "line\x1b[31m"})
		}, protocol.ErrorInvalid},
		{"finish reason that is too long", func(l *launcher) lcall {
			return l.post(protocol.PathSessionFinish,
				protocol.Finish{Succeeded: false, Message: strings.Repeat("a", 2049)})
		}, protocol.ErrorInvalid},
		{"finish with an unknown field", func(l *launcher) lcall {
			return l.post(protocol.PathSessionFinish, map[string]any{"ok": true})
		}, protocol.ErrorInvalid},
	}
}

// TestSessionRequestsAreChecked proves the checks of the handler on a request
// of a valid session: each fails before the recorder or the claimer.
func TestSessionRequestsAreChecked(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		for _, tc := range requestChecks() {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				s := newSessionEnv(t, b)
				l := s.launch()

				l.expect(t, "request", tc.build(l), http.StatusBadRequest, tc.code)

				if got := s.jobs.called(); len(got) != 0 {
					t.Fatalf("a request that fails a check reached the recorder: %v", got)
				}

				if n := s.claimer.finished.Load(); n != 0 {
					t.Fatalf("a request that fails a check finished the phase %d times", n)
				}
			})
		}
	}
}

// TestLogChunkHashIsChecked proves the hash of an upload: a body that is not
// the signed body fails with bad-proof before the recorder stores a byte, and
// a header that is no SHA-256 fails with invalid.
func TestLogChunkHashIsChecked(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			l := s.launch()

			// The proof and the header carry the hash of "signed"; the body is
			// another text.
			c := l.upload(logPath(0), []byte("signed"))
			c.body = []byte("tampered")

			status, body := l.do(c)
			if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorBadProof {
				t.Fatalf("tampered body = %d %s, want 401 bad-proof", status, body)
			}

			path := logPath(0)
			now := s.clock.now()
			sg := signing{
				subject:     l.id,
				key:         l.priv,
				at:          now,
				nonce:       newNonce(t),
				release:     "0.4.0",
				contentHash: "not-a-hash",
				server:      "",
			}

			status, body = s.sendSigned(http.MethodPut, path, []byte("x"), sum([]byte("x")), sg)
			if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
				t.Fatalf("header that is no hash = %d %s, want 400 invalid", status, body)
			}

			if got := s.jobs.called(); len(got) != 0 {
				t.Fatalf("a refused chunk reached the recorder: %v", got)
			}
		})
	}
}

// TestSecretsNeverReachTheLog proves that the log the server writes holds none
// of the secrets that pass through a session: the handoff token, the reason of
// a finish, and the content of a log chunk, also when the request fails.
func TestSecretsNeverReachTheLog(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSessionEnv(t, b)
			token := s.runner.handoff(s.work.Operation)

			const (
				chunkLine  = "line of output that a tool printed"
				reasonText = "the reason that a launcher gave"
			)

			x := s.newExchange(token)

			status, body := s.sendExchange(x)
			if status != http.StatusOK {
				t.Fatalf("exchange = %d %s, want 200", status, body)
			}

			var opened protocol.SessionResponse

			decode(t, body, &opened)

			// The exchange fails once more with the token, and once with a
			// replayed proof.
			s.sendExchange(x)
			s.sendExchange(s.newExchange(token))

			l := &launcher{e: s, id: opened.Session, pub: x.pub, priv: x.priv}

			secrets := []string{token, chunkLine, reasonText, hex.EncodeToString(x.priv.Seed())}

			// Valid, failed and refused writes that carry the secrets.
			l.do(l.upload(logPath(0), []byte(chunkLine)))

			tampered := l.upload(logPath(1), []byte(chunkLine))
			tampered.body = []byte("tampered " + chunkLine)
			l.do(tampered)

			l.do(l.post(protocol.PathSessionFinish,
				protocol.Finish{Succeeded: false, Message: "bad\x00" + reasonText}))
			l.do(l.post(protocol.PathSessionFinish,
				protocol.Finish{Succeeded: false, Message: reasonText}))
			l.do(l.post(protocol.PathSessionFinish,
				protocol.Finish{Succeeded: false, Message: reasonText}))

			logged := s.logs.String()
			for _, secret := range secrets {
				if strings.Contains(logged, secret) {
					t.Fatalf("the server log holds a secret: %q", secret)
				}
			}

			if logged == "" {
				t.Fatal("the server log is empty: the test does not prove anything")
			}
		})
	}
}
