package accesstoken_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// plantServiceRecord stores a principal record with a subject of the
// namespace service principal, straight in the store: no API makes one, but a
// restore or a row of another version could.
func plantServiceRecord(t *testing.T, f *fixture, id, subject string, kind principal.Kind) principal.Principal {
	t.Helper()

	at := f.clock.Now()
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: subject, DisplayName: subject,
		Kind: kind, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	e := caller()
	e.At, e.Namespace, e.Kind, e.Outcome, e.Object = at, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, principal.Ref(id)

	if err := f.store.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("plant principal %q: %v", subject, err)
	}

	return p
}

// plantServiceToken stores a token of the planted record p, and returns the
// secret of the token. The secret comes from a token of another database, so
// that the digest is new in this one.
func plantServiceToken(t *testing.T, f *fixture, p principal.Principal) accesstoken.Secret {
	t.Helper()

	other := newFixture(t, nil)
	_, secret := other.create(t, other.service, "donor")

	at := f.clock.Now()
	tok := accesstoken.Token{
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
		DisabledAt: time.Time{}, ID: "tok-planted", PrincipalID: p.ID, Name: "planted", Kind: accesstoken.KindService,
		ReplacedBy: "", CreatedBy: "restore",
	}

	e := caller()
	e.At, e.Namespace, e.Kind, e.Outcome, e.Object = at, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, accesstoken.TokenRef(p.ID, tok.ID)

	if err := f.store.CreateToken(t.Context(), tok, accesstoken.DigestOf(secret), e); err != nil {
		t.Fatalf("plant token: %v", err)
	}

	return secret
}

var namespaceServiceSubjects = []string{ //nolint:gochecknoglobals // A frozen test table.
	"system:namespace-service:acme",
	"System:Namespace-Service:acme",
	"SYSTEM:NAMESPACE-SERVICE:ACME",
}

// createAttempts returns the ways to ask for a token of the planted record
// rec. The own scope takes a person only: it refuses a service for its kind
// before the subject matters.
func createAttempts(t *testing.T, f *fixture, rec principal.Principal) map[string]func() error {
	t.Helper()

	in := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: rec.ID, Name: "mint"}
	attempts := map[string]func() error{
		"service scope": func() error {
			_, _, err := f.svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService, in)

			return err
		},
	}

	if rec.Kind == principal.KindPerson {
		attempts["own scope"] = func() error {
			own := in
			own.PrincipalID = ""
			_, _, err := f.svc.Create(t.Context(), as(rec), caller(), accesstoken.ScopeOwn, own)

			return err
		}
	}

	return attempts
}

// TestCreateRefusesTheNamespaceServiceRecord proves that the token service
// mints no token for a planted record with the subject of the namespace
// service principal, in each scope and for each kind, also for an
// administrator.
func TestCreateRefusesTheNamespaceServiceRecord(t *testing.T) {
	t.Parallel()

	for _, subject := range namespaceServiceSubjects {
		for _, kind := range []principal.Kind{principal.KindService, principal.KindPerson} {
			f := newFixture(t, nil)
			rec := plantServiceRecord(t, &f, "prn-planted", subject, kind)

			for name, run := range createAttempts(t, &f, rec) {
				if err := run(); !errors.Is(err, accesstoken.ErrNoTokenForKind) ||
					!errors.Is(err, principal.ErrReservedSubject) {
					t.Errorf("%q %s %s: Create = %v, want ErrNoTokenForKind and ErrReservedSubject",
						subject, kind, name, err)
				}
			}

			page, err := f.svc.List(t.Context(), accesstoken.PageRequest{
				PrincipalID: rec.ID, Cursor: "", Filter: accesstoken.FilterAll, Limit: 0,
			})
			if err != nil || page.Total != 0 {
				t.Errorf("%q %s: tokens of the record = %d, %v; want none", subject, kind, page.Total, err)
			}
		}
	}
}

// TestAuthenticateRefusesATokenOfTheNamespaceServiceRecord proves that a
// token row of a planted record authenticates nothing, and that the refusal
// returns no principal.
func TestAuthenticateRefusesATokenOfTheNamespaceServiceRecord(t *testing.T) {
	t.Parallel()

	for _, subject := range namespaceServiceSubjects {
		f := newFixture(t, nil)
		rec := plantServiceRecord(t, &f, "prn-planted", subject, principal.KindService)
		secret := plantServiceToken(t, &f, rec)

		got, err := f.svc.AuthenticateToken(t.Context(), secret.Reveal())
		if !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("%q: AuthenticateToken = %v, want ErrUnauthenticated", subject, err)
		}

		if got.Subject != "" || got.Admin || got.TokenID != "" {
			t.Errorf("%q: AuthenticateToken returned the principal %+v with an error", subject, got)
		}
	}
}

// TestRotateRefusesATokenOfTheNamespaceServiceRecord proves that a planted
// token of the record cannot be rotated into a new one.
func TestRotateRefusesATokenOfTheNamespaceServiceRecord(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	rec := plantServiceRecord(t, &f, "prn-planted", namespaceServiceSubjects[0], principal.KindService)
	plantServiceToken(t, &f, rec)

	_, _, err := f.svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
		OldEndsAt: nil, ExpiresAt: time.Time{}, ID: "tok-planted", Scope: accesstoken.ScopeService,
	})
	if !errors.Is(err, accesstoken.ErrNoTokenForKind) || !errors.Is(err, principal.ErrReservedSubject) {
		t.Fatalf("Rotate = %v, want ErrNoTokenForKind and ErrReservedSubject", err)
	}
}
