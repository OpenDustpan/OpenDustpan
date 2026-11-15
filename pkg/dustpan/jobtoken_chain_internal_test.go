package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/jobunits"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// jobTokenAddress is the source address of the valid job token lookups of the
// tests. The fixture of the budgets spends the budgets of budgetAddress.
const jobTokenAddress = "203.0.113.9"

// errJobsDown is a job store that cannot answer.
var errJobsDown = errors.New("the job store is down")

// jobTokenNow returns the clock of the tests.
func jobTokenNow() time.Time {
	return time.UnixMilli(1_700_000_000_000).UTC()
}

// fakeJobTokens is a jobtoken.Store that knows one record. It answers
// jobtoken.ErrNotFound for every other digest, as an adapter does.
type fakeJobTokens struct {
	match jobtoken.Match
}

// Issue implements jobtoken.Store. The tests never issue a job token.
func (*fakeJobTokens) Issue(context.Context, jobtoken.Token, namespace.Fence, audit.Entry) error {
	return jobtoken.ErrInvalid
}

// GetByDigest implements jobtoken.Store.
func (f *fakeJobTokens) GetByDigest(_ context.Context, d jobtoken.Digest) (jobtoken.Match, error) {
	if !f.match.Token.Digest.Equal(d) {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	return f.match, nil
}

// tokenJobStore is a job.Store that answers one job. The reader of the unit of
// a job reads it on every request of a job token.
type tokenJobStore struct {
	job.Store

	j job.Job
}

// Get implements job.Store.
func (s *tokenJobStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	if id != s.j.ID {
		return job.Job{}, job.ErrNotFound
	}

	return s.j, nil
}

// failingJobStore is a job.Store whose Get fails for a cause that is not a
// missing job.
type failingJobStore struct {
	job.Store

	err error
}

// Get implements job.Store.
func (s *failingJobStore) Get(context.Context, job.ID) (job.Job, error) {
	return job.Job{}, s.err
}

// permissiveAuthorizer allows every check, as a loose Authorizer of an
// embedder does. It keeps the actions that reached it, so a test proves which
// rule decided, and it answers the scope of the units that the test gives it.
type permissiveAuthorizer struct {
	actions []identity.Action
	units   []identity.Object
}

// Authorize implements identity.Authorizer. It allows every check.
func (a *permissiveAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object,
) error {
	a.actions = append(a.actions, action)

	return nil
}

// ListObjects implements identity.Authorizer. It answers the units of the
// test, and it covers no other object.
func (a *permissiveAuthorizer) ListObjects(
	context.Context, identity.Principal, identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: a.units, Principals: nil}, nil
}

// jobTokenWorld is a server configuration that issues job tokens, one valid
// secret of them, and the units of the test.
type jobTokenWorld struct {
	cfg    *settings
	secret jobtoken.Secret
	unit   identity.Object
	other  identity.Object
}

// newJobTokenWorld returns a world whose job token is live for the unit
// pulumi:acme/web/prod, whose starter is the administrator alice.
func newJobTokenWorld(t *testing.T) jobTokenWorld {
	t.Helper()

	now := jobTokenNow()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	unit = unit.WithUnitID("unt-1")

	other, err := identity.UnitObject("pulumi", ns, "web/dev")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	other = other.WithUnitID("unt-2")

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	var j job.Job

	j.ID = "job-1"
	j.Spec.Unit = unit
	j.State = job.StateRunning

	// The starter is an administrator: the job token service clears the mark,
	// and the rule of job tokens clears it again before it asks the next
	// authorizer. The starter record makes both checks visible.
	starter := identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: "", JobID: "",
	}

	var match jobtoken.Match

	match.Token.Digest = jobtoken.DigestOf(secret)
	match.Token.StarterID = "prn-alice"
	match.Token.JobID = "job-1"
	match.Token.Namespace = unit.Namespace()
	match.Token.Unit = unit
	match.Token.ClaimOperation = "op-1"
	match.Token.ClaimToken = 1
	match.Token.ExpiresAt = now.Add(time.Hour)
	match.Token.CreatedAt = now.Add(-time.Minute)

	match.Claim.OperationState = durable.StateRunning
	match.Claim.OperationClaimToken = 1
	match.Claim.LeaseEndsAt = now.Add(time.Minute)
	match.Claim.JobID = "job-1"
	match.Claim.JobState = job.StateRunning
	match.Claim.JobOperation = "op-1"
	match.Claim.JobUnitID = unit.UnitID()
	match.Claim.JobStarterID = "prn-alice"
	match.Claim.Starter = starter
	match.Claim.StarterRecordID = "prn-alice"
	match.Claim.StarterEnabled = true

	cfg := defaultSettings()
	cfg.authenticator = refuseAll{}
	cfg.authorizer = &permissiveAuthorizer{actions: nil, units: []identity.Object{unit}}
	cfg.jobStore = &tokenJobStore{Store: nil, j: j}
	cfg.logger = slog.New(slog.DiscardHandler)
	cfg.now = func() time.Time { return now }

	WithJobTokenStore(&fakeJobTokens{match: match})(cfg)

	return jobTokenWorld{cfg: cfg, secret: secret, unit: unit, other: other}
}

