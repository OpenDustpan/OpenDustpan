package dustpan

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestGrantOnCollectionReachesItsUnits checks the wiring of the collection
// service as the collections of the authorizer (ADR 51): a grant on a
// collection, keyed by its stable ID, decides the units in that collection
// and no other unit.
func TestGrantOnCollectionReachesItsUnits(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	at := time.Unix(0, 0)

	st := openCollectionStore(t)
	ns := createCollectionNamespace(t, st, at)
	createPrincipals(t, st, at, "alice", "bob")

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.now = func() time.Time { return at }

	collections, err := collection.NewService(collection.Config{Store: st, Namespaces: st, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	payments := collection.Ref{Namespace: ns.Name, Name: collection.Name("payments")}

	created := nsChange(at, ns.Name, "collection:acme/payments")
	if _, createErr := collections.Create(ctx, payments, created); createErr != nil {
		t.Fatalf("create collection: %v", createErr)
	}

	inPayments := seedStack(t, st, ns, "app/prod", "stk-payments")
	moveUnit(t, collections, payments, at)

	outside := seedStack(t, st, ns, "other/prod", "stk-other")

	grants := wireAccess(t, cfg)
	authz := grants.Authorizer()

	admin := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "admin",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	grantInput := access.GrantInput{
		Object:  identity.CollectionObject(ns.Name, "payments"),
		Role:    access.RoleViewer,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-alice"},
	}

	if _, grantErr := grants.Grant(ctx, nsChange(at, ns.Name, "grant:alice"), admin, grantInput); grantErr != nil {
		t.Fatalf("grant on collection: %v", grantErr)
	}

	alice := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "alice",
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	bob := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "bob",
		DisplayName:            "",
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
	}

	tests := []struct {
		name   string
		caller identity.Principal
		unit   identity.Object
		want   error
	}{
		{name: "grant reaches a unit in the collection", caller: alice, unit: inPayments, want: nil},
		{name: "grant does not reach a unit outside it", caller: alice, unit: outside, want: identity.ErrDenied},
		{name: "no grant denies a unit in the collection", caller: bob, unit: inPayments, want: identity.ErrDenied},
	}

	for _, tc := range tests {
		err := authz.Authorize(ctx, tc.caller, identity.ActionUnitRead, tc.unit)

		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: got %v, want nil", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		default:
		}
	}
}

// openCollectionStore opens an empty SQLite store for one test.
func openCollectionStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// createCollectionNamespace stores the namespace acme and returns it.
func createCollectionNamespace(t *testing.T, st *sqlite.Store, at time.Time) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt: at, Name: "acme", HomeRegion: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns
}

// createPrincipals stores one person for each subject.
func createPrincipals(t *testing.T, st *sqlite.Store, at time.Time, subjects ...string) {
	t.Helper()

	for _, subject := range subjects {
		p := principal.Principal{
			CreatedAt:   at,
			UpdatedAt:   at,
			ID:          "prn-" + subject,
			Subject:     subject,
			DisplayName: "",
			Kind:        principal.KindPerson,
			Source:      principal.SourceLocal,
			Namespace:   "",
			GroupCount:  0, BreakGlass: false,
			DisabledAt: time.Time{},
		}
		if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, "principal:"+p.ID)); err != nil {
			t.Fatalf("create principal %q: %v", subject, err)
		}
	}
}

// moveUnit moves app/prod of the namespace acme into the collection to.
func moveUnit(t *testing.T, svc *collection.Service, to collection.Ref, at time.Time) {
	t.Helper()

	target, err := svc.Get(t.Context(), to)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}

	m := collection.Move{
		Unit: collection.UnitRef{Namespace: to.Namespace, Tool: "pulumi", Name: "app/prod"},
		From: "",
		To:   to,
		ToID: target.ID,
	}
	if err := svc.SetUnit(t.Context(), m, nsChange(at, to.Namespace, "unit:app/prod")); err != nil {
		t.Fatalf("move unit: %v", err)
	}
}

// wireAccess builds the authorizer and the grant service of the server from
// cfg, as the server does, and returns both.
func wireAccess(t *testing.T, cfg *settings) *access.Service {
	t.Helper()

	authz, err := defaultAuthorizer(cfg)
	if err != nil {
		t.Fatalf("default authorizer: %v", err)
	}

	cfg.authorizer = authz

	grants, err := newAccessService(cfg)
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	if grants == nil {
		t.Fatal("access service: the store keeps no roles and grants")
	}

	return grants
}

// nsChange returns a valid audit entry of a change in the namespace ns, for
// the record object at the time at.
func nsChange(at time.Time, ns namespace.Name, object string) audit.Entry {
	e := storetest.ServerChange(at, object)
	e.Namespace = ns

	return e
}

// seedStack stores the Pulumi stack name in ns with the stable ID id, and
// returns the unit object with that ID.
func seedStack(t *testing.T, st *sqlite.Store, ns namespace.Namespace, name, id string) identity.Object {
	t.Helper()

	project, stackName, _ := strings.Cut(name, "/")

	ref, err := stack.ParseRef(ns.Name.String(), project, stackName)
	if err != nil {
		t.Fatalf("stack ref: %v", err)
	}

	s := stack.Stack{
		CreatedAt: ns.CreatedAt, UpdatedAt: ns.CreatedAt, Tags: map[string]string{}, Ref: ref,
		ID: stack.ID(id), Version: 0,
	}
	if createErr := st.CreateStack(t.Context(), s, ns.Fence()); createErr != nil {
		t.Fatalf("create stack %q: %v", name, createErr)
	}

	u, err := identity.UnitObject("pulumi", ns.Name, name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return u.WithUnitID(id)
}
