package runnerapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// tokenAuditPage returns the page of the audit reads of these tests.
func tokenAuditPage() audit.Page {
	return audit.Page{Size: audit.MaxPageSize}
}

// tokenEnv is a session environment whose handler issues job tokens: the
// service of the job tokens and the records of the principals are those of
// the opened store, the job reader is the job store itself, and the claimer
// is the real job claimer, so a poll claims the phase of a real job. The job
// of the environment has one tool step, called plan, and its starter is the
// principal ada.
type tokenEnv struct {
	*sessionEnv

	// audit holds the entries that the handler appended.
	audit *testAudit
	// tokens is the job token service of the handler, for a test that reads a
	// token back after the clock of the server moved.
	tokens  *jobtoken.Service
	unit    identity.Object
	job     job.Job
	starter principal.Principal
	// grantLost removes the grant of the starter in the authorizer of the
	// handler when it is set.
	grantLost *atomic.Bool
	// l is the launcher of the claim, after launch or launchDirect ran.
	l *launcher
}

// starterGrants is the authorizer of the token environment: the
// administrator may do each action, and the principal subject may start a plan
// on a unit until lost is set. It denies every other request.
type starterGrants struct {
	subject string
	lost    *atomic.Bool
}

func (g starterGrants) Authorize(_ context.Context, p identity.Principal, action identity.Action,
	obj identity.Object,
) error {
	if p.Admin {
		return nil
	}

	if p.Subject == g.subject && !g.lost.Load() && action == identity.ActionUnitRunPlan && obj.IsUnit() {
		return nil
	}

	return &identity.DeniedError{Subject: p.Subject, Action: action, Object: obj}
}

func (g starterGrants) ListObjects(ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	scope, err := identity.AdminOnly{}.ListObjects(ctx, p, action)
	if err != nil {
		return scope, fmt.Errorf("list objects: %w", err)
	}

	return scope, nil
}

// newTokenEnv opens the environment and runs it to the state where a runner of
// a fresh pool holds the claim of the first phase of a real dispatched job.
// poolLease is the lease of the pool (zero takes the default) and timeout is
// the timeout of the step of the job (zero takes the default).
func newTokenEnv(
	t *testing.T, poolLease, timeout time.Duration, changes ...func(cfg *runnerapi.Config),
) *tokenEnv {
	t.Helper()

	written := newTestAudit()
	logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	var tokens *jobtoken.Service

	lost := &atomic.Bool{}

	e := newEnvServing(t, openSQLite, func(s stores, o *serveOptions) {
		o.claimer = job.NewClaimer(s.jobs, s.operations, s.namespaces, jobtest.NoGates{})
		o.jobs = s.jobs
		o.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.authz = starterGrants{subject: "ada", lost: lost}

		records, ok := s.namespaces.(principal.Store)
		if !ok {
			t.Fatal("the store keeps no records of principals")
		}

		o.mutate = func(cfg *runnerapi.Config) {
			if a, ok := cfg.Audit.(*testAudit); ok {
				written = a
			}

			tokens = jobtoken.NewService(s.jobtokens, cfg.Now)
			cfg.JobTokens = tokens
			cfg.Starters = records

			for _, change := range changes {
				change(cfg)
			}
		}
	})

	pool := createTokenPool(t, e, poolLease)
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	unit := unitOf(t, e.store, e.fence, "network")
	ada := newStarter(t, e.store, "ada")
	j := dispatchCredentialsJob(t, e, pool, unit, timeout)

	work := r.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want the phase")
	}

	return &tokenEnv{
		sessionEnv: &sessionEnv{
			env: e, jobs: nil, claimer: nil, logs: logs, pool: pool, runner: r, work: work,
		},
		audit:     written,
		tokens:    tokens,
		unit:      unit,
		job:       j,
		starter:   ada,
		grantLost: lost,
		l:         nil,
	}
}

// launch opens the job session of the claim and keeps the launcher.
func (s *tokenEnv) launch() {
	s.l = s.sessionEnv.launch()
}

// launchDirect opens the job session through the store, as the exchange does.
// A test uses it when the exchange itself must not run, so that a check of
// the credentials path runs as if the checks of the exchange passed.
func (s *tokenEnv) launchDirect() *launcher {
	s.t.Helper()

	x := s.newExchange(s.runner.handoff(s.work.Operation))

	sess, err := s.store.runners.OpenSession(s.t.Context(), sha256Hex(x.token),
		runner.SessionKindLauncher, runner.Key{Public: x.pub, CreatedAt: time.Time{}}, s.clock.now())
	if err != nil {
		s.t.Fatalf("open session: %v", err)
	}

	s.l = &launcher{e: s.sessionEnv, id: sess.ID, pub: x.pub, priv: x.priv}

	return s.l
}

// ask asks for the credentials of the step named step and returns the status and
// the body.
func (s *tokenEnv) ask(step string) (int, []byte) {
	return s.l.do(s.l.post(protocol.PathSessionCredentials, protocol.CredentialsRequest{Step: step}))
}