// jobTokenPrincipal returns the principal that a job token of job-1 signs in
// as, for the subject subject.
func jobTokenPrincipal(subject string) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: subject, Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialJobToken, JobID: "job-1",
	}
}

// jobGuess is a value with the form of a job token that no record holds.
func jobGuess() string {
	return jobtoken.SecretPrefix + strings.Repeat("A", 43)
}

// canceledAt returns a context of a request from addr that is already done: a
// gate over its budget then answers at once with identity.ErrRateLimited, and
// a gate within its budget goes through.
func canceledAt(t *testing.T, addr string) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	return authgate.WithRequest(ctx, addr)
}

// TestNewAuthPortsAcceptsAJobToken proves that the server that issues job
// tokens resolves one, and that the rule of job tokens bounds the principal to
// the unit of its job, outside the Authorizer of an embedder that allows every
// check.
func TestNewAuthPortsAcceptsAJobToken(t *testing.T) {
	t.Parallel()

	w := newJobTokenWorld(t)

	ports, err := newAuthPorts(w.cfg, nil)
	if err != nil {
		t.Fatalf("auth ports: %v", err)
	}

	p, err := ports.authenticator.AuthenticateToken(
		authgate.WithRequest(t.Context(), jobTokenAddress), w.secret.Reveal(),
	)
	if err != nil {
		t.Fatalf("job token: %v", err)
	}

	if p.JobTokenID() != "job-1" || p.Credential != identity.CredentialJobToken {
		t.Fatalf("principal = %+v, want the job token of job-1", p)
	}

	if p.Admin || p.TemporaryAdministrator || !p.TemporaryExpiresAt.IsZero() {
		t.Errorf("principal = %+v, want no administrator mark", p)
	}

	checkJobTokenReach(t, ports.authorizer, p, w)
	checkJobTokenList(t, ports.authorizer, p, w)
}

// checkJobTokenReach proves the reach of the principal p through the authorizer
// of the server: the unit of its job, and no other object, with the two reads
// of the outputs of another unit left to the next authorizer.
func checkJobTokenReach(t *testing.T, authz identity.Authorizer, p identity.Principal, w jobTokenWorld) {
	t.Helper()

	allowed := authz.Authorize(t.Context(), p, identity.ActionUnitRead, w.unit)
	if allowed != nil {
		t.Errorf("read of the unit of the job = %v, want allowed", allowed)
	}

	denied := authz.Authorize(t.Context(), p, identity.ActionUnitRead, w.other)
	if !errors.Is(denied, identity.ErrDenied) {
		t.Errorf("read of another unit = %v, want a denial", denied)
	}

	// The two reads of the outputs of another unit reach the next authorizer,
	// which decides them with the grants of the starter.
	for _, action := range []identity.Action{identity.ActionUnitReadOutputs, identity.ActionUnitReadSecrets} {
		err := authz.Authorize(t.Context(), p, action, w.other)
		if err != nil {
			t.Errorf("%s of another unit = %v, want the next authorizer to decide", action, err)
		}
	}
}

