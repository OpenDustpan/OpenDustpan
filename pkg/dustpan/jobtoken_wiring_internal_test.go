package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errJobTokenIssueUnused reports the issue method of the test store, which
// the server never calls.
var errJobTokenIssueUnused = errors.New("job token issue is not used in this test")

// jobTokenStore is a jobtoken.Store with one fixed record.
type jobTokenStore struct {
	match jobtoken.Match
	reads int
}

// Issue implements jobtoken.Store.
func (*jobTokenStore) Issue(
	context.Context, jobtoken.Token, namespace.Fence, audit.Entry,
) error {
	return errJobTokenIssueUnused
}

// GetByDigest implements jobtoken.Store.
func (s *jobTokenStore) GetByDigest(_ context.Context, d jobtoken.Digest) (jobtoken.Match, error) {
	s.reads++

	if !s.match.Token.Digest.Equal(d) {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	return s.match, nil
}

// jobTokenMatch returns the live record of secret at now.
func jobTokenMatch(t *testing.T, secret jobtoken.Secret, now time.Time) jobtoken.Match {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	unit = unit.WithUnitID("unt-1")

	starter := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialAccessToken, JobID: "", JobUnit: identity.Object{},
	}

	var m jobtoken.Match

	m.Token.Digest = jobtoken.DigestOf(secret)
	m.Token.StarterID = "prn-alice"
	m.Token.JobID = "job-1"
	m.Token.Namespace = unit.Namespace()
	m.Token.Unit = unit
	m.Token.ClaimOperation = "op-1"
	m.Token.ClaimToken = 1
	m.Token.ExpiresAt = now.Add(time.Hour)
	m.Token.CreatedAt = now.Add(-time.Minute)

	m.Claim.OperationState = durable.StateRunning
	m.Claim.LeaseEndsAt = now.Add(time.Minute)
	m.Claim.JobID = "job-1"
	m.Claim.JobState = job.StateRunning
	m.Claim.JobOperation = "op-1"
	m.Claim.JobUnitID = unit.UnitID()
	m.Claim.JobStarterID = "prn-alice"
	m.Claim.Starter = starter
	m.Claim.StarterRecordID = "prn-alice"
	m.Claim.OperationClaimToken = 1
	m.Claim.StarterEnabled = true

	return m
}

// TestNewAuthPortsAcceptsAJobToken proves that the authenticator chain that
// newAuthPorts builds reads a job token from the store and answers the
// principal of the job.
func TestNewAuthPortsAcceptsAJobToken(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000).UTC()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	store := &jobTokenStore{match: jobTokenMatch(t, secret, now), reads: 0}

	cfg := defaultSettings()
	cfg.authenticator = refuseAll{}
	cfg.jobTokenStore = store
	cfg.now = func() time.Time { return now }

	ports, err := newAuthPorts(cfg, nil)
	if err != nil {
		t.Fatalf("auth ports: %v", err)
	}

	if ports.gates.gate != nil {
		t.Fatal("the access token gate is set with no access token service")
	}

	p, err := ports.authenticator.AuthenticateToken(t.Context(), secret.Reveal())
	if err != nil {
		t.Fatalf("AuthenticateToken = %v", err)
	}

	if p.Credential != identity.CredentialJobToken || p.JobID != "job-1" || p.Subject != "alice" {
		t.Fatalf("principal = %+v, want the job token of job-1 for alice", p)
	}

	if p.JobUnit != store.match.Token.Unit || !p.JobUnit.IsUnit() {
		t.Fatalf("job unit = %+v, want the unit of the record %+v", p.JobUnit, store.match.Token.Unit)
	}

	if store.reads == 0 {
		t.Fatal("the authenticator did not read the job token store")
	}
}