// newSweepTokenEnv is the environment of the sweep case: a runner of a pool
// for sweeps holds the claim of one sweep, the exchange of the claim refuses,
// and the handler issues job tokens. The launcher of the claim opens its
// session through the store, as launchSweep does.
func newSweepTokenEnv(t *testing.T) *tokenEnv {
	t.Helper()

	jobs := newSessionJobs()

	var (
		tokens  *jobtoken.Service
		written *testAudit
	)

	e := newEnvServing(t, openSQLite, func(s stores, o *serveOptions) {
		claimer := &sessionClaimer{
			opsClaimer: &opsClaimer{ops: s.operations, released: atomic.Int64{}},
			finished:   atomic.Int64{}, outcome: atomic.Pointer[job.Outcome]{},
		}
		o.claimer, o.recorder, o.jobs = claimer, jobs, jobs

		records, ok := s.namespaces.(principal.Store)
		if !ok {
			t.Fatal("the store keeps no records of principals")
		}

		o.mutate = func(cfg *runnerapi.Config) {
			cfg.Sweeps = fakeSweeps{ops: s.operations}
			tokens = jobtoken.NewService(s.jobtokens, cfg.Now)
			cfg.JobTokens = tokens
			cfg.Starters = records

			if a, ok := cfg.Audit.(*testAudit); ok {
				written = a
			}
		}
	})

	status, body := e.admin("/api/dustpan/v1/namespaces/acme/runner-pools",
		map[string]any{"name": "scan", "purpose": "sweeps"})
	if status != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", status, body)
	}

	var pool struct {
		ID string `json:"id"`
	}

	decode(t, body, &pool)

	r := e.newRunner("scanner-1")
	if status, body = r.join(pool.ID, e.joinToken(pool.ID)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	start := durable.StartRequest{Key: "sweep-1", Kind: runner.SweepKind(pool.ID), Version: 0, Input: nil}
	if _, _, err := e.store.operations.Start(t.Context(), start); err != nil {
		t.Fatalf("start sweep: %v", err)
	}

	work := r.poll(2 * time.Second)
	if work == nil || work.Sweep != "sweep-1" {
		t.Fatalf("poll = %+v, want the sweep", work)
	}

	// A sweep has no unit, no job and no starter, so those fields stay empty.
	var s tokenEnv

	s.sessionEnv = &sessionEnv{
		env: e, jobs: nil, claimer: nil, logs: nil, pool: pool.ID, runner: r, work: work,
	}
	s.audit, s.tokens = written, tokens

	return &s
}

// createTokenPool makes a pool for jobs and returns its ID. A lease of zero
// takes the default.
func createTokenPool(t *testing.T, e *env, lease time.Duration) string {
	t.Helper()

	body := map[string]any{"name": "prod", "purpose": "jobs"}
	if lease != 0 {
		body["lease"] = lease.Milliseconds()
	}

	status, resp := e.admin("/api/dustpan/v1/namespaces/acme/runner-pools", body)
	if status != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", status, resp)
	}

	var pool struct {
		ID string `json:"id"`
	}

	decode(t, resp, &pool)

	if pool.ID == "" {
		t.Fatalf("create pool = %s, want an ID", resp)
	}

	return pool.ID
}

// newStarter makes the principal with the subject name subject in the store.
func newStarter(t *testing.T, s stores, subject string) principal.Principal {
	t.Helper()

	records, ok := s.namespaces.(principal.Store)
	if !ok {
		t.Fatal("the store keeps no records of principals")
	}

	p := principal.Principal{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), UpdatedAt: time.UnixMilli(1_700_000_000_000).UTC(),
		DisabledAt: time.Time{}, ID: ident.NewID("prn-"), Subject: subject, DisplayName: subject,
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	entry := storetest.ServerChange(p.CreatedAt, principal.Ref(p.ID))
	if err := records.CreatePrincipal(t.Context(), p, entry); err != nil {
		t.Fatalf("create principal %q: %v", subject, err)
	}

	return p
}

// dispatchCredentialsJob stores a plan job of the pool on unit, with one tool
// step of the length timeout (zero takes the default) and the starter ada,
// dispatched as the durable operation of its phase.
func dispatchCredentialsJob(
	t *testing.T, e *env, pool string, unit identity.Object, timeout time.Duration,
) job.Job {
	t.Helper()

	j := job.Job{
		ID:          job.ID(ident.NewID(job.IDPrefix)),
		NamespaceID: "",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
			},
			LocationSource: job.LocationSourceRequest,
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
				Dir: "", Timeout: timeout, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: pool, Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
		},
		State:  job.StateQueued,
		Wait:   job.WaitNone,
		Reason: "",
		Phases: []job.Phase{{
			Kind: job.PhasePlan, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil,
			Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
		}},
		Current:        0,
		StartedBy:      "ada",
		Source:         job.SourceAPI,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "",
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}

	start := &durable.StartRequest{Key: job.PhaseKey(j.ID, 0), Kind: job.RunnerKind(pool), Version: 1, Input: nil}

	stored, created, err := e.store.jobs.Create(t.Context(), j, start, e.fence, nil)
	if err != nil || !created {
		t.Fatalf("create job = created %v, %v; want a dispatched job", created, err)
	}

	return stored
}