// checkJobTokenList proves that a list by the job token principal p names the
// unit of its job only.
func checkJobTokenList(t *testing.T, authz identity.Authorizer, p identity.Principal, w jobTokenWorld) {
	t.Helper()

	scope, err := authz.ListObjects(t.Context(), p, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if len(scope.Units) != 1 || scope.Units[0].UnitID() != w.unit.UnitID() {
		t.Errorf("scope units = %v, want the unit of the job only", scope.Units)
	}
}

// bothKinds is the world of a server that serves the two kinds of token: the
// fixture of the access tokens, the world of the job tokens, the chain of the
// server and the two gates of that chain.
type bothKinds struct {
	f          budgetFixture
	w          jobTokenWorld
	chain      identity.Chain
	gates      signInGates
	accessGate *authgate.Gate
	jobGate    *authgate.Gate
}

// newBothKinds returns that world, and proves the shape of the chain as it
// builds it: the configured authenticator first, then one gate for each kind of
// token, and the two gates are apart.
func newBothKinds(t *testing.T) bothKinds {
	t.Helper()

	f := newBudgetFixture(t)
	w := newJobTokenWorld(t)

	f.cfg.jobStore = w.cfg.jobStore
	f.cfg.jobTokenStore = w.cfg.jobTokenStore

	jobs := jobtoken.NewService(f.cfg.jobTokenStore, w.cfg.now)

	authn, gates, err := gatedAuthenticator(f.cfg.authenticator, f.tokens, jobs, newGateEnv(f.cfg))
	if err != nil {
		t.Fatalf("gated authenticator: %v", err)
	}

	chain, ok := authn.(identity.Chain)
	if !ok || len(chain) != 2 {
		t.Fatalf("chain = %T %v, want an identity.Chain of 2", authn, authn)
	}

	if _, first := chain[0].(refuseAll); !first {
		t.Fatalf("chain[0] = %T, want the configured authenticator", chain[0])
	}

	kinds := tokenKindsOf(t, chain)

	accessGate := gateOf(t, kinds.access, "access")
	jobGate := gateOf(t, kinds.job, "job")

	if accessGate == jobGate {
		t.Fatal("both kinds of token share one gate, want one gate for each kind")
	}

	return bothKinds{f: f, w: w, chain: chain, gates: gates, accessGate: accessGate, jobGate: jobGate}
}

// tokenKindsOf returns the member of the chain that resolves a token, and
// proves that it holds the gates of both kinds.
func tokenKindsOf(t *testing.T, chain identity.Chain) tokenKinds {
	t.Helper()

	kinds, ok := chain[1].(tokenKinds)
	if !ok {
		t.Fatalf("chain[1] = %T, want the gates of both kinds of token", chain[1])
	}

	return kinds
}

// gateOf returns the failure gate behind which authn resolves the kind of token
// that kind names.
func gateOf(t *testing.T, authn identity.Authenticator, kind string) *authgate.Gate {
	t.Helper()

	gate, ok := authn.(*authgate.Gate)
	if !ok {
		t.Fatalf("the gate of the %s tokens = %T, want *authgate.Gate", kind, authn)
	}

	return gate
}

// guessJobTokens sends count values with the form of a job token from the
// address of the budget, and proves that the chain refuses each one.
func (b bothKinds) guessJobTokens(t *testing.T, count int) {
	t.Helper()

	for range count {
		_, err := b.chain.AuthenticateToken(authgate.WithRequest(t.Context(), budgetAddress), jobGuess())
		if !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("job token guess: err = %v, want ErrUnauthenticated", err)
		}
	}
}

// guessAccessTokens sends count values with the form of an access token from
// the address of the valid job token, and proves that the chain refuses each
// one.
func (b bothKinds) guessAccessTokens(t *testing.T, count int) {
	t.Helper()

	for range count {
		_, err := b.chain.AuthenticateToken(authgate.WithRequest(t.Context(), jobTokenAddress), guess())
		if !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatalf("access token guess: err = %v, want ErrUnauthenticated", err)
		}
	}
}

// lookup proves that the value raw of the address addr resolves through the
// chain.
func (b bothKinds) lookup(t *testing.T, addr, raw string) {
	t.Helper()

	if _, err := b.chain.AuthenticateToken(authgate.WithRequest(t.Context(), addr), raw); err != nil {
		t.Errorf("token of %s: err = %v, want nil", addr, err)
	}
}

// limited proves that gate is over its budget for the address addr: it then
// answers at once with identity.ErrRateLimited.
func limited(t *testing.T, gate *authgate.Gate, addr string) {
	t.Helper()

	err := gate.Admit(canceledAt(t, addr))
	if !errors.Is(err, identity.ErrRateLimited) {
		t.Errorf("budget of %s after the guesses = %v, want ErrRateLimited", addr, err)
	}
}

// withinBudget proves that gate is not over its budget for the address addr:
// it then goes through.
func withinBudget(t *testing.T, gate *authgate.Gate, addr string) {
	t.Helper()

	err := gate.Admit(canceledAt(t, addr))
	if err != nil {
		t.Errorf("budget of %s after the guesses = %v, want nil", addr, err)
	}
}

