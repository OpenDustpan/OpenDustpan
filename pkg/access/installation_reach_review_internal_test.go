package access

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// emptyReviewStore is a Store that holds no custom role. principalReviewScope
// only lists roles, so no other method is used.
type emptyReviewStore struct {
	Store
}

func (emptyReviewStore) ListRoles(context.Context) ([]Role, error) {
	return nil, nil
}

// emptyReviewCallers is a Callers port with no subject.
type emptyReviewCallers struct{}

func (emptyReviewCallers) SubjectsOf(context.Context, string) ([]Subject, error) {
	return nil, nil
}

// emptyReviewPrincipals is a PrincipalNamespaces port with no principal.
type emptyReviewPrincipals struct{}

func (emptyReviewPrincipals) GetPrincipal(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, principal.ErrNotFound
}

func (emptyReviewPrincipals) ListNamespacePrincipals(
	context.Context, namespace.Name, principal.Query,
) ([]principal.Principal, int64, error) {
	return nil, 0, nil
}

// reviewDirectory is a Directory that answers the principals in recs.
type reviewDirectory struct {
	recs map[string]principal.Principal
}

func (d reviewDirectory) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	rec, ok := d.recs[id]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return rec, nil
}

func (reviewDirectory) GetPrincipalBySubject(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, principal.ErrNotFound
}

func (reviewDirectory) GetGroup(context.Context, string) (principal.Group, error) {
	return principal.Group{}, principal.ErrNotFound
}

func (reviewDirectory) ListMembers(
	context.Context, string, principal.Query,
) ([]principal.Member, int64, error) {
	return nil, 0, nil
}

// emptyReviewNamespaces is a Namespaces port that holds no namespace.
type emptyReviewNamespaces struct{}

func (emptyReviewNamespaces) GetNamespace(
	context.Context, namespace.Name,
) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

// reviewScopeFixture is a Service whose directory holds a service principal
// of a namespace and one of the installation.
type reviewScopeFixture struct {
	svc  *Service
	ns   principal.Principal
	inst principal.Principal
}

// reviewScopeService builds a Service whose directory holds a service
// principal of a namespace and one of the installation, with the reach value.
func reviewScopeService(tb testing.TB, reaches bool) reviewScopeFixture {
	tb.Helper()

	at := time.Unix(0, 0)
	nsPrincipal := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-svc", Subject: "svc",
		DisplayName: "svc", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "acme", GroupCount: 0, BreakGlass: false,
	}
	instPrincipal := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-inst", Subject: "inst",
		DisplayName: "inst", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	settings := FixedSettings(reaches)
	authz, err := NewAuthorizer(AuthorizerConfig{
		Store: emptyReviewStore{Store: nil}, Callers: emptyReviewCallers{}, Collections: nil,
		Units: nil, Principals: emptyReviewPrincipals{},
	})
	if err != nil {
		tb.Fatalf("authorizer: %v", err)
	}
	authz = authz.WithSettingsPort(settings)

	built, err := NewService(ServiceConfig{
		Store: emptyReviewStore{Store: nil},
		Directory: reviewDirectory{recs: map[string]principal.Principal{
			"prn-svc":  nsPrincipal,
			"prn-inst": instPrincipal,
		}},
		Namespaces: emptyReviewNamespaces{},
		Authorizer: authz,
		Reach: ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil,
			Collections: nil, Units: nil, Settings: settings,
		},
		Now: nil, NewID: nil, BootstrapAdmins: []string{"admin"}, Settings: settings, LimitGroups: nil,
	})
	if err != nil {
		tb.Fatalf("service: %v", err)
	}

	return reviewScopeFixture{svc: built, ns: nsPrincipal, inst: instPrincipal}
}

// scopeHasServer reports whether scope names the installation.
func scopeHasServer(scope ReviewScope) bool {
	for _, c := range scope.Containers {
		if c == identity.ServerObject().String() {
			return true
		}
	}

	return false
}

// checkNamespaceScopeAllows checks that a namespace principal keeps @server.
func checkNamespaceScopeAllows(t *testing.T, svc *Service, id string) {
	t.Helper()

	scope, err := svc.principalReviewScope(t.Context(), identity.PrincipalObject(id))
	if err != nil {
		t.Fatalf("principalReviewScope: %v", err)
	}

	if !scopeHasServer(scope) {
		t.Fatalf("namespace principal Containers has no @server (containers %v)", scope.Containers)
	}
}

// checkNamespaceScopeDenies checks that a namespace principal loses @server.
func checkNamespaceScopeDenies(t *testing.T, svc *Service, id string) {
	t.Helper()

	scope, err := svc.principalReviewScope(t.Context(), identity.PrincipalObject(id))
	if err != nil {
		t.Fatalf("principalReviewScope: %v", err)
	}

	if scopeHasServer(scope) {
		t.Fatalf("namespace principal Containers has @server (containers %v)", scope.Containers)
	}
}

// checkInstallationScope checks that an installation principal keeps @server.
func checkInstallationScope(t *testing.T, svc *Service, id string) {
	t.Helper()

	scope, err := svc.principalReviewScope(t.Context(), identity.PrincipalObject(id))
	if err != nil {
		t.Fatalf("principalReviewScope: %v", err)
	}

	if !scopeHasServer(scope) {
		t.Fatalf("installation principal Containers has no @server (containers %v)", scope.Containers)
	}
}

// TestPrincipalReviewScopeInstallationReach checks the containers of the
// export scope of a service principal for each value of the setting.
func TestPrincipalReviewScopeInstallationReach(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		reaches bool
	}{
		{name: "true", reaches: true},
		{name: "false", reaches: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := reviewScopeService(t, tc.reaches)
			if tc.reaches {
				checkNamespaceScopeAllows(t, fx.svc, fx.ns.ID)
			} else {
				checkNamespaceScopeDenies(t, fx.svc, fx.ns.ID)
			}
			checkInstallationScope(t, fx.svc, fx.inst.ID)
		})
	}
}
