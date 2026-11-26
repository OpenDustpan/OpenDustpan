package accesstoken_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// oldKind is the kind that an earlier layout stored for a token of a trust
// rule. No code issues it now.
const oldKind = accesstoken.Kind("trust")

// storeTokenOfKind stores a token of the kind k for the service principal of
// f and returns its secret.
func storeTokenOfKind(t *testing.T, f *fixture, k accesstoken.Kind) accesstoken.Secret {
	t.Helper()

	issued, err := f.svc.NewTrustToken(t.Context(), f.clock.Now(), f.service.ID, "old kind", "trust-rule:old")
	if err != nil {
		t.Fatalf("NewTrustToken = %v", err)
	}

	e := caller()
	e.At, e.Namespace, e.Object = f.clock.Now(), audit.ServerScope, accesstoken.TokenRef(f.service.ID, issued.Token.ID)
	e.Kind, e.Outcome = audit.KindChange, audit.OutcomeSuccess

	if err := f.store.CreateToken(t.Context(), issued.Token, issued.Digest, e); err != nil {
		t.Fatalf("store the token: %v", err)
	}

	// No port writes a kind that it does not know, so change the stored row
	// with a second connection, as a row of another version would be.
	conn, openErr := sql.Open("sqlite", f.path)
	if openErr != nil {
		t.Fatalf("open database: %v", openErr)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	}()

	const query = "UPDATE access_tokens SET kind = ? WHERE id = ?"
	if _, execErr := conn.ExecContext(t.Context(), query, string(k), issued.Token.ID); execErr != nil {
		t.Fatalf("set stored token kind: %v", execErr)
	}

	return issued.Secret
}

// serviceOver returns the token service of f that reads its tokens from st.
func serviceOver(t *testing.T, f *fixture, st accesstoken.Store) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      st, Principals: f.store, Audit: f.store, Guard: noStepGuard(t), Authorizer: nil,
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

	return svc
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
		// stored is the kind that the store holds for the token.
		stored accesstoken.Kind
		// wrap returns the store that the service under test reads.
		wrap func(accesstoken.Store) accesstoken.Store
	}{
		{"the store refuses the row", oldKind, func(s accesstoken.Store) accesstoken.Store { return s }},
		// The store holds a valid kind and the stub changes it on the way out,
		// so only the check of the service can stop the token.
		{"the service refuses what the store gives", accesstoken.KindService, func(s accesstoken.Store) accesstoken.Store {
			return stubKindStore{Store: s, kind: oldKind}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			secret := storeTokenOfKind(t, &f, tc.stored)

			svc := serviceOver(t, &f, tc.wrap(f.store))

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

// rotateCountStore returns every token with the kind that it holds, as a store
// that skips the check of the kind, and counts the calls that write a token.
type rotateCountStore struct {
	accesstoken.Store

	kind    accesstoken.Kind
	rotates *atomic.Int32
}

func (s rotateCountStore) GetToken(ctx context.Context, id string) (accesstoken.Token, error) {
	tok, err := s.Store.GetToken(ctx, id)
	tok.Kind = s.kind

	return tok, err
}

func (s rotateCountStore) RotateToken(ctx context.Context, r accesstoken.Replacement, e audit.Entry) error {
	s.rotates.Add(1)

	return s.Store.RotateToken(ctx, r, e)
}

// TestRotateRefusesTokenOfUnknownKind checks that a store that gives back a
// token of a kind that this version does not know does not get a successor of
// that kind: the service fails and does not call the store to write.
func TestRotateRefusesTokenOfUnknownKind(t *testing.T) {
	t.Parallel()

	for _, kind := range []accesstoken.Kind{oldKind, "", "Service", "service ", "service\x00", "../service"} {
		t.Run(strconv.Quote(string(kind)), func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			tok, _ := f.create(t, f.service, "deploy")

			var rotates atomic.Int32

			svc := serviceOver(t, &f, rotateCountStore{Store: f.store, kind: kind, rotates: &rotates})

			_, secret, err := svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
				ID: tok.ID, Scope: accesstoken.ScopeService,
				OldEndsAt: nil, ExpiresAt: time.Time{},
			})
			if err == nil {
				t.Fatalf("Rotate of a token of kind %q succeeded, want an error", kind)
			}

			if secret.Reveal() != "" {
				t.Error("Rotate of a token of an unknown kind returned a secret")
			}

			if n := rotates.Load(); n != 0 {
				t.Errorf("store.RotateToken called %d times, want 0", n)
			}
		})
	}
}

// TestNewTokenRefusesUnknownKind sends a hostile kind straight to the record
// constructor of the service, as if the route check before it did not exist.
func TestNewTokenRefusesUnknownKind(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	for _, kind := range []accesstoken.Kind{oldKind, "", "Service", "service ", "service\x00", "../service", "service\n"} {
		tok, err := accesstoken.IssueTokenForTest(f.svc, kind)
		if !errors.Is(err, accesstoken.ErrUnknownKind) {
			t.Errorf("issue a token of kind %q: err = %v, want ErrUnknownKind", kind, err)
		}

		if tok.ID != "" {
			t.Errorf("issue a token of kind %q returned the token %q, want none", kind, tok.ID)
		}
	}

	for _, kind := range []accesstoken.Kind{accesstoken.KindPersonal, accesstoken.KindService, accesstoken.KindJWTExchange} {
		if _, err := accesstoken.IssueTokenForTest(f.svc, kind); err != nil {
			t.Errorf("issue a token of kind %q: %v", kind, err)
		}
	}
}

// revokeCountStore returns every token with the kind that it holds, as a store
// that skips the check of the kind, and counts the calls that revoke a token.
type revokeCountStore struct {
	accesstoken.Store

	kind    accesstoken.Kind
	revokes *atomic.Int32
}

func (s revokeCountStore) GetToken(ctx context.Context, id string) (accesstoken.Token, error) {
	tok, err := s.Store.GetToken(ctx, id)
	tok.Kind = s.kind

	return tok, err
}

func (s revokeCountStore) RevokeToken(ctx context.Context, id string, e audit.Entry) error {
	s.revokes.Add(1)

	return s.Store.RevokeToken(ctx, id, e)
}

// TestRevokeRefusesTokenOfUnknownKind checks that a store that gives back a
// token of a kind that this version does not know gets no revoke from the
// service: the revoke answers ErrUnknownKind with the ID of the row, and the
// service does not call the store to write.
func TestRevokeRefusesTokenOfUnknownKind(t *testing.T) {
	t.Parallel()

	for _, kind := range []accesstoken.Kind{oldKind, "", "Service", "service ", "service\x00", "../service"} {
		t.Run(strconv.Quote(string(kind)), func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			tok, _ := f.create(t, f.service, "deploy")

			var revokes atomic.Int32

			svc := serviceOver(t, &f, revokeCountStore{Store: f.store, kind: kind, revokes: &revokes})

			err := svc.Revoke(t.Context(), admin(), caller(), accesstoken.ScopeService, tok.ID)
			if !errors.Is(err, accesstoken.ErrUnknownKind) {
				t.Fatalf("Revoke of a token of kind %q: err = %v, want ErrUnknownKind", kind, err)
			}

			if !strings.Contains(err.Error(), tok.ID) {
				t.Errorf("Revoke of a token of kind %q: error %q does not name the row", kind, err)
			}

			if n := revokes.Load(); n != 0 {
				t.Errorf("store.RevokeToken called %d times, want 0", n)
			}
		})
	}
}
