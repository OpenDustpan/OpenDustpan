package dustpan

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
)

// namespaceServiceAuthenticator resolves each token to a principal with the
// subject of the namespace service principal.
type namespaceServiceAuthenticator struct{}

func (namespaceServiceAuthenticator) AuthenticateToken(context.Context, string) (identity.Principal, error) {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:     identity.NamespaceServicePrefix + "acme",
		DisplayName: "", Admin: true, TokenID: "tok", TokenExpiresAt: time.Time{},
		Kind: identity.KindService, Credential: identity.CredentialAccessToken, JobID: "",
		JobUnit: identity.Object{},
	}, nil
}

// TestWireNamespaceServiceGuard proves the authentication layer of the
// server: on every kind of installation, a token that resolves to the subject
// of the namespace service principal authenticates nothing.
func TestWireNamespaceServiceGuard(t *testing.T) {
	t.Parallel()

	for _, kind := range []installation.Kind{installation.KindProduction, installation.KindDev, ""} {
		t.Run("kind "+kind.String(), func(t *testing.T) {
			t.Parallel()

			cfg := defaultSettings()
			cfg.installationKind = kind
			cfg.authenticator = namespaceServiceAuthenticator{}

			ports, err := newAuthPorts(cfg, nil)
			if err != nil {
				t.Fatalf("auth ports: %v", err)
			}

			p, err := ports.authenticator.AuthenticateToken(t.Context(), "token")
			if !errors.Is(err, identity.ErrUnauthenticated) {
				t.Fatalf("AuthenticateToken = %v, want ErrUnauthenticated", err)
			}

			if p.Subject != "" || p.Admin {
				t.Fatalf("a refused token returned the principal %+v", p)
			}
		})
	}
}
