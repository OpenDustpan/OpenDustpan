package dustpan

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// collectionScope returns the fixture with a collection service over its
// store, an authorizer that knows the collections, and the read scope.
func collectionScope(t *testing.T) (scopeFixture, *collection.Service, deletedReadScope) {
	t.Helper()

	f := newScopeFixture(t)

	svc, err := collection.NewService(collection.Config{Store: f.store, Namespaces: f.store, Now: scopeAt})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: svc, Units: f.store, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	f.authz = authz

	return f, svc, newDeletedReadScope(authz, svc)
}

// makeCollection creates the collection name in ns and returns its object with
// the stable ID.
func makeCollection(
	t *testing.T, f scopeFixture, svc *collection.Service, ns namespace.Name, name string,
) identity.Object {
	t.Helper()

	n, err := collection.ParseName(name)
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	_, e := storetest.GrantWrite(t, f.store, identity.NamespaceObject(ns), scopeAt(), "collection:"+name)

	c, err := svc.Create(t.Context(), collection.Ref{Namespace: ns, Name: n}, e)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	return identity.CollectionObject(ns, name).WithCollectionID(c.ID)
}

// putInCollection moves the unit web/prod of acme into the collection col.
func putInCollection(t *testing.T, f scopeFixture, svc *collection.Service, col identity.Object) {
	t.Helper()

	_, e := storetest.GrantWrite(t, f.store, identity.NamespaceObject("acme"), scopeAt(), "unit:move")
	ref := collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}

	to, err := collection.ParseName(col.CollectionName())
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	move := collection.Move{
		Unit: ref, From: "", To: collection.Ref{Namespace: col.Namespace(), Name: to}, ToID: col.CollectionID(),
		EndsLock: false,
	}
	if err := svc.SetUnit(t.Context(), move, e); err != nil {
		t.Fatalf("move unit: %v", err)
	}
}

// deleteWeb soft-deletes the stack web/prod of acme.
func deleteWeb(t *testing.T, f scopeFixture) {
	t.Helper()

	fence, e := storetest.GrantWrite(t, f.store, identity.NamespaceObject("acme"), scopeAt(), "unit:delete")
	e.Action, e.Object = "unit:manage", "pulumi:acme/web/prod"
	d := inventory.Deletion{Entry: e, PurgeAfter: scopeAt().Add(time.Hour)}

	err := f.store.DeleteStack(t.Context(), stack.Ref{Namespace: "acme", Project: "web", Stack: "prod"}, fence,
		stack.EmptyGuard(), d)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}
}

// TestDeletedReadScopeCollectionGrant proves that a grant on a collection
// covers the deleted units whose last collection it was, and no other deleted
// unit: not a unit of another collection of the namespace, not a unit when the
// grant is on a collection of another namespace, and none on a server with no
// collection port.
func TestDeletedReadScopeCollectionGrant(t *testing.T) {
	t.Parallel()

	f, svc, scope := collectionScope(t)
	payments := makeCollection(t, f, svc, "acme", "payments")
	billing := makeCollection(t, f, svc, "acme", "billing")
	foreign := makeCollection(t, f, svc, "other", "payments")

	putInCollection(t, f, svc, payments)
	deleteWeb(t, f)

	f.seed(t, "carla", payments)
	f.seed(t, "bill", billing)
	f.seed(t, "frank", foreign)

	cases := []struct {
		name    string
		subject string
		ns      namespace.Name
		scope   deletedReadScope
		want    []string
	}{
		{name: "grant on the collection", subject: "carla", ns: "acme", scope: scope, want: []string{"stk-scope"}},
		{
			name:    "grant on the collection, asked for another namespace",
			subject: "carla",
			ns:      "other",
			scope:   scope,
			want:    []string{},
		},
		{
			name:    "grant on another collection of the namespace",
			subject: "bill",
			ns:      "acme",
			scope:   scope,
			want:    []string{},
		},
		{
			name:    "grant on a collection of the same name elsewhere",
			subject: "frank",
			ns:      "acme",
			scope:   scope,
			want:    []string{},
		},
		{
			name:    "grant on a collection of the same name elsewhere, own namespace",
			subject: "frank",
			ns:      "other",
			scope:   scope,
			want:    []string{},
		},
		{
			name: "no collection port", subject: "carla", ns: "acme",
			scope: deletedReadScope{authz: f.authz, units: nil}, want: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := identity.Principal{
				TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: tc.subject, DisplayName: "",
				Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
			}

			all, ids, err := tc.scope.ReadableUnits(t.Context(), p, tc.ns)
			if err != nil {
				t.Fatalf("readable units: %v", err)
			}

			if all || len(ids) != len(tc.want) || (len(ids) == 1 && ids[0] != tc.want[0]) {
				t.Fatalf("readable units = %v %v, want %v", all, ids, tc.want)
			}
		})
	}
}
