package accesstoken_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// oldKind is the kind that an earlier layout stored for a token of a trust
// rule. No code issues it now.
const oldKind = accesstoken.Kind("trust")

// storeOldKindToken stores a token of the kind oldKind for the service
// principal of f and returns its secret.
func storeOldKindToken(t *testing.T, f *fixture) accesstoken.Secret {
	t.Helper()

	issued, err := f.svc.NewTrustToken(t.Context(), f.clock.Now(), f.service.ID, "old kind", "trust-rule:old")
	if err != nil {
		t.Fatalf("NewTrustToken = %v", err)
	}

	issued.Token.Kind = oldKind

	e := caller()
	e.At, e.Namespace, e.Object = f.clock.Now(), audit.ServerScope, accesstoken.TokenRef(f.service.ID, issued.Token.ID)
	e.Kind, e.Outcome = audit.KindChange, audit.OutcomeSuccess

	if err := f.store.CreateToken(t.Context(), issued.Token, issued.Digest, e); err != nil {
		t.Fatalf("store the old-kind token: %v", err)
	}

	return issued.Secret
}

// stubKindStore answers every lookup by digest with a token of the stored
// kind and no error, as a store adapter that skips the check of the kind.
type stubKindStore struct {
	accesstoken.Store

	kind accesstoken.Kind
}

func (s stubKindStore) GetTokenByDigest(ctx context.Context, d accesstoken.Digest) (accesstoken.Match, error) {
	m, err := s.Store.GetTokenByDigest(ctx, d)
	m.Token.Kind = s.kind

	return m, err
}

// TestStoredTokenOfUnknownKindNeverAuthenticates checks that a token with a
// stored kind that this version does not know, the old kind "trust", fails
// closed: the store refuses the row, and the service refuses it again when a
// store gives it back.
func TestStoredTokenOfUnknownKindNeverAuthenticates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// wrap returns the store that the service under test reads.
		wrap func(accesstoken.Store) accesstoken.Store
	}{
		{"the store refuses the row", func(s accesstoken.Store) accesstoken.Store { return s }},
		{"the service refuses what the store gives", func(s accesstoken.Store) accesstoken.Store {
			return stubKindStore{Store: s, kind: oldKind}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			secret := storeOldKindToken(t, &f)

			svc, err := accesstoken.NewService(accesstoken.Config{
				TempAdmins: nil,
				Store:      tc.wrap(f.store), Principals: f.store, Freshness: nil, Authorizer: nil,
				Reach: access.ReachConfig{
					Grants:      f.store,
					Roles:       f.store,
					Callers:     f.store,
					Principals:  f.store,
					Collections: nil,
					Units:       nil,
				},
				Logger: nil, Now: f.clock.Now, NewID: nil,
			})
			if err != nil {
				t.Fatalf("service: %v", err)
			}

			p, err := svc.AuthenticateToken(t.Context(), secret.Reveal())
			if !errors.Is(err, identity.ErrUnauthenticated) || !errors.Is(err, accesstoken.ErrUnknownKind) {
				t.Fatalf(
					"AuthenticateToken of an old-kind token: err = %v, want ErrUnauthenticated and ErrUnknownKind",
					err,
				)
			}

			if p.Credential != "" || p.Subject != "" {
				t.Errorf("AuthenticateToken of an old-kind token returned %+v, want no principal", p)
			}
		})
	}
}

// TestKindValidIsAnAllowlist checks the kinds that a token can have.
func TestKindValidIsAnAllowlist(t *testing.T) {
	t.Parallel()

	for k, want := range map[accesstoken.Kind]bool{
		accesstoken.KindPersonal:    true,
		accesstoken.KindService:     true,
		accesstoken.KindJWTExchange: true,
		"trust":                     false,
		"":                          false,
		"Personal":                  false,
		"admin":                     false,
	} {
		if got := k.Valid(); got != want {
			t.Errorf("Kind(%q).Valid() = %v, want %v", string(k), got, want)
		}
	}
}

// TestRevokeOfJWTExchangeNeedsManageTokens checks that a caller that is not
// an administrator revokes a token of a token exchange only with
// principal:manage-tokens on its principal. Without the grant the revoke
// answers ErrDenied.
func TestRevokeOfJWTExchangeNeedsManageTokens(t *testing.T) {
	t.Parallel()

	manage := string(identity.ActionPrincipalManageTokens) + " principal:"

	for _, tc := range []struct {
		name  string
		grant bool
		want  error
	}{
		{"without the grant", false, identity.ErrDenied},
		{"with the grant", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			issued, err := storeTrust(t, &f)
			if err != nil {
				t.Fatalf("NewTrustToken = %v", err)
			}

			allowed := map[string]bool{}
			if tc.grant {
				allowed[manage+f.service.ID] = true
			}

			svc := newCheckedService(t, &f, &recordAuthorizer{allowed: allowed, checked: nil, mu: sync.Mutex{}})

			err = svc.Revoke(t.Context(), as(f.person), caller(), accesstoken.ScopeService, issued.Token.ID)
			if !errors.Is(err, tc.want) {
				t.Errorf("revoke of a jwt-exchange token %s: err = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}
