package accesstoken_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/internal/store/sqlite"
)

// storeMtlsTrust makes a token of the kind mtls-exchange for the service
// principal of f, and stores it as the store of the trust rules does.
func storeMtlsTrust(t *testing.T, f *fixture, leafNotAfter time.Time) (accesstoken.Issued, error) {
	t.Helper()

	issued, issueErr := f.svc.NewMtlsTrustToken(t.Context(), accesstoken.MtlsInput{
		At: f.clock.Now(), LeafNotAfter: leafNotAfter, PrincipalID: f.service.ID, Name: "trust rule trr-1",
		CreatedBy: "trust-rule:trr-1",
	})
	if issueErr != nil {
		return accesstoken.Issued{}, issueErr
	}

	e := caller()
	e.At, e.Namespace, e.Object = f.clock.Now(), audit.ServerScope, accesstoken.TokenRef(f.service.ID, issued.Token.ID)
	e.Kind, e.Outcome = audit.KindChange, audit.OutcomeSuccess

	if storeErr := f.store.CreateToken(t.Context(), issued.Token, issued.Digest, e); storeErr != nil {
		t.Fatalf("store the mTLS trust token: %v", storeErr)
	}

	return issued, nil
}

func checkMtlsKindListCredential(t *testing.T) {
	t.Helper()

	f := newFixture(t, nil)
	at := f.clock.Now()
	leafNotAfter := at.Add(2 * time.Hour)

	issued, issueErr := storeMtlsTrust(t, &f, leafNotAfter)
	if issueErr != nil {
		t.Fatalf("NewMtlsTrustToken = %v", issueErr)
	}

	if issued.Token.Kind != accesstoken.KindMTLSExchange {
		t.Fatalf("kind = %q, want %q", issued.Token.Kind, accesstoken.KindMTLSExchange)
	}

	if !issued.Token.Kind.Valid() || !issued.Token.Kind.FromExchange() {
		t.Fatalf("kind %q is not a valid exchange kind", issued.Token.Kind)
	}

	page, listErr := f.svc.List(t.Context(), accesstoken.PageRequest{
		PrincipalID: f.service.ID, Cursor: "", Filter: accesstoken.FilterAll, Limit: 0,
	})
	if listErr != nil || len(page.Items) != 1 || page.Items[0].Kind != accesstoken.KindMTLSExchange {
		t.Fatalf("List = %+v, %v; want the one token of kind mtls-exchange", page, listErr)
	}

	got, authErr := f.svc.AuthenticateToken(t.Context(), issued.Secret.Reveal())
	if authErr != nil {
		t.Fatalf("AuthenticateToken = %v", authErr)
	}

	if got.Credential != identity.CredentialTrustToken || got.Subject != f.service.Subject {
		t.Fatalf("principal = %+v, want a trust token of %s", got, f.service.Subject)
	}
}

func checkMtlsExpiryCapped(t *testing.T) {
	t.Helper()

	f := newFixture(t, nil)
	at := f.clock.Now()
	soon := at.Add(10 * time.Minute)

	issued, issueErr := storeMtlsTrust(t, &f, soon)
	if issueErr != nil {
		t.Fatalf("NewMtlsTrustToken = %v", issueErr)
	}

	if !issued.Token.ExpiresAt.Equal(soon) {
		t.Fatalf("expiry = %s, want the leaf not-after %s", issued.Token.ExpiresAt, soon)
	}

	far := at.Add(2 * time.Hour)

	second, secondErr := f.svc.NewMtlsTrustToken(t.Context(), accesstoken.MtlsInput{
		At: at, LeafNotAfter: far, PrincipalID: f.service.ID, Name: "trust rule trr-2",
		CreatedBy: "trust-rule:trr-2",
	})
	if secondErr != nil {
		t.Fatalf("NewMtlsTrustToken = %v", secondErr)
	}

	if want := at.Add(time.Hour); !second.Token.ExpiresAt.Equal(want) {
		t.Fatalf("expiry = %s, want the exchange lifetime %s", second.Token.ExpiresAt, want)
	}
}

func checkMtlsLeafRefused(t *testing.T) {
	t.Helper()

	f := newFixture(t, nil)
	at := f.clock.Now()

	// Each leaf meets its own check: the text of the error names the check,
	// so a test fails when one check goes and the other still refuses.
	cases := []struct {
		leaf time.Time
		text string
	}{
		{time.Time{}, "is missing"},
		{at, "is not after the issue time"},
		{at.Add(-time.Minute), "is not after the issue time"},
	}

	for _, tc := range cases {
		_, issueErr := f.svc.NewMtlsTrustToken(t.Context(), accesstoken.MtlsInput{
			At: at, LeafNotAfter: tc.leaf, PrincipalID: f.service.ID, Name: "trust rule trr-1",
			CreatedBy: "trust-rule:trr-1",
		})
		if !errors.Is(issueErr, accesstoken.ErrInvalid) || !strings.Contains(issueErr.Error(), tc.text) {
			t.Errorf("leaf %s: NewMtlsTrustToken = %v, want ErrInvalid with %q", tc.leaf, issueErr, tc.text)
		}
	}
}

