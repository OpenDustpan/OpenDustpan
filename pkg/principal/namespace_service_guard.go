package principal

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
)

// NamespaceServiceGuard is an identity.Authenticator that refuses a caller
// whose subject starts with identity.NamespaceServicePrefix.
type NamespaceServiceGuard struct {
	next identity.Authenticator
}

var _ identity.Authenticator = NamespaceServiceGuard{next: nil}

// RefuseNamespaceServiceSubjects returns an authenticator that asks next and
// then refuses each caller whose subject starts with
// identity.NamespaceServicePrefix, in any case, with
// identity.ErrUnauthenticated. The server builds the namespace service
// principal in its own process, so no credential that a client presents ever
// resolves to it. It returns no principal when it refuses.
func RefuseNamespaceServiceSubjects(next identity.Authenticator) NamespaceServiceGuard {
	return NamespaceServiceGuard{next: next}
}

// AuthenticateToken implements identity.Authenticator.
func (g NamespaceServiceGuard) AuthenticateToken(ctx context.Context, token string) (identity.Principal, error) {
	if g.next == nil {
		return identity.Principal{}, fmt.Errorf("%w: no authenticator", identity.ErrUnauthenticated)
	}

	p, err := g.next.AuthenticateToken(ctx, token)
	if err != nil {
		return identity.Principal{}, err
	}

	if identity.IsNamespaceServiceSubject(p.Subject) || p.Credential == identity.CredentialServer {
		return identity.Principal{}, fmt.Errorf("%w: a credential cannot act as the namespace service principal",
			identity.ErrUnauthenticated)
	}

	return p, nil
}

// AdminSubjects returns the subjects of the bootstrap administrators of the
// wrapped authenticator, or none when it names none, so that the guard hides
// no capability of the authenticator that it wraps.
func (g NamespaceServiceGuard) AdminSubjects() []string {
	if a, ok := g.next.(interface{ AdminSubjects() []string }); ok {
		return a.AdminSubjects()
	}

	return nil
}