// The tests TestSessionCredentials... prove the credentials path of the job
// session: a live session gets one job token per request with the earlier
// expiry and one audit entry that names the job, the step and the expiry; a
// claim that is not live, a job with no stable unit ID, a starter that lost
// its record and a sweep get one denial entry each and no token; the claim
// answer holds no job token.

// TestSessionCredentials proves that a live session gets its token.
func TestSessionCredentials(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()

	claimEnd := s.opState().Claim.ExpiresAt

	status, body := s.ask("plan")
	if status != http.StatusOK {
		t.Fatalf("credentials = %d %s, want 200", status, body)
	}

	var resp protocol.CredentialsResponse

	decode(t, body, &resp)

	if !strings.HasPrefix(resp.Token, jobtoken.SecretPrefix) {
		t.Fatalf("token = %q, want a %s secret", resp.Token, jobtoken.SecretPrefix)
	}

	if !resp.ExpiresAt.Equal(claimEnd) {
		t.Fatalf("expiry = %s, want %s, the end of the claim", resp.ExpiresAt, claimEnd)
	}

	if _, err := s.tokens.AuthenticateToken(t.Context(), resp.Token); err != nil {
		t.Fatalf("authenticate the new token: %v", err)
	}
}

// TestSessionCredentialsStepEnd proves that the step bound ends the token
// before the claim does.
func TestSessionCredentialsStepEnd(t *testing.T) {
	t.Parallel()

	// A lease of ten minutes and a step of one minute: the step bound
	// ends first, and the token expires at the end of the step plus its
	// grace period, while the claim runs on.
	s := newTokenEnv(t, 10*time.Minute, time.Minute)
	s.launch()

	before := s.clock.now().UTC()

	status, body := s.ask("plan")
	if status != http.StatusOK {
		t.Fatalf("credentials = %d %s, want 200", status, body)
	}

	after := s.clock.now().UTC()

	var resp protocol.CredentialsResponse

	decode(t, body, &resp)

	earliest := before.Add(time.Minute + runner.GraceOther)
	latest := after.Add(time.Minute + runner.GraceOther)
	if resp.ExpiresAt.Before(earliest) || resp.ExpiresAt.After(latest) {
		t.Fatalf("expiry = %s, want the end of the step plus grace, between %s and %s",
			resp.ExpiresAt, earliest, latest)
	}

	// The claim of ten minutes stays live, and the token of the step does
	// not: the step bound ends it before the claim does.
	s.clock.advance(2 * time.Minute)

	if _, err := s.tokens.AuthenticateToken(t.Context(), resp.Token); err == nil {
		t.Fatal("the token of the ended step still authenticates after its expiry")
	}
}

// TestClaimAnswerHoldsNoJobToken proves that the claim answer holds no job
// token.
func TestClaimAnswerHoldsNoJobToken(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)

	// A second job of the same pool waits for its claim. The next poll
	// claims it, so the answer of that poll is a claim answer.
	dispatchCredentialsJob(t, s.env, s.pool, s.unit, 0)

	status, body := s.runner.call(protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(0), AssignmentVersion: runner.AssignmentVersion,
	})
	if status != http.StatusOK {
		t.Fatalf("poll = %d %s, want 200", status, body)
	}

	if strings.Contains(string(body), jobtoken.SecretPrefix) {
		t.Fatalf("the claim answer holds a job token: %s", body)
	}
}

// TestSessionCredentialsIssueEntry proves that one issue entry names the job,
// the step and the expiry.
func TestSessionCredentialsIssueEntry(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()

	if status, body := s.ask("plan"); status != http.StatusOK {
		t.Fatalf("credentials = %d %s, want 200", status, body)
	}

	entries := issuedEntries(t, s)
	if len(entries) != 1 {
		t.Fatalf("the store holds %d issue entries, want 1", len(entries))
	}

	e := entries[0]
	if e.Subject != s.l.id {
		t.Errorf("issue subject = %q, want the session %q", e.Subject, s.l.id)
	}

	if e.JobID != string(s.job.ID) {
		t.Errorf("issue job = %q, want %q", e.JobID, string(s.job.ID))
	}

	if !holdsChange(e, "step", "plan") {
		t.Errorf("the issue entry names no step: %+v", e.Changes)
	}

	at, ok := changeValue(e, "expiresAt")
	if !ok {
		t.Errorf("the issue entry names no expiry: %+v", e.Changes)
	} else if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Errorf("issue expiry %q is not an RFC 3339 time: %v", at, err)
	}

	for _, c := range e.Changes {
		if strings.Contains(c.New, jobtoken.SecretPrefix) {
			t.Errorf("the issue entry holds token text in %q", c.Key)
		}
	}
}

