package principal_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestRefuseNamespaceServiceSubjects proves the layer of the authenticator: a
// credential that resolves to the subject of the namespace service principal
// authenticates nothing, in any case, and the guard returns no principal. The
// server credential is refused too. Any other principal and any error of the
// next authenticator pass.
func TestRefuseNamespaceServiceSubjects(t *testing.T) {
	t.Parallel()

	serverCredential := personWith("alice")
	serverCredential.Credential = identity.CredentialServer

	adminWithPrefix := personWith("system:namespace-service:acme")
	adminWithPrefix.Admin = true

	tests := []struct {
		name string
		p    identity.Principal
		err  error
		want error
	}{
		{"subject", personWith("system:namespace-service:acme"), nil, identity.ErrUnauthenticated},
		{"subject in upper case", personWith("SYSTEM:NAMESPACE-SERVICE:acme"), nil, identity.ErrUnauthenticated},
		{"subject with the admin flag", adminWithPrefix, nil, identity.ErrUnauthenticated},
		{"server credential", serverCredential, nil, identity.ErrUnauthenticated},
		{"other person", personWith("alice"), nil, nil},
		{"similar subject", personWith("system:namespace-servic:acme"), nil, nil},
		{"error of next", noPrincipal, errStore, errStore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			guard := principal.RefuseNamespaceServiceSubjects(fixedAuthenticator{p: tt.p, err: tt.err})

			p, err := guard.AuthenticateToken(t.Context(), "token")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			if tt.want != nil && (p.Subject != "" || p.Admin) {
				t.Fatalf("a refused credential returned the principal %+v", p)
			}
		})
	}
}

// TestRefuseNamespaceServiceSubjectsWithoutNext proves that the guard fails
// closed when it wraps no authenticator.
func TestRefuseNamespaceServiceSubjectsWithoutNext(t *testing.T) {
	t.Parallel()

	_, err := principal.RefuseNamespaceServiceSubjects(nil).AuthenticateToken(t.Context(), "token")
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}
