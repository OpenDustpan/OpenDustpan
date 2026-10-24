package access_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/identity/identitytest"
)

// TestNamespaceServiceSuite runs the namespace service principal suite against
// the authorizer of grants, which decides these principals itself.
func TestNamespaceServiceSuite(t *testing.T) {
	t.Parallel()

	identitytest.RunNamespaceService(t, func(t *testing.T) identity.Authorizer {
		t.Helper()

		return newFixture(t).authz
	})
}