// TestSessionCredentialsClaimEnded proves that a request after the claim
// ended gets no token.
func TestSessionCredentialsClaimEnded(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()
	s.lapse(s.work.Operation)

	// The session layer checks the claim on every request, so the refusal
	// happens before the credentials path.
	status, body := s.ask("plan")
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorClaimLost {
		t.Fatalf("credentials after the claim ended = %d %s, want 409 claim-lost", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("the ended claim got a token")
	}

	if got := refusedEntries(t, s); len(got) != 0 {
		t.Fatalf("the refusal wrote %d credentials entries, want 0", len(got))
	}

	checkSessionRefusalLog(t, s, "claim-lost")
}

// TestSessionCredentialsTakeover proves that a request after a takeover gets
// no token.
func TestSessionCredentialsTakeover(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()
	s.takeOver()

	status, body := s.ask("plan")
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorClaimLost {
		t.Fatalf("credentials after the takeover = %d %s, want 409 claim-lost", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("the taken-over claim got a token")
	}

	if got := refusedEntries(t, s); len(got) != 0 {
		t.Fatalf("the refusal wrote %d credentials entries, want 0", len(got))
	}

	checkSessionRefusalLog(t, s, "claim-lost")
}

// TestSessionCredentialsDeadClaimInPath proves that the claim check of the
// path denies a dead claim when the session layer lets it pass.
func TestSessionCredentialsDeadClaimInPath(t *testing.T) {
	t.Parallel()

	// The hostile case of the claim check, run as if the session layer
	// failed: the session is live for the store of the sessions, and the
	// operation that the credentials path reads is not. The path refuses,
	// writes one denial entry that names the job, the step and the reason,
	// and makes no token.
	var dead atomic.Bool

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Operations = deadClaimOps{Store: cfg.Operations, dead: &dead}
	})
	s.launchDirect()
	dead.Store(true)

	status, body := s.ask("plan")
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorClaimLost {
		t.Fatalf("credentials of a dead claim = %d %s, want 409 claim-lost", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a dead claim got a token")
	}

	denied := refusedEntries(t, s)
	if len(denied) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(denied))
	}

	checkDenial(t, denied[0], s.l.id, string(s.job.ID), "claim-lost")
}

// TestSessionCredentialsRetiredRunner proves that a retired runner gets no
// token.
func TestSessionCredentialsRetiredRunner(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()

	status, body := s.admin("/api/dustpan/v1/namespaces/acme/runners/"+s.runner.id+"/retire", nil)
	if status != http.StatusNoContent {
		t.Fatalf("retire = %d %s, want 204", status, body)
	}

	status, body = s.ask("plan")
	if status != http.StatusForbidden || errorCode(t, body) != protocol.ErrorRetired {
		t.Fatalf("credentials of a retired runner = %d %s, want 403 retired", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a retired runner got a token")
	}

	if got := refusedEntries(t, s); len(got) != 0 {
		t.Fatalf("the refusal wrote %d credentials entries, want 0", len(got))
	}

	checkSessionRefusalLog(t, s, "retired")
}

// TestSessionCredentialsSessionLogOtherPath proves that a session-layer
// refusal of another path of the job session writes no record of the
// credentials path.
func TestSessionCredentialsSessionLogOtherPath(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()
	s.lapse(s.work.Operation)

	status, body := s.l.do(s.l.post(protocol.PathSessionRenew, nil))
	if status != http.StatusConflict {
		t.Fatalf("renew after the claim ended = %d %s, want 409", status, body)
	}

	if n := strings.Count(s.logs.String(), credentialsRefusedLog); n != 0 {
		t.Fatalf("the renew refusal wrote %d credentials records, want 0:\n%s", n, s.logs.String())
	}
}

// credentialsRefusedLog is the message of the log record of a credentials
// request that the session layer refused.
const credentialsRefusedLog = `msg="credentials request refused by the session layer"`

// checkSessionRefusalLog checks the log of a credentials request that the
// session layer refused: exactly one record, with the reason and the short
// hash of the session, and with no session ID and no token text.
func checkSessionRefusalLog(t *testing.T, s *tokenEnv, reason string) {
	t.Helper()

	logs := s.logs.String()

	var records []string

	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, credentialsRefusedLog) {
			records = append(records, line)
		}
	}

	if len(records) != 1 {
		t.Fatalf("the log holds %d credentials refusal records, want 1:\n%s", len(records), logs)
	}

	rec := records[0]
	if !strings.Contains(rec, "reason="+reason) {
		t.Errorf("the record names no reason %q: %s", reason, rec)
	}

	if !strings.Contains(rec, "session="+sha256Hex(s.l.id)[:16]) {
		t.Errorf("the record names no short hash of the session: %s", rec)
	}

	// The open of the session logs its ID by design; the refusal record
	// holds only its short hash.
	if strings.Contains(rec, s.l.id) {
		t.Errorf("the record holds the session ID: %s", rec)
	}

	if strings.Contains(logs, jobtoken.SecretPrefix) {
		t.Error("the log holds token text")
	}
}