// TestNewAuthPortsChainsBothTokenGates proves that the chain of the server
// holds a gate for the access tokens and a gate of its own for the job tokens,
// that each kind of value goes to the gate of its kind, and that a flood of
// guesses of one kind spends the budget of that kind only. The valid token of
// either kind still resolves.
func TestNewAuthPortsChainsBothTokenGates(t *testing.T) {
	t.Parallel()

	b := newBothKinds(t)

	if b.gates.gate != b.accessGate {
		t.Error("the sign-in gates are not the gate of the access tokens")
	}

	// A flood of job token guesses spends the budget of the job tokens only.
	b.guessJobTokens(t, authgate.DefaultBurst)

	limited(t, b.jobGate, budgetAddress)
	withinBudget(t, b.accessGate, budgetAddress)

	// A valid access token of that address still resolves.
	b.lookup(t, budgetAddress, b.f.valid)

	// A flood of access token guesses from the address of the valid job token
	// spends the budget of the access tokens only.
	b.guessAccessTokens(t, authgate.DefaultBurst)

	limited(t, b.accessGate, jobTokenAddress)
	withinBudget(t, b.jobGate, jobTokenAddress)

	// The valid job token of that address still resolves through the chain.
	b.lookup(t, jobTokenAddress, b.w.secret.Reveal())
}

// TestTokenKindsSendsAJobTokenToTheJobGate proves that a valid job token
// resolves through the chain of the server, and that a value of the other kind
// reaches the gate of the access tokens in that same chain. The chain hands a
// value to one gate only: the gate of the access tokens never keeps a failure
// for a job token, which the chain would then answer with, and the gate of the
// job tokens never sees a value of another kind.
func TestTokenKindsSendsAJobTokenToTheJobGate(t *testing.T) {
	t.Parallel()

	b := newBothKinds(t)

	p, err := b.chain.AuthenticateToken(authgate.WithRequest(t.Context(), jobTokenAddress), b.w.secret.Reveal())
	if err != nil {
		t.Fatalf("job token through the chain: %v, want the principal of its job", err)
	}

	if p.JobTokenID() != "job-1" {
		t.Errorf("principal = %+v, want the job token of job-1", p)
	}

	b.lookup(t, jobTokenAddress, b.f.valid)
}

// denialAuditStore keeps the entries of the refusals that it is given. A set
// err fails the append, as a store that cannot write does.
type denialAuditStore struct {
	entries []audit.Entry
	err     error
}

// Append implements audit.Store.
func (s *denialAuditStore) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if s.err != nil {
		return audit.Entry{}, s.err
	}

	s.entries = append(s.entries, e)

	return e, nil
}

// AppendServer implements audit.ServerStore.
func (s *denialAuditStore) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if s.err != nil {
		return audit.Entry{}, s.err
	}

	s.entries = append(s.entries, e)

	return e, nil
}

// List implements audit.Store.
func (*denialAuditStore) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: nil, Total: 0}, nil
}

// Check implements audit.Store.
func (*denialAuditStore) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// denialNamespaces knows the namespace acme.
type denialNamespaces struct{}

