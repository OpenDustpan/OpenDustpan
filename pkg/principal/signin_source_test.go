package principal_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// TestSignInPrincipalRefusesSourceThatIsNotValid proves that a sign-in that
// makes a principal refuses an empty or unknown source with ErrInvalid and
// stores no principal. An empty source never becomes oidc.
func TestSignInPrincipalRefusesSourceThatIsNotValid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		subject string
		source  principal.Source
	}{
		{name: "empty", subject: "corp:empty-source", source: ""},
		{name: "upper case", subject: "corp:upper-source", source: "OIDC"},
		{name: "space", subject: "corp:space-source", source: " oidc"},
		{name: "unknown", subject: "corp:unknown-source", source: "saml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newSignInFixture(t)
			in := signIn(tc.subject, true, "Alice Doe")
			in.Source = tc.source

			p, created, err := f.svc.SignInPrincipal(t.Context(), testEntry(), in)
			if !errors.Is(err, principal.ErrInvalid) || created {
				t.Fatalf("SignInPrincipal(source %q) = %+v, created %v, %v; want ErrInvalid",
					tc.source, p, created, err)
			}

			got, err := f.store.GetPrincipalBySubject(t.Context(), tc.subject)
			if !errors.Is(err, principal.ErrNotFound) {
				t.Fatalf("GetPrincipalBySubject(%q) = %+v, %v; want ErrNotFound", tc.subject, got, err)
			}
		})
	}
}
