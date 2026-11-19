package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// idAuthorizer is an Authorizer that names a fixed discovery grant ID.
type idAuthorizer struct {
	identity.Authorizer

	id  string
	err error
}

func (a idAuthorizer) DiscoveryGrantID(
	_ context.Context, _ identity.Principal, _ namespace.Name,
) (string, error) {
	return a.id, a.err
}

// TestDiscoveryGrantIDKeepsAuthorizerError proves that an error of the
// authorizer reaches the caller with no ID.
func TestDiscoveryGrantIDKeepsAuthorizerError(t *testing.T) {
	t.Parallel()

	authz := idAuthorizer{Authorizer: nil, id: "", err: errReadFailed}

	id, found, err := access.DiscoveryGrantID(t.Context(), authz, person(), "acme")
	if !found || id != "" || !errors.Is(err, errReadFailed) {
		t.Fatalf("DiscoveryGrantID = %q, %v, %v; want no ID, true, the error", id, found, err)
	}
}

// person returns a principal for the fake authorizer, which ignores it.
func person() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "usr-test",
		DisplayName:            "",
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
}
