package principal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// devSubjects returns the hostile subjects that every layer below refuses.
func devSubjects() []string {
	return []string{"dev:admin", "dev:other", "DEV:admin", "Dev:x", "dev:"}
}

// Each of the following tests sends the hostile subject straight to one
// layer, as if the other layers had failed (the layers refuse in turn).

func TestValidateSubjectRefusesDevPrefix(t *testing.T) {
	t.Parallel()

	for _, subject := range devSubjects() {
		if err := principal.ValidateSubject(subject); !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("ValidateSubject(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

func TestPrincipalValidateRefusesDevPrefix(t *testing.T) {
	t.Parallel()

	for _, subject := range devSubjects() {
		if err := stored("prn-x", subject).Validate(); !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("Validate(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

func TestCreatePrincipalRefusesDevPrefix(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for _, subject := range devSubjects() {
		_, err := f.svc.CreatePrincipal(t.Context(), testEntry(), principal.Input{
			Subject: subject, DisplayName: "", Kind: principal.KindPerson,
		})
		if !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("CreatePrincipal(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

func TestSignInPrincipalRefusesDevPrefix(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for _, subject := range devSubjects() {
		_, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(subject, true))
		if !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("SignInPrincipal(%q) = %v, want ErrReservedSubject", subject, err)
		}
	}
}

// TestSignInPrincipalRefusesTheDevPersonThatExists proves that a provider that
// composes "dev:admin" does not reach the person that exists: the refusal
// comes before the read of the store.
func TestSignInPrincipalRefusesTheDevPersonThatExists(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)
	f.put(t, stored("prn-dev", principal.DevAdminSubject))

	_, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(principal.DevAdminSubject, false))
	if !errors.Is(err, principal.ErrReservedSubject) {
		t.Fatalf("SignInPrincipal of the dev person = %v, want ErrReservedSubject", err)
	}
}

func TestUpdatePrincipalRefusesTheDevPerson(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)
	f.put(t, stored("prn-dev", principal.DevAdminSubject))

	name := "other"

	_, err := f.svc.UpdatePrincipal(t.Context(), testEntry(), "prn-dev", principal.Change{DisplayName: &name})
	if !errors.Is(err, principal.ErrReservedSubject) {
		t.Fatalf("UpdatePrincipal of the dev person = %v, want ErrReservedSubject", err)
	}
}

// TestSignInDevAdministrator proves that the one internal path makes the
// person dev:admin, only that subject, only once, and that a later call
// returns the same person.
func TestSignInDevAdministrator(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	first, created, err := f.svc.SignInDevAdministrator(t.Context(), testEntry(), "Dev administrator")
	if err != nil || !created {
		t.Fatalf("SignInDevAdministrator = created %v, %v; want a new person", created, err)
	}

	checkDevPerson(t, first)

	second, created, err := f.svc.SignInDevAdministrator(t.Context(), testEntry(), "Other name")
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second call = %s, created %v, %v; want the same person", second.ID, created, err)
	}

	req := principal.PageRequest{Prefix: principal.DevSubjectPrefix, Cursor: "", Limit: 10}

	page, err := f.svc.ListPrincipals(t.Context(), req, "")
	if err != nil || page.Total != 1 {
		t.Fatalf("people with the dev prefix = %d, %v; want 1", page.Total, err)
	}
}

// checkDevPerson proves that p is a local person dev:admin with no namespace
// and no break-glass mark.
func checkDevPerson(t *testing.T, p principal.Principal) {
	t.Helper()

	if p.Subject != principal.DevAdminSubject || p.Kind != principal.KindPerson ||
		p.Source != principal.SourceLocal || p.DisplayName != "Dev administrator" ||
		p.Namespace != "" || p.BreakGlass {
		t.Fatalf("person = %+v, want a local person dev:admin", p)
	}
}

var errStore = errors.New("store down")

// fixedAuthenticator answers one principal for each token.
type fixedAuthenticator struct {
	p   identity.Principal
	err error
}

func (a fixedAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	return a.p, a.err
}

func personWith(subject string) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: subject,
		DisplayName: subject, Admin: false, TokenID: "tok", TokenExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialSession, JobID: "",
	}
}

// noPrincipal is the principal of a failed authentication.
var noPrincipal = personWith("") //nolint:gochecknoglobals // A frozen test value.

// TestRefuseDevSubjects proves the layer of the authenticator: a token that
// resolves to a dev person authenticates nothing, whatever its case, and
// any other principal and any error of the next authenticator pass.
func TestRefuseDevSubjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		next fixedAuthenticator
		want error
	}{
		{
			name: "dev person",
			next: fixedAuthenticator{p: personWith("dev:admin"), err: nil},
			want: identity.ErrUnauthenticated,
		},
		{
			name: "dev person, upper case",
			next: fixedAuthenticator{p: personWith("DEV:x"), err: nil},
			want: identity.ErrUnauthenticated,
		},
		{name: "other person", next: fixedAuthenticator{p: personWith("alice"), err: nil}, want: nil},
		{
			name: "subject that only looks similar",
			next: fixedAuthenticator{p: personWith("developer"), err: nil},
			want: nil,
		},
		{name: "error of next", next: fixedAuthenticator{p: noPrincipal, err: errStore}, want: errStore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := principal.RefuseDevSubjects(tt.next).AuthenticateToken(t.Context(), "token")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			if tt.want != nil && p.Subject != "" {
				t.Fatalf("a refused token returned the principal %q", p.Subject)
			}
		})
	}
}