// checkMtlsNameRefused sends names that ValidateName refuses.
func checkMtlsNameRefused(t *testing.T) {
	t.Helper()

	f := newFixture(t, nil)
	at := f.clock.Now()

	for _, name := range []string{"", "trust rule\ntrr-1", strings.Repeat("n", 1000)} {
		_, issueErr := f.svc.NewMtlsTrustToken(t.Context(), accesstoken.MtlsInput{
			At: at, LeafNotAfter: at.Add(time.Hour), PrincipalID: f.service.ID, Name: name,
			CreatedBy: "trust-rule:trr-1",
		})
		if !errors.Is(issueErr, accesstoken.ErrInvalid) {
			t.Errorf("name %q: NewMtlsTrustToken = %v, want ErrInvalid", name, issueErr)
		}
	}
}

// badPolicyStore is a store whose policy has an exchange lifetime of zero,
// as a row of another version or a restored backup could hold.
type badPolicyStore struct {
	*sqlite.Store
}

func (badPolicyStore) GetPolicy(context.Context) (accesstoken.Policy, error) {
	p := accesstoken.DefaultPolicy()
	p.ExchangeLifetime = 0

	return p, nil
}

// checkMtlsBadPolicy reads a policy with no exchange lifetime: the issue
// refuses with ErrInvalid and makes no token.
func checkMtlsBadPolicy(t *testing.T) {
	t.Helper()

	f := newFixture(t, nil)
	at := f.clock.Now()

	svc, svcErr := accesstoken.NewService(accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil, Store: badPolicyStore{Store: f.store}, Principals: f.store, Freshness: nil,
		Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	})
	if svcErr != nil {
		t.Fatalf("token service: %v", svcErr)
	}

	issued, issueErr := svc.NewMtlsTrustToken(t.Context(), accesstoken.MtlsInput{
		At: at, LeafNotAfter: at.Add(time.Hour), PrincipalID: f.service.ID, Name: "trust rule trr-1",
		CreatedBy: "trust-rule:trr-1",
	})
	if !errors.Is(issueErr, accesstoken.ErrInvalid) || issued.Token.ID != "" ||
		!strings.Contains(issueErr.Error(), "is not positive") {
		t.Fatalf(
			"NewMtlsTrustToken with no exchange lifetime = %v, token %q; want ErrInvalid (not positive) and no token",
			issueErr,
			issued.Token.ID,
		)
	}
}

func checkMtlsRevoke(t *testing.T) {
	t.Helper()

	f := newFixture(t, nil)
	at := f.clock.Now()

	issued, issueErr := storeMtlsTrust(t, &f, at.Add(2*time.Hour))
	if issueErr != nil {
		t.Fatalf("NewMtlsTrustToken = %v", issueErr)
	}

	_, _, rotateErr := f.svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
		ExpiresAt: time.Time{}, OldEndsAt: nil, ID: issued.Token.ID, Scope: accesstoken.ScopeService,
	})
	if !errors.Is(rotateErr, accesstoken.ErrWrongKind) {
		t.Fatalf("Rotate = %v, want ErrWrongKind", rotateErr)
	}

	revokeErr := f.svc.Revoke(t.Context(), admin(), caller(), accesstoken.ScopeService, issued.Token.ID)
	if revokeErr != nil {
		t.Fatalf("Revoke = %v", revokeErr)
	}

	_, authErr := f.svc.AuthenticateToken(t.Context(), issued.Secret.Reveal())
	if !errors.Is(authErr, identity.ErrUnauthenticated) {
		t.Fatalf("AuthenticateToken after the revoke = %v, want ErrUnauthenticated", authErr)
	}
}

// TestMtlsTrustTokenIssue checks the token of an mTLS exchange: the token has
// the kind mtls-exchange, the token list and the issue audit entry show the
// kind, and a request with the token has the credential trust-token. The
// expiry is capped at the leaf not-after time.
func TestMtlsTrustTokenIssue(t *testing.T) {
	t.Parallel()

	checkMtlsKindListCredential(t)
	checkMtlsExpiryCapped(t)
	checkMtlsLeafRefused(t)
	checkMtlsNameRefused(t)
	checkMtlsBadPolicy(t)
	checkMtlsRevoke(t)
}