// TestNewAuthPortsChainsBothTokenGates proves that the authenticator chain
// keeps the access token gate and the job token gate when both are set.
func TestNewAuthPortsChainsBothTokenGates(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000).UTC()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	store := &jobTokenStore{match: jobTokenMatch(t, secret, now), reads: 0}

	chain, _, err := gatedAuthenticator(gatedAuthenticatorConfig{
		next: refuseAll{}, tokens: new(accesstoken.Service), jobTokens: store, store: nil,
		logger: slog.New(slog.DiscardHandler), now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("gated authenticator: %v", err)
	}

	links, ok := chain.(identity.Chain)
	if !ok || len(links) != 3 {
		t.Fatalf("chain = %T with %d links, want an identity.Chain of 3", chain, len(links))
	}
}

// allowAllAuthorizer allows every check and records the principal that it
// saw.
type allowAllAuthorizer struct {
	asked int
	got   identity.Principal
}

// Authorize implements identity.Authorizer.
func (a *allowAllAuthorizer) Authorize(
	_ context.Context, p identity.Principal, _ identity.Action, _ identity.Object,
) error {
	a.asked++
	a.got = p

	return nil
}

// ListObjects implements identity.Authorizer.
func (a *allowAllAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: true}, nil
}

// jobTokenAuthFixture returns the job token principal, the authorizer ports
// of newAuthPorts and the configured authorizer.
func jobTokenAuthFixture(
	t *testing.T,
) (identity.Principal, authPorts, *allowAllAuthorizer) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	p := identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: true, TemporaryExpiresAt: time.Unix(1_700_000_000, 0).UTC(),
		Kind: identity.KindPerson, Credential: identity.CredentialJobToken, JobID: "job-1",
		JobUnit: unit.WithUnitID("unt-1"),
	}

	var none identity.Principal

	next := &allowAllAuthorizer{asked: 0, got: none}

	cfg := defaultSettings()
	cfg.authorizer = next

	ports, err := newAuthPorts(cfg, nil)
	if err != nil {
		t.Fatalf("auth ports: %v", err)
	}

	return p, ports, next
}

// jobTokenOtherUnit returns a unit other than the unit of p.
func jobTokenOtherUnit(t *testing.T, p identity.Principal) identity.Object {
	t.Helper()

	other, err := identity.UnitObject("pulumi", p.JobUnit.Namespace(), "web/other")
	if err != nil {
		t.Fatalf("other unit: %v", err)
	}

	return other
}

// TestNewAuthPortsLimitsAJobTokenToItsUnit proves that the authorizer chain
// that newAuthPorts builds denies a job token every other unit, even when the
// configured authorizer allows everything, and that the job token never
// reaches the configured authorizer with a flag.
func TestNewAuthPortsLimitsAJobTokenToItsUnit(t *testing.T) {
	t.Parallel()

	p, ports, next := jobTokenAuthFixture(t)
	other := jobTokenOtherUnit(t, p)

	if err := ports.authorizer.Authorize(t.Context(), p, identity.ActionUnitRead, other); !errors.Is(
		err, identity.ErrDenied,
	) {
		t.Fatalf("action on another unit = %v, want ErrDenied", err)
	}

	if next.asked != 0 {
		t.Fatalf("the configured authorizer was asked %d times for another unit, want 0", next.asked)
	}

	if err := ports.authorizer.Authorize(t.Context(), p, identity.ActionUnitRead, p.JobUnit); err != nil {
		t.Fatalf("action on its unit = %v, want allowed", err)
	}

	if next.asked != 1 {
		t.Fatalf("the configured authorizer was asked %d times, want 1", next.asked)
	}

	if next.got.Admin || next.got.TemporaryAdministrator || !next.got.TemporaryExpiresAt.IsZero() {
		t.Fatalf("the configured authorizer saw the flags %+v, want them cleared", next.got)
	}

	if err := ports.authorizer.Authorize(t.Context(), p, identity.ActionJobWriteCost, p.JobUnit); err != nil {
		t.Fatalf("job:write-cost on its unit = %v, want allowed", err)
	}

	if next.asked != 1 {
		t.Fatalf("the configured authorizer was asked %d times for job:write-cost, want 1", next.asked)
	}
}