// Get implements refusal.Namespaces.
func (denialNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != "acme" {
		return namespace.Namespace{}, fmt.Errorf("%w: %s", namespace.ErrNotFound, name)
	}

	return namespace.Namespace{
		CreatedAt: time.Time{}, ID: "", Name: name, PrimaryInstallation: "", FailoverVersion: 3,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// denyRequest returns the request of a read in the namespace acme.
func denyRequest(t *testing.T) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tofu/state/acme/web%2Fdev", http.NoBody)
}

// TestJobTokenRuleRefusalWritesOneAuditEntry proves that each refusal of the
// rule of job tokens goes through the denial path that the adapters use: the
// refusal recorder writes one entry for each refusal, with the fixed reason of
// a policy denial, and it writes no entry for an allowed read.
func TestJobTokenRuleRefusalWritesOneAuditEntry(t *testing.T) {
	t.Parallel()

	w := newJobTokenWorld(t)

	ports, err := newAuthPorts(w.cfg, nil)
	if err != nil {
		t.Fatalf("auth ports: %v", err)
	}

	store := &denialAuditStore{entries: nil, err: nil}

	recorder, err := refusal.New(ports.authorizer, store, denialNamespaces{}, w.cfg.logger, nil)
	if err != nil {
		t.Fatalf("refusal recorder: %v", err)
	}

	subjects := []string{"alice", "bob", "carol"}

	for _, subject := range subjects {
		denySubject(t, recorder, w, subject)
	}

	if len(store.entries) != len(subjects) {
		t.Fatalf("refusals = %d, want one entry for each of %d refusals", len(store.entries), len(subjects))
	}

	for i := range store.entries {
		checkRefusalEntry(t, i, store.entries[i], subjects[i])
	}
}

// denySubject proves that the read of the unit of the job of subject is
// allowed, and that its read of another unit is refused.
func denySubject(t *testing.T, recorder *refusal.Recorder, w jobTokenWorld, subject string) {
	t.Helper()

	p := jobTokenPrincipal(subject)

	// The read of the unit of the job is allowed, and writes nothing.
	req := denyRequest(t)

	allowed := recorder.Authorize(req.Context(), req, p, identity.ActionUnitRead, w.unit)
	if allowed != nil {
		t.Fatalf("read of the unit of the job of %s = %v, want allowed", subject, allowed)
	}

	req = denyRequest(t)

	denied := recorder.Authorize(req.Context(), req, p, identity.ActionUnitRead, w.other)
	if !errors.Is(denied, identity.ErrDenied) {
		t.Fatalf("read of another unit by %s = %v, want a denial", subject, denied)
	}
}

// checkRefusalEntry proves the entry of a refusal in the namespace acme.
func checkRefusalEntry(t *testing.T, i int, e audit.Entry, subject string) {
	t.Helper()

	if e.Subject != subject {
		t.Errorf("entry %d subject = %q, want %q", i, e.Subject, subject)
	}

	if e.Action != string(identity.ActionUnitRead) {
		t.Errorf("entry %d action = %q, want %q", i, e.Action, identity.ActionUnitRead)
	}

	if e.Namespace != "acme" {
		t.Errorf("entry %d namespace = %q, want acme", i, e.Namespace)
	}

	if !strings.HasPrefix(e.Reason, refusal.ReasonPolicy) {
		t.Errorf("entry %d reason = %q, want %q and the route", i, e.Reason, refusal.ReasonPolicy)
	}
}

// TestJobTokenRuleFailedReadWritesAnAuthorizationFailure proves that a job
// token whose unit the store cannot read is refused, and that the entry of the
// refusal says that the authorization failed: a failure is never a policy
// denial, and the text of the store error stays out of the entry.
func TestJobTokenRuleFailedReadWritesAnAuthorizationFailure(t *testing.T) {
	t.Parallel()

	w := newJobTokenWorld(t)
	w.cfg.jobStore = &failingJobStore{Store: nil, err: errJobsDown}

	ports, err := newAuthPorts(w.cfg, nil)
	if err != nil {
		t.Fatalf("auth ports: %v", err)
	}

	store := &denialAuditStore{entries: nil, err: nil}

	recorder, err := refusal.New(ports.authorizer, store, denialNamespaces{}, w.cfg.logger, nil)
	if err != nil {
		t.Fatalf("refusal recorder: %v", err)
	}

	req := denyRequest(t)

	err = recorder.Authorize(req.Context(), req, jobTokenPrincipal("alice"), identity.ActionUnitRead, w.unit)
	if err == nil || errors.Is(err, identity.ErrDenied) {
		t.Fatalf("read with a failed unit read = %v, want an error that is not a denial", err)
	}

	if len(store.entries) != 1 {
		t.Fatalf("refusals = %d, want one entry", len(store.entries))
	}

	e := store.entries[0]

	if !strings.HasPrefix(e.Reason, refusal.ReasonFailed) {
		t.Errorf("reason = %q, want %q and the route", e.Reason, refusal.ReasonFailed)
	}

	if strings.Contains(e.Reason, errJobsDown.Error()) {
		t.Errorf("reason = %q, want no text of the store error", e.Reason)
	}
}

// TestNewAuthPortsRefusesJobTokensWithoutAJobStore proves that the server
// fails at the build when it issues job tokens and keeps no job store: the
// rule of job tokens must read the unit of the job of every request, and a
// rule that cannot read one must not fall back to a wider reach.
func TestNewAuthPortsRefusesJobTokensWithoutAJobStore(t *testing.T) {
	t.Parallel()

	w := newJobTokenWorld(t)
	w.cfg.jobStore = nil

	if _, err := newAuthPorts(w.cfg, nil); !errors.Is(err, jobunits.ErrMissingStore) {
		t.Fatalf("auth ports without a job store = %v, want jobunits.ErrMissingStore", err)
	}
}

// TestNewJobTokenGateRefusesNoService proves the defense-in-depth check of the
// gate of the job tokens: a gate over no service fails the build instead of
// taking the failure budget of an address and dropping every job token.
func TestNewJobTokenGateRefusesNoService(t *testing.T) {
	t.Parallel()

	env := gateEnv{failures: nil, logger: slog.New(slog.DiscardHandler), now: jobTokenNow}

	_, err := newJobTokenGate(nil, env)
	if !errors.Is(err, errNoJobTokens) {
		t.Fatalf("job token gate without a service = %v, want errNoJobTokens", err)
	}
}
