package accesstoken_test

import (
	"errors"
	"regexp"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// kindCase makes one stored token of a kind for a fixture. credential is the
// credential that the authenticator gives the token, and want is the error of
// a service-scope revoke by an administrator.
type kindCase struct {
	name       string
	kind       accesstoken.Kind
	mint       func(t *testing.T, f *fixture) (accesstoken.Token, accesstoken.Secret)
	credential identity.Credential
	want       error
}

// kindCases returns one row for each kind of access token.
func kindCases() []kindCase {
	return []kindCase{
		{
			name: "personal",
			kind: accesstoken.KindPersonal,
			mint: func(t *testing.T, f *fixture) (accesstoken.Token, accesstoken.Secret) {
				t.Helper()

				return f.create(t, f.person, "laptop")
			},
			credential: identity.CredentialAccessToken,
			want:       accesstoken.ErrWrongKind,
		},
		{
			name: "service",
			kind: accesstoken.KindService,
			mint: func(t *testing.T, f *fixture) (accesstoken.Token, accesstoken.Secret) {
				t.Helper()

				return f.create(t, f.service, "deploy")
			},
			credential: identity.CredentialAccessToken,
			want:       nil,
		},
		{
			name: "jwt-exchange",
			kind: accesstoken.KindJWTExchange,
			mint: func(t *testing.T, f *fixture) (accesstoken.Token, accesstoken.Secret) {
				t.Helper()

				issued, err := storeTrust(t, f)
				if err != nil {
					t.Fatalf("NewTrustToken = %v", err)
				}

				return issued.Token, issued.Secret
			},
			credential: identity.CredentialTrustToken,
			want:       nil,
		},
	}
}

// TestCredentialOfEachKind checks the credential of an authenticated token of
// each kind: a token that a token exchange issues gives the trust token, and
// every other kind gives the access token.
func TestCredentialOfEachKind(t *testing.T) {
	t.Parallel()

	for _, c := range kindCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			tok, secret := c.mint(t, &f)
			if tok.Kind != c.kind {
				t.Fatalf("token kind = %q, want %q", tok.Kind, c.kind)
			}

			p, err := f.svc.AuthenticateToken(t.Context(), secret.Reveal())
			if err != nil {
				t.Fatalf("AuthenticateToken = %v", err)
			}

			if p.Credential != c.credential {
				t.Errorf("credential of a %s token = %q, want %q", c.kind, p.Credential, c.credential)
			}
		})
	}
}

// TestCredentialOfUnknownKind checks that the credential of a token of a kind
// that this version does not know is an error, not the access token: a store
// that gives such a token back does not sign anyone in.
func TestCredentialOfUnknownKind(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	secret := storeTokenOfKind(t, &f, accesstoken.KindService)
	svc := serviceOver(t, &f, stubKindStore{Store: f.store, kind: "other"})

	p, err := svc.AuthenticateToken(t.Context(), secret.Reveal())
	if !errors.Is(err, accesstoken.ErrUnknownKind) {
		t.Fatalf("AuthenticateToken of an unknown-kind token: err = %v, want ErrUnknownKind", err)
	}

	if p.Credential != "" {
		t.Errorf("credential = %q, want none", p.Credential)
	}
}

// TestRevokeScopeOfEachKind checks the service-scope revoke of each kind by an
// administrator: a service token and a token of a token exchange are revoked,
// and a personal token is refused with ErrWrongKind.
func TestRevokeScopeOfEachKind(t *testing.T) {
	t.Parallel()

	for _, c := range kindCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			tok, _ := c.mint(t, &f)

			err := f.svc.Revoke(t.Context(), admin(), caller(), accesstoken.ScopeService, tok.ID)
			if !errors.Is(err, c.want) {
				t.Errorf("revoke a %s token in the service scope: err = %v, want %v", c.kind, err, c.want)
			}
		})
	}
}

// TestKindForWorkloadErrorHasNoInternalReference checks that the error text
// for a workload gives the reason and not an internal reference.
func TestKindForWorkloadErrorHasNoInternalReference(t *testing.T) {
	t.Parallel()

	_, err := accesstoken.KindFor(principal.KindWorkload)
	if !errors.Is(err, accesstoken.ErrNoTokenForKind) {
		t.Fatalf("KindFor(workload) error = %v, want ErrNoTokenForKind", err)
	}

	if regexp.MustCompile(`\b(ADR|RFC) [0-9]+`).MatchString(err.Error()) {
		t.Fatalf("KindFor(workload) error = %q, want no internal reference", err)
	}
}