// TestSessionCredentialsNoUnitID proves that a job with no stable unit ID
// gets no token.
func TestSessionCredentialsNoUnitID(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Jobs = unitlessJobs{inner: cfg.Jobs}
	})
	s.launch()

	status, body := s.ask("plan")
	if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
		t.Fatalf("credentials of a unitless job = %d %s, want 400 invalid", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a job with no stable unit ID got a token")
	}

	denied := refusedEntries(t, s)
	if len(denied) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(denied))
	}

	checkDenial(t, denied[0], s.l.id, string(s.job.ID), "no-unit")
}

// TestSessionCredentialsStarterGone proves that a starter that lost its
// record gets no token.
func TestSessionCredentialsStarterGone(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()

	records, ok := s.store.namespaces.(principal.Store)
	if !ok {
		t.Fatal("the store keeps no records of principals")
	}

	entry := storetest.ServerChange(s.clock.now().UTC(), principal.Ref(s.starter.ID))
	if err := records.DeletePrincipal(t.Context(), s.starter.ID, entry); err != nil {
		t.Fatalf("delete the starter: %v", err)
	}

	status, body := s.ask("plan")
	if status != http.StatusNotFound {
		t.Fatalf("credentials of a lost starter = %d %s, want 404", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a starter with no record got a token")
	}

	denied := refusedEntries(t, s)
	if len(denied) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(denied))
	}

	checkDenial(t, denied[0], s.l.id, string(s.job.ID), "starter-gone")
}

// TestSessionCredentialsGrantLost proves that a starter that keeps its record
// but lost the grant to run the job gets no token.
func TestSessionCredentialsGrantLost(t *testing.T) {
	t.Parallel()

	// The hostile case of the grant check, run as if the other layers
	// failed: the record of the starter exists and is enabled, so the token
	// store would issue the token. Only the grant check of the path refuses.
	s := newTokenEnv(t, 0, 0)
	s.launch()
	s.grantLost.Store(true)

	status, body := s.ask("plan")
	checkGrantLost(t, s, status, body)
}

// TestSessionCredentialsStarterDisabled proves that a disabled starter gets no
// token.
func TestSessionCredentialsStarterDisabled(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()

	records, ok := s.store.namespaces.(principal.Store)
	if !ok {
		t.Fatal("the store keeps no records of principals")
	}

	entry := storetest.ServerChange(s.clock.now().UTC(), principal.Ref(s.starter.ID))
	if err := records.DisablePrincipal(t.Context(), s.starter.ID, entry); err != nil {
		t.Fatalf("disable the starter: %v", err)
	}

	// The token store refuses a disabled starter too, with claim-lost. The
	// answer grant-lost shows that the check of the path refused first.
	status, body := s.ask("plan")
	checkGrantLost(t, s, status, body)
}

// checkGrantLost checks the refusal of a starter without its grant: 403
// grant-lost with the fixed text, no token, no issue entry and one denial
// entry that names the job, the step and the reason.
func checkGrantLost(t *testing.T, s *tokenEnv, status int, body []byte) {
	t.Helper()

	if status != http.StatusForbidden || errorCode(t, body) != protocol.ErrorGrantLost {
		t.Fatalf("credentials of a starter without the grant = %d %s, want 403 grant-lost", status, body)
	}

	var answer protocol.Error

	decode(t, body, &answer)

	if !strings.HasPrefix(answer.Message, "Grant lost") {
		t.Errorf("message = %q, want the text that starts with Grant lost", answer.Message)
	}

	if strings.Contains(string(body), jobtoken.SecretPrefix) || strings.Contains(string(body), "ada") {
		t.Errorf("the answer holds a token or the starter: %s", body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a starter without the grant got a token")
	}

	denied := refusedEntries(t, s)
	if len(denied) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(denied))
	}

	checkDenial(t, denied[0], s.l.id, string(s.job.ID), "grant-lost")
}

// TestSessionCredentialsBadStepName proves that the boundary check of the path
// refuses a step name that is not valid, as if the job had a step of that
// name: no token, no step name in the answer, and one denial entry for each
// request that holds the reason and a fixed placeholder in place of the name.
func TestSessionCredentialsBadStepName(t *testing.T) {
	t.Parallel()

	names := []string{
		"../x", "", "PLAN", "plan/../../etc", "plan\nevent=credentials-issued", "plan%00", strings.Repeat("a", 200),
	}

	// The hostile case of the boundary check, run as if the check of the job
	// spec had failed: the job reader answers a job that has a step of each
	// name, so only the check of the step name refuses.
	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Jobs = hostileStepJobs{inner: cfg.Jobs, names: names}
	})
	s.launch()

	for _, name := range names {
		status, body := s.ask(name)
		if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
			t.Errorf("credentials of step %q = %d %s, want 400 invalid", name, status, body)
		}

		if name != "" && strings.Contains(string(body), name) {
			t.Errorf("the answer of step %q holds the step name: %s", name, body)
		}
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a step name that is not valid got a token")
	}

	got := refusedEntries(t, s)
	if len(got) != len(names) {
		t.Fatalf("step names that are not valid wrote %d denial entries, want %d", len(got), len(names))
	}

	for i := range got {
		checkDenialStep(t, got[i], denialWant{session: s.l.id, job: "", step: "(invalid)", reason: "invalid-step"})
	}
}

