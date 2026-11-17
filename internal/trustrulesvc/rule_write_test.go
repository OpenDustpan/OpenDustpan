package trustrulesvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// updateRefusalFixture returns a service over a store that serves stored,
// with the principals of the update refusal cases and the session policy
// sessions. A nil sessions means the default freshness window.
func updateRefusalFixture(
	t *testing.T, stored trustrule.Rule, sessions trustrulesvc.SessionPolicy,
) fixture {
	t.Helper()

	underlying := &fakeStore{created: nil, entries: nil, deleted: nil}
	store := &seededStore{fakeStore: underlying, stored: stored}
	authz := &grants{allow: []identity.Action{identity.ActionPrincipalManageTokens}, checked: nil, err: nil}
	reach := &fakeReach{checked: nil, err: nil}

	disabled := principalOf("prn-disabled", principal.KindService, "acme")
	disabled.DisabledAt = time.Unix(1, 0)

	people := fakePrincipals{
		"prn-worker":   principalOf("prn-worker", principal.KindService, "acme"),
		"prn-disabled": disabled,
		"prn-person":   principalOf("prn-person", principal.KindPerson, ""),
		"prn-server":   principalOf("prn-server", principal.KindService, ""),
		"prn-stranger": principalOf("prn-stranger", principal.KindService, "other"),
	}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: store, Namespaces: fakeNamespaces{}, Principals: people, Authorizer: authz, Reach: reach,
		InstallationURLs: []string{"https://dustpan.example.com", "https://issuer.example.com/base/"}, OwnIssuer: nil,
		CheckURL: nil, Bundles: nil, Sessions: sessions,
		Now: exchangeNow,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return fixture{svc: svc, store: underlying, authz: authz, reach: reach}
}

// TestUpdateNeedsAConsoleSession sends an update with a sign-in but no
// console session, as if every other check had failed: the session guard
// refuses it with ErrFreshRequired before anything reaches the store.
func TestUpdateNeedsAConsoleSession(t *testing.T) {
	t.Parallel()

	stored := seededRule()
	f := updateRefusalFixture(t, stored, nil)

	ctx := sessionauth.WithSignIn(
		context.Background(), sessionauth.SignIn{At: exchangeNow(), Provider: "password"},
	)

	in := renameInput(stored.ID, "deploy-2", 1)

	_, err := f.svc.Update(ctx, person(), emptyEntry(), "acme", in)
	if !errors.Is(err, trustrulesvc.ErrFreshRequired) {
		t.Fatalf("Update = %v, want ErrFreshRequired", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused update reached the store")
	}
}

// errSessionPolicy answers the session policy with err.
type errSessionPolicy struct {
	err error
}

func (p errSessionPolicy) Policy(context.Context) (session.Policy, error) {
	return session.Policy{}, p.err
}

// errPolicyUnavailable is the session store error of the fail-closed case.
var errPolicyUnavailable = errors.New("the session policy is not available")

// TestUpdateFailsClosedOnSessionPolicyError sends an update when the session
// policy cannot be read: the freshness check fails closed with the policy
// error, never nil, and nothing reaches the store.
func TestUpdateFailsClosedOnSessionPolicyError(t *testing.T) {
	t.Parallel()

	stored := seededRule()
	f := updateRefusalFixture(t, stored, errSessionPolicy{err: errPolicyUnavailable})

	in := renameInput(stored.ID, "deploy-2", 1)

	_, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, errPolicyUnavailable) {
		t.Fatalf("Update = %v, want the session policy error", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused update reached the store")
	}
}
