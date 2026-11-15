package access_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// namespacedDirectory reads the store, and reports the principal id as a
// principal of the namespace acme. The store keeps it as a principal of the
// installation, so only the Service sees the namespace.
type namespacedDirectory struct {
	access.Directory

	id string
}

func (d namespacedDirectory) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	p, err := d.Directory.GetPrincipal(ctx, id)
	if err == nil && p.ID == d.id {
		p.Namespace = "acme"
	}

	return p, err
}

// TestGrantChecksSubjectNamespace proves that the Service refuses a grant
// to a principal of a namespace on an object outside that namespace before
// the store sees it, so the refusal does not depend on the store.
func TestGrantChecksSubjectNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.person(t, "bot")

	svc, err := access.NewService(access.ServiceConfig{
		Store: f.store, Directory: namespacedDirectory{Directory: f.store, id: "prn-bot"}, Namespaces: f.store,
		Authorizer: f.authz,
		Reach: access.ReachConfig{
			Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store,
			Collections: f.units, Units: f.store,
		},
		Now: nil, NewID: nil, BootstrapAdmins: []string{"admin"}, Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	for _, obj := range []identity.Object{identity.NamespaceObject("beta"), identity.ServerObject()} {
		_, grantErr := svc.Grant(t.Context(), testEntry(), admin(), access.GrantInput{
			Object: obj, Role: access.RoleViewer, Subject: prn("bot"),
		})
		if !errors.Is(grantErr, access.ErrInvalid) {
			t.Errorf("grant on %s to a principal of acme = %v, want ErrInvalid", obj.String(), grantErr)
		}
	}

	_, err = svc.Grant(t.Context(), testEntry(), admin(), access.GrantInput{
		Object: identity.NamespaceObject("acme"), Role: access.RoleViewer, Subject: prn("bot"),
	})
	if err != nil {
		t.Fatalf("grant on its own namespace = %v, want nil", err)
	}
}
