package accesstoken_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
)

// storeTrust makes a token of the kind trust for the service principal of
// f, and stores it as the store of the trust rules does.
func storeTrust(t *testing.T, f *fixture) (accesstoken.Issued, error) {
	t.Helper()

	issued, err := f.svc.NewTrustToken(t.Context(), f.clock.Now(), f.service.ID, "trust rule trr-1", "trust-rule:trr-1")
	if err != nil {
		return accesstoken.Issued{}, err
	}

	e := caller()
	e.At, e.Namespace, e.Object = f.clock.Now(), audit.ServerScope, accesstoken.TokenRef(f.service.ID, issued.Token.ID)
	e.Kind, e.Outcome = audit.KindChange, audit.OutcomeSuccess

	if err := f.store.CreateToken(t.Context(), issued.Token, issued.Digest, e); err != nil {
		t.Fatalf("store the trust token: %v", err)
	}

	return issued, nil
}

// lifetimeOf returns the lifetime of a new trust token of f.
func lifetimeOf(t *testing.T, f *fixture) time.Duration {
	t.Helper()

	issued, err := storeTrust(t, f)
	if err != nil {
		t.Fatalf("NewTrustToken = %v", err)
	}

	return issued.Token.ExpiresAt.Sub(issued.Token.CreatedAt)
}

// setPolicy applies c to the policy of f.
func setPolicy(t *testing.T, f *fixture, c accesstoken.PolicyChange) {
	t.Helper()

	if _, err := f.svc.SetPolicy(t.Context(), caller(), c); err != nil {
		t.Fatalf("SetPolicy = %v", err)
	}
}

// TestTrustTokenLivesTheExchangeLifetime checks the lifetime of an exchanged
// token: one hour by default, the setting when an administrator changes it,
// and never more than the maximum lifetime.
func TestTrustTokenLivesTheExchangeLifetime(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	issued, err := storeTrust(t, &f)
	if err != nil {
		t.Fatalf("NewTrustToken = %v", err)
	}

	if issued.Token.Kind != accesstoken.KindTrust || issued.Token.PrincipalID != f.service.ID {
		t.Fatalf("token = %+v", issued.Token)
	}

	if got := lifetimeOf(t, &f); got != time.Hour {
		t.Fatalf("lifetime = %s, want 1h", got)
	}

	ten, short := 10*time.Minute, 5*time.Minute

	setPolicy(t, &f, accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: &ten,
	})

	if got := lifetimeOf(t, &f); got != ten {
		t.Fatalf("lifetime after the change = %s, want 10m", got)
	}

	setPolicy(t, &f, accesstoken.PolicyChange{
		MaxLifetime: &short, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
	})

	if got := lifetimeOf(t, &f); got != short {
		t.Fatalf("lifetime under a shorter maximum = %s, want 5m", got)
	}
}

// TestPolicyRefusesAnExchangeLifetimeOutOfBounds checks the bounds of the
// setting.
func TestPolicyRefusesAnExchangeLifetimeOutOfBounds(t *testing.T) {
	t.Parallel()

	for _, d := range []time.Duration{0, time.Second, 25 * time.Hour, time.Minute + time.Microsecond} {
		p := accesstoken.DefaultPolicy()
		p.ExchangeLifetime = d

		if err := p.Validate(); !errors.Is(err, accesstoken.ErrInvalid) {
			t.Errorf("exchange lifetime %s: Validate = %v, want ErrInvalid", d, err)
		}
	}

	if got := accesstoken.DefaultPolicy().ExchangeLifetime; got != time.Hour {
		t.Errorf("default exchange lifetime = %s, want 1h", got)
	}
}

// TestTrustTokenAuthenticatesAsATrustToken checks the credential of an
// exchanged token: a trust token, never an access token of a person.
func TestTrustTokenAuthenticatesAsATrustToken(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	issued, err := storeTrust(t, &f)
	if err != nil {
		t.Fatalf("NewTrustToken = %v", err)
	}

	p, err := f.svc.AuthenticateToken(t.Context(), issued.Secret.Reveal())
	if err != nil {
		t.Fatalf("AuthenticateToken = %v", err)
	}

	if p.Credential != identity.CredentialTrustToken || p.Subject != f.service.Subject || p.ActsAsPerson() {
		t.Fatalf("principal = %+v, want a trust token of %s", p, f.service.Subject)
	}

	// The token ends after its lifetime.
	f.clock.Advance(time.Hour)

	_, err = f.svc.AuthenticateToken(t.Context(), issued.Secret.Reveal())
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("AuthenticateToken after one hour = %v, want ErrUnauthenticated", err)
	}
}

// TestTrustTokenRevokesAndNeverRotates checks the routes of the service
// tokens on an exchanged token: a holder of principal:manage-tokens revokes
// it, and no rotation renews it.
func TestTrustTokenRevokesAndNeverRotates(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	issued, err := storeTrust(t, &f)
	if err != nil {
		t.Fatalf("NewTrustToken = %v", err)
	}

	_, _, err = f.svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
		ExpiresAt: time.Time{}, OldEndsAt: nil, ID: issued.Token.ID, Scope: accesstoken.ScopeService,
	})
	if !errors.Is(err, accesstoken.ErrWrongKind) {
		t.Fatalf("Rotate = %v, want ErrWrongKind", err)
	}

	err = f.svc.Revoke(t.Context(), admin(), caller(), accesstoken.ScopeService, issued.Token.ID)
	if err != nil {
		t.Fatalf("Revoke = %v", err)
	}

	_, err = f.svc.AuthenticateToken(t.Context(), issued.Secret.Reveal())
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("AuthenticateToken after the revoke = %v, want ErrUnauthenticated", err)
	}
}

// TestTrustTokenAuthenticatorConformance runs the conformance suite of the
// Authenticator port on the Service with a token of a token exchange: the
// Service is the Authenticator that checks the credential of an exchange on
// each request.
func TestTrustTokenAuthenticatorConformance(t *testing.T) {
	t.Parallel()

	identitytest.RunAuthenticator(t, func(t *testing.T) (identity.Authenticator, string) {
		t.Helper()

		f := newFixture(t, nil)

		issued, err := storeTrust(t, &f)
		if err != nil {
			t.Fatalf("NewTrustToken = %v", err)
		}

		return f.svc, issued.Secret.Reveal()
	})
}
