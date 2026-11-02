package sessiontest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
)

// tokenPolicyEntry returns a valid audit entry of a change of the access
// token policy at the time when.
func tokenPolicyEntry(when time.Time) audit.Entry {
	e := entry("ses-x", when)
	e.Action = "server:manage-token-policy"
	e.Object = accesstoken.PolicyRef

	return e
}

// setAccounts stores the account disable and its period in the access token
// policy of s, at the time when.
func setAccounts(t *testing.T, s Store, disable bool, inactivity time.Duration, when time.Time) {
	t.Helper()

	_, err := s.UpdatePolicy(t.Context(), func(old accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		old.DisableAccounts, old.Inactivity, old.UpdatedAt = disable, inactivity, when

		return old, tokenPolicyEntry(when), nil
	})
	if err != nil {
		t.Fatalf("set the account disable to %t: %v", disable, err)
	}
}

// accountsOf returns the account part of the access token policy p.
func accountsOf(p accesstoken.Policy) session.AccountPolicy {
	return session.AccountPolicy{Inactivity: p.Inactivity, DisableAccounts: p.DisableAccounts}
}

// testLookupAnswersPolicies proves that the lookup by digest answers the
// session policy and the account part of the access token policy as stored
// at the time of the lookup.
func testLookupAnswersPolicies(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")
	mustSession(t, s, newSession("ses-1", "prn-1", at(1)), digest(1))

	wantSession, err := s.GetSessionPolicy(ctx)
	if err != nil {
		t.Fatalf("get session policy: %v", err)
	}

	tokens, err := s.GetPolicy(ctx)
	if err != nil {
		t.Fatalf("get access token policy: %v", err)
	}

	m, err := s.GetSessionByDigest(ctx, digest(1))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if m.Policy != wantSession || m.Accounts != accountsOf(tokens) {
		t.Fatalf("lookup policies = %+v, %+v; want %+v, %+v", m.Policy, m.Accounts, wantSession, accountsOf(tokens))
	}

	changed := session.Policy{
		UpdatedAt: at(5), Limits: session.Limits{Idle: 30 * time.Minute, MaxLength: 8 * time.Hour},
		TokenFreshness: 0, TokenFreshnessDefault: false,
	}

	_, err = s.UpdateSessionPolicy(ctx, func(session.Policy) (session.Policy, audit.Entry, error) {
		return changed, policyEntry(at(5)), nil
	})
	if err != nil {
		t.Fatalf("update session policy: %v", err)
	}

	setAccounts(t, s, !tokens.DisableAccounts, 2*time.Hour, at(6))

	m, err = s.GetSessionByDigest(ctx, digest(1))
	if err != nil {
		t.Fatalf("lookup after the changes: %v", err)
	}

	wantAccounts := session.AccountPolicy{Inactivity: 2 * time.Hour, DisableAccounts: !tokens.DisableAccounts}
	if m.Policy != changed || m.Accounts != wantAccounts {
		t.Fatalf("lookup policies after the changes = %+v, %+v; want %+v, %+v",
			m.Policy, m.Accounts, changed, wantAccounts)
	}
}

// PairFactory returns two handles on one fresh, empty database for one
// subtest, as two servers open one database. An adapter that allows one
// handle per database passes that handle twice.
type PairFactory func(t *testing.T) (Store, Store)

// RunFresh executes the cases that change a policy through one handle and
// authenticate a session through the other. A change must apply to the next
// session use at once, with no cache in between.
func RunFresh(t *testing.T, newPair PairFactory) {
	t.Helper()

	t.Run("an account disable applies to the next session use", func(t *testing.T) {
		t.Parallel()

		a, b := newPair(t)
		testFreshAccountDisable(t, a, b)
	})
}

// tokenAccountRule applies the account rule of the access token policy, as
// the server does for a console session.
type tokenAccountRule struct{}

func (tokenAccountRule) AccountInactive(o session.Owner, p session.AccountPolicy, now time.Time) bool {
	policy := accesstoken.DefaultPolicy()
	policy.Inactivity, policy.DisableAccounts = p.Inactivity, p.DisableAccounts

	return policy.AccountInactiveAt(accesstoken.Owner{
		ActiveAt: o.ActiveAt, DisabledAt: o.DisabledAt, ID: o.ID, Subject: o.Subject, DisplayName: o.DisplayName,
		Kind: o.Kind, BreakGlass: o.BreakGlass,
	}, now)
}

// testFreshAccountDisable authenticates a session through a with the account
// disable off. The other handle b turns it on with a period of 5 minutes.
// Ten minutes after the last use, the session is still within its idle
// limit, but the next Authenticate through a refuses it at once.
func testFreshAccountDisable(t *testing.T, a, b Store) {
	t.Helper()

	now := at(1)
	clock := func() time.Time { return now }

	sessions, err := session.NewSessions(session.SessionsConfig{
		Store: a, Providers: nil, Inactivity: tokenAccountRule{}, Now: clock,
	})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	setAccounts(t, b, false, 5*time.Minute, at(0))
	mustPerson(t, a, "prn-1")

	_, secret, err := sessions.Start(t.Context(), entry("ses-1", now), session.Start{
		AuthTime: now, PrincipalID: "prn-1", Provider: "corp-idp", SourceIP: "192.0.2.10", UserAgent: "sessiontest",
		Generation: 0, Stored: false,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	now = now.Add(2 * time.Minute)

	if _, err := sessions.Authenticate(t.Context(), secret); err != nil {
		t.Fatalf("authenticate with the account disable off: %v", err)
	}

	now = now.Add(10 * time.Minute)

	setAccounts(t, b, true, 5*time.Minute, now)

	if _, err := sessions.Authenticate(t.Context(), secret); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("authenticate after another handle turned the account disable on: err = %v, "+
			"want ErrUnauthenticated", err)
	}
}