// TestSessionCredentialsSweep proves that the launcher of a sweep gets no
// token.
func TestSessionCredentialsSweep(t *testing.T) {
	t.Parallel()

	s := newSweepTokenEnv(t)
	l := s.launchSweep()

	status, body := l.do(l.post(protocol.PathSessionCredentials, protocol.CredentialsRequest{Step: "plan"}))
	if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
		t.Fatalf("credentials of a sweep = %d %s, want 400 invalid", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("the launcher of a sweep got a token")
	}

	denied := refusedEntries(t, s)
	if len(denied) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(denied))
	}

	// A sweep names no job: the entry holds the session, the step and the
	// reason only.
	checkDenial(t, denied[0], l.id, "", "sweep")
}

// TestSessionCredentialsUnknownStep proves that an unknown step gets no token
// and one denial entry.
func TestSessionCredentialsUnknownStep(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0)
	s.launch()

	status, body := s.ask("nope")
	if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
		t.Fatalf("credentials of an unknown step = %d %s, want 400 invalid", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("an unknown step got a token")
	}

	got := refusedEntries(t, s)
	if len(got) != 1 {
		t.Fatalf("an unknown step wrote %d denial entries, want 1", len(got))
	}

	checkDenialStep(t, got[0], denialWant{session: s.l.id, job: string(s.job.ID), step: "nope", reason: "unknown-step"})
}

// checkDenial checks one denial entry of the credentials path: its outcome is
// denied and it names the session, the step plan, the reason and, when job is
// not empty, the job. It holds no token text.
func checkDenial(t *testing.T, e audit.Entry, session, jobID, reason string) {
	t.Helper()

	checkDenialStep(t, e, denialWant{session: session, job: jobID, step: "plan", reason: reason})
}

// denialWant is what a denial entry of the credentials path must name.
type denialWant struct {
	session, job, step, reason string
}

// checkDenialStep is checkDenial for a denial entry that names any step.
func checkDenialStep(t *testing.T, e audit.Entry, want denialWant) {
	t.Helper()

	session, jobID, step, reason := want.session, want.job, want.step, want.reason

	if e.Outcome != audit.OutcomeDenied {
		t.Errorf("denial outcome = %q, want denied", e.Outcome)
	}

	if e.Subject != session {
		t.Errorf("denial subject = %q, want the session %q", e.Subject, session)
	}

	if jobID != "" && e.JobID != jobID {
		t.Errorf("denial job = %q, want %q", e.JobID, jobID)
	}

	if !holdsChange(e, "step", step) {
		t.Errorf("the denial entry names no step %q: %+v", step, e.Changes)
	}

	if !holdsChange(e, "reason", reason) {
		t.Errorf("the denial entry names no reason %q: %+v", reason, e.Changes)
	}

	if strings.Contains(e.Reason, jobtoken.SecretPrefix) {
		t.Error("the denial reason holds token text")
	}

	for _, c := range e.Changes {
		if strings.Contains(c.New, jobtoken.SecretPrefix) {
			t.Errorf("the denial entry holds token text in %q", c.Key)
		}
	}
}

// issuedEntries reads the issue entries that the store of the environment
// holds.
func issuedEntries(t *testing.T, s *tokenEnv) []audit.Entry {
	t.Helper()

	return entriesOfEvent(t, "credentials-issued", func() (audit.Listing, error) {
		return s.store.audit.List(t.Context(), namespace.Name("acme"), tokenAuditPage())
	})
}

// refusedEntries reads the entries of the credentials refusals that the
// handler of the environment appended.
func refusedEntries(t *testing.T, s *tokenEnv) []audit.Entry {
	t.Helper()

	return entriesOfEvent(t, "credentials-refused", func() (audit.Listing, error) {
		return s.audit.List(t.Context(), namespace.Name("acme"), tokenAuditPage())
	})
}

// entriesOfEvent returns the entries of the listing whose event is event.
func entriesOfEvent(t *testing.T, event string, list func() (audit.Listing, error)) []audit.Entry {
	t.Helper()

	got, err := list()
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	out := make([]audit.Entry, 0, len(got.Entries))

	for i := range got.Entries {
		if holdsChange(got.Entries[i], "event", event) {
			out = append(out, got.Entries[i])
		}
	}

	return out
}

// holdsChange reports whether e carries a change of key with the value value.
func holdsChange(e audit.Entry, key, value string) bool {
	got, ok := changeValue(e, key)

	return ok && got == value
}

// changeValue returns the value of the change of key and whether e carries it.
func changeValue(e audit.Entry, key string) (string, bool) {
	for _, c := range e.Changes {
		if c.Key == key {
			return c.New, true
		}
	}

	return "", false
}

