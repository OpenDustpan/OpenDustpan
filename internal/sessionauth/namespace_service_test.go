package sessionauth_test

import (
	"errors"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestRefusesNamespaceServiceSubjects proves that a session whose owner has
// the subject of the namespace service principal authenticates nothing, in any
// case of the prefix, with no option set, and that it returns no principal.
func TestRefusesNamespaceServiceSubjects(t *testing.T) {
	t.Parallel()

	secret := newSecret(t)

	for _, subject := range []string{
		"system:namespace-service:acme",
		"System:Namespace-Service:acme",
		"SYSTEM:NAMESPACE-SERVICE:",
	} {
		match := aliceMatch()
		match.Owner.Subject = subject

		c := newChecker(t, &fakeSessions{err: nil, match: match, calls: 0})

		p, _, err := c.Authenticate(request(t, http.MethodGet, secret.Reveal(), nil))
		if !errors.Is(err, identity.ErrUnauthenticated) {
			t.Errorf("subject %q: err = %v, want ErrUnauthenticated", subject, err)
		}

		if p.Subject != "" || p.Admin {
			t.Errorf("subject %q: a refused session returned the principal %+v", subject, p)
		}
	}
}