// unitlessJobs is a job reader that answers the job of its reader with the
// stable unit ID removed, as a job of a version that kept no stable IDs. It
// is the hostile case of the boundary check of the credentials path, run as
// if the check of the create had failed.
type unitlessJobs struct {
	inner runnerapi.JobReader
}

func (f unitlessJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	j.Spec.Unit = identity.Object{}

	return j, nil
}

// hostileStepJobs is a job reader that answers the job of its reader with one
// more step of the plan phase for each name in names, as a job of a spec
// check that failed.
type hostileStepJobs struct {
	inner runnerapi.JobReader
	names []string
}

func (f hostileStepJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	steps := make([]job.Step, 0, len(j.Spec.Steps)+len(f.names))
	steps = append(steps, j.Spec.Steps...)

	for _, name := range f.names {
		st := j.Spec.Steps[0]
		st.Name = name
		steps = append(steps, st)
	}

	j.Spec.Steps = steps

	return j, nil
}

// deadClaimOps is the durable store of the hostile case of the claim check:
// after dead is set, every read of an operation answers an operation that is
// over, as a claim that ended between the check of the session and the read
// of the path.
type deadClaimOps struct {
	durable.Store

	dead *atomic.Bool
}

func (s deadClaimOps) Get(ctx context.Context, id durable.ID) (durable.Operation, error) {
	op, err := s.Store.Get(ctx, id)
	if err != nil {
		return durable.Operation{}, err
	}

	if s.dead.Load() {
		op.State = durable.StateSucceeded
	}

	return op, nil
}

// TestSessionCredentialsStepNotInPhase proves that a step of another phase
// gets no token and one denial entry.
func TestSessionCredentialsStepNotInPhase(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Jobs = otherPhaseJobs{inner: cfg.Jobs}
	})
	s.launch()

	status, body := s.ask("plan")
	if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
		t.Fatalf("credentials of a step of another phase = %d %s, want 400 invalid", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a step of another phase got a token")
	}

	got := refusedEntries(t, s)
	if len(got) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(got))
	}

	checkDenial(t, got[0], s.l.id, string(s.job.ID), "unknown-step")
}

// TestSessionCredentialsAuthorizerFails proves the fail-closed layer of the
// grant check: an authorizer that returns an error that is not a denial gives
// no token and no issue entry, and the answer is 5xx.
func TestSessionCredentialsAuthorizerFails(t *testing.T) {
	t.Parallel()

	broken := &atomic.Bool{}

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Authorizer = failingStarterAuthz{inner: cfg.Authorizer, broken: broken}
	})
	s.launch()
	broken.Store(true)

	status, body := s.ask("plan")
	if status < http.StatusInternalServerError {
		t.Fatalf("credentials with an authorizer that fails = %d %s, want 5xx", status, body)
	}

	if strings.Contains(string(body), jobtoken.SecretPrefix) {
		t.Errorf("the answer holds token text: %s", body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("an authorizer that fails gave a token")
	}

	failed := entriesOfEvent(t, "credentials-failed", func() (audit.Listing, error) {
		return s.audit.List(t.Context(), namespace.Name("acme"), tokenAuditPage())
	})
	if len(failed) != 1 {
		t.Fatalf("the failure wrote %d entries, want 1", len(failed))
	}

	if failed[0].Outcome != audit.OutcomeFailure || !holdsChange(failed[0], "reason", "server-error") ||
		failed[0].Reason != "server-error" || failed[0].JobID != string(s.job.ID) {
		t.Errorf("failure entry = %+v, want outcome failure, reason server-error and the job", failed[0])
	}

	if strings.Contains(failed[0].Reason, errAuthorizerDown.Error()) {
		t.Error("the failure entry holds the text of the error")
	}
}

// TestSessionCredentialsFailureEntryAppendFails proves the last fail-closed
// layer: when the entry of a failed request cannot be written, the request
// still gets no token and the answer is 5xx, and the handler logs the failed
// append.
func TestSessionCredentialsFailureEntryAppendFails(t *testing.T) {
	t.Parallel()

	broken := &atomic.Bool{}

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Authorizer = failingStarterAuthz{inner: cfg.Authorizer, broken: broken}
		cfg.Audit = new(appendFailsAudit)
	})
	s.launch()
	broken.Store(true)

	status, body := s.ask("plan")
	if status < http.StatusInternalServerError {
		t.Fatalf("credentials with no way to write the entry = %d %s, want 5xx", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a request whose entry failed got a token")
	}

	if !strings.Contains(s.logs.String(), "audit append failed") {
		t.Error("the handler logged no failed append")
	}
}

// appendFailsAudit is an audit store whose fenced append always fails.
type appendFailsAudit struct{ testAudit }

var errAppendDown = errors.New("append down")

func (*appendFailsAudit) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAppendDown
}

// TestSessionCredentialsUnknownStartAction proves the fail-closed layer of the
// start requirements: a job whose start action is unknown gets no token and one
// denial entry, and does not pass the grant loop with no requirements.
func TestSessionCredentialsUnknownStartAction(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Jobs = unknownOperationJobs{inner: cfg.Jobs}
	})
	s.launch()

	status, body := s.ask("plan")
	checkGrantLost(t, s, status, body)
}

// TestSessionCredentialsExpiryNotAfterNow proves the expiry layer of the path:
// an expiry that is not after the clock of the server gets no token and one
// denial entry, as if the claim ended between the session check and the issue.
func TestSessionCredentialsExpiryNotAfterNow(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Jobs = pastTimeoutJobs{inner: cfg.Jobs}
	})
	s.launch()

	status, body := s.ask("plan")
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorClaimLost {
		t.Fatalf("credentials with an ended expiry = %d %s, want 409 claim-lost", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("an expiry that is not after now got a token")
	}

	got := refusedEntries(t, s)
	if len(got) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(got))
	}

	checkDenial(t, got[0], s.l.id, string(s.job.ID), "expired")
}

// TestSessionCredentialsNoIssuer proves that a server with no job token issuer
// has no credentials path.
func TestSessionCredentialsNoIssuer(t *testing.T) {
	t.Parallel()

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.JobTokens = nil
	})
	s.launch()

	status, body := s.ask("plan")
	if status != http.StatusNotFound {
		t.Fatalf("credentials of a server with no issuer = %d %s, want 404", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a server with no issuer gave a token")
	}
}

// errAuthorizerDown is the error of an authorizer that does not answer.
var errAuthorizerDown = errors.New("the authorizer does not answer")

// failingStarterAuthz answers an error that is not a denial for the starter
// ada once broken is set, and asks its inner authorizer for every other
// request.
type failingStarterAuthz struct {
	inner  identity.Authorizer
	broken *atomic.Bool
}

func (f failingStarterAuthz) Authorize(ctx context.Context, p identity.Principal, action identity.Action,
	obj identity.Object,
) error {
	if p.Subject == "ada" && f.broken.Load() {
		return errAuthorizerDown
	}

	return f.inner.Authorize(ctx, p, action, obj)
}

func (f failingStarterAuthz) ListObjects(ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	scope, err := f.inner.ListObjects(ctx, p, action)
	if err != nil {
		return scope, fmt.Errorf("list objects: %w", err)
	}

	return scope, nil
}

// unknownOperationJobs is a job reader that answers the job of its reader with
// an operation that no start action matches.
type unknownOperationJobs struct {
	inner runnerapi.JobReader
}

func (f unknownOperationJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	j.Spec.Operation = "no-such-operation"

	return j, nil
}

// pastTimeoutJobs is a job reader that answers the job of its reader with
// steps whose timeout is far in the past, so that the expiry of the token is
// not after the clock of the server.
type pastTimeoutJobs struct {
	inner runnerapi.JobReader
}

func (f pastTimeoutJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	steps := append([]job.Step(nil), j.Spec.Steps...)
	for i := range steps {
		steps[i].Timeout = -24 * time.Hour
	}

	j.Spec.Steps = steps

	return j, nil
}

// otherPhaseJobs is a job reader that answers the job of its reader with steps
// of a phase that is not the phase of the claim.
type otherPhaseJobs struct {
	inner runnerapi.JobReader
}

func (f otherPhaseJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	steps := append([]job.Step(nil), j.Spec.Steps...)
	for i := range steps {
		steps[i].Phase = "no-such-phase"
	}

	j.Spec.Steps = steps

	return j, nil
}

// TestSessionCredentialsPhaseOutOfRange proves that a claim with a phase that
// the job does not have gets no token and one denial entry with the reason of
// an unknown step.
func TestSessionCredentialsPhaseOutOfRange(t *testing.T) {
	t.Parallel()

	stripped := &atomic.Bool{}

	s := newTokenEnv(t, 0, 0, func(cfg *runnerapi.Config) {
		cfg.Jobs = noPhaseJobs{inner: cfg.Jobs, stripped: stripped}
	})
	s.launch()
	stripped.Store(true)

	status, body := s.ask("plan")
	if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
		t.Fatalf("credentials with a phase out of range = %d %s, want 400 invalid", status, body)
	}

	if len(issuedEntries(t, s)) != 0 {
		t.Fatal("a claim with a phase out of range got a token")
	}

	got := refusedEntries(t, s)
	if len(got) != 1 {
		t.Fatalf("the refusal wrote %d denial entries, want 1", len(got))
	}

	checkDenial(t, got[0], s.l.id, string(s.job.ID), "unknown-step")
}

// noPhaseJobs is a job reader that answers the job of its reader with no
// phases once stripped is set, so that the phase of the claim is out of range.
type noPhaseJobs struct {
	inner    runnerapi.JobReader
	stripped *atomic.Bool
}

func (f noPhaseJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	if f.stripped.Load() {
		j.Phases = nil
	}

	return j, nil
}
