package access_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// collectionReviewFixture is a service over a store with real collections.
// The collection payments of acme holds the workspaces web and api (api is
// deleted), the collection platform holds data, and loose is in none.
type collectionReviewFixture struct {
	*fixture

	svc     *access.Service
	payment identity.Object
	ids     map[string]string
}

func newCollectionReviewFixture(t *testing.T) *collectionReviewFixture {
	t.Helper()

	f := newFixture(t)
	ctx := t.Context()

	cols, err := collection.NewService(collection.Config{Store: f.store, Namespaces: f.store, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: cols, Units: f.store, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: f.store, Directory: f.store, Namespaces: f.store, Authorizer: authz, Now: nil, NewID: nil,
		BootstrapAdmins: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	ns, err := f.store.GetNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	r := &collectionReviewFixture{fixture: f, svc: svc, payment: identity.Object{}, ids: map[string]string{}}
	made := map[string]collection.Collection{}

	for _, name := range []string{"payments", "platform"} {
		ref := collection.Ref{Namespace: "acme", Name: collection.Name(name)}

		c, createErr := cols.Create(ctx, ref, collectionEntry("collection:create"))
		if createErr != nil {
			t.Fatalf("create collection %s: %v", name, createErr)
		}

		made[name] = c
	}

	for name, in := range map[string]string{"web": "payments", "api": "payments", "data": "platform", "loose": ""} {
		r.ids[name] = r.addWorkspace(t, ns, name)

		if in == "" {
			continue
		}

		move := collection.Move{
			Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: name},
			From: "", To: made[in].Ref, ToID: made[in].ID, EndsLock: false,
		}
		if moveErr := cols.SetUnit(ctx, move, collectionEntry("unit:move")); moveErr != nil {
			t.Fatalf("move %s: %v", name, moveErr)
		}
	}

	r.payment = identity.CollectionObject("acme", "payments").WithCollectionID(made["payments"].ID)

	gone := collectionEntry("unit:manage")
	gone.Object = "opentofu:acme/api"

	deletion := inventory.Deletion{Entry: gone, PurgeAfter: gone.At.Add(time.Hour)}
	ref := workspace.Ref{Namespace: "acme", Name: "api"}

	if delErr := f.store.DeleteWorkspace(ctx, ref, ns.Fence(), deletion); delErr != nil {
		t.Fatalf("delete workspace: %v", delErr)
	}

	return r
}

// addWorkspace stores the workspace name in ns and returns its stable ID.
func (r *collectionReviewFixture) addWorkspace(t *testing.T, ns namespace.Namespace, name string) string {
	t.Helper()

	wsName, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: "", CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0),
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        workspace.ID(ident.NewID("ws-")),
		Ref:       workspace.Ref{Namespace: "acme", Name: wsName},
		Revision:  1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := r.store.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}

	return string(w.ID)
}

// collectionEntry returns a valid audit entry in acme.
func collectionEntry(action string) audit.Entry {
	e := testEntry()
	e.At, e.Namespace, e.Action, e.Object = time.UnixMilli(1_700_000_000_000).UTC(), "acme", action, "acme/web"
	e.Kind, e.Outcome = audit.KindChange, audit.OutcomeSuccess

	return e
}

// grantOn stores a grant of role to subject on obj.
func (r *collectionReviewFixture) grantOn(
	t *testing.T, id string, subject access.Subject, role string, obj identity.Object,
) {
	t.Helper()

	fence, e := storetest.GrantWrite(t, r.store, obj, time.Unix(0, 0), "grant:"+id)

	err := r.store.CreateGrant(t.Context(), access.Grant{
		CreatedAt: time.Unix(0, 0), Condition: nil, Object: obj, ID: id, Role: role, CreatedBy: "admin",
		Subject: subject,
	}, fence, e, nil)
	if err != nil {
		t.Fatalf("create grant %s: %v", id, err)
	}
}

func (r *collectionReviewFixture) unitOf(t *testing.T, name string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return obj.WithUnitID(r.ids[name])
}

// TestReviewOfACollection proves that the export of a collection holds the
// grants on the collection, on the units in it now (a deleted unit included,
// with its state), and on the objects that contain it, and none on a unit of
// another collection or of none. It also proves that the rows carry the
// membership of a group (via) and the last access of a principal.
func TestReviewOfACollection(t *testing.T) {
	t.Parallel()

	r := newCollectionReviewFixture(t)
	r.person(t, "alice")
	r.person(t, "bob")

	ops := r.group(t, "ops", "bob")

	r.grantOn(t, "grt-col", prn("alice"), access.RoleAuditor, r.payment)
	r.grantOn(t, "grt-web", prn("alice"), access.RoleViewer, r.unitOf(t, "web"))
	r.grantOn(t, "grt-api", ops, access.RoleViewer, r.unitOf(t, "api"))
	r.grantOn(t, "grt-data", prn("alice"), access.RoleViewer, r.unitOf(t, "data"))
	r.grantOn(t, "grt-loose", prn("alice"), access.RoleViewer, r.unitOf(t, "loose"))
	r.grantOn(t, "grt-ns", prn("bob"), access.RoleWriter, identity.NamespaceObject("acme"))
	r.grantOn(t, "grt-ns-reader", prn("bob"), access.RoleViewer, identity.NamespaceObject("acme"))
	r.grantOn(t, "grt-other-ns", prn("bob"), access.RoleWriter, identity.NamespaceObject("beta"))

	page, err := r.svc.Review(t.Context(), r.payment, access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: true,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	type want struct {
		state     string
		inherited bool
		via       bool
		tracked   bool
	}

	wants := map[string]want{
		"grt-col": {state: access.ObjectLive, inherited: false, via: false, tracked: true},
		"grt-web": {state: access.ObjectLive, inherited: false, via: false, tracked: true},
		"grt-api": {state: access.ObjectDeleted, inherited: false, via: true, tracked: true},
		"grt-ns":  {state: access.ObjectLive, inherited: true, via: false, tracked: true},
		// A viewer on the namespace gives a data action, so it is in the export.
		"grt-ns-reader": {state: access.ObjectLive, inherited: true, via: false, tracked: true},
	}

	got := map[string]access.ReviewEntry{}

	for i := range page.Entries {
		e := page.Entries[i]
		if e.Holder == access.BootstrapHolder {
			continue
		}

		got[e.Row.Grant.ID] = e
	}

	if len(got) != len(wants) {
		t.Fatalf("export holds %d grants, want %v", len(got), wants)
	}

	for id, w := range wants {
		e, ok := got[id]
		if !ok {
			t.Fatalf("export lacks grant %s", id)
		}

		if e.ObjectState != w.state || (e.InheritedFrom != nil) != w.inherited || (e.Row.Via != nil) != w.via ||
			e.Row.LastAccessTracked != w.tracked {
			t.Errorf("grant %s: state %q, inherited %t, via %t, tracked %t; want %+v",
				id, e.ObjectState, e.InheritedFrom != nil, e.Row.Via != nil, e.Row.LastAccessTracked, w)
		}
	}
}

// TestReviewOfACollectionRefusals proves that the export of a collection that
// does not exist, or of a namespace that does not exist, answers ErrNotFound.
func TestReviewOfACollectionRefusals(t *testing.T) {
	t.Parallel()

	r := newCollectionReviewFixture(t)

	for _, tc := range []struct {
		name string
		obj  identity.Object
	}{
		{name: "missing collection", obj: identity.CollectionObject("acme", "nothing")},
		{name: "missing namespace", obj: identity.CollectionObject("gone", "payments")},
		{name: "collection of another namespace", obj: identity.CollectionObject("beta", "payments")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := r.svc.Review(t.Context(), tc.obj, access.ReviewRequest{
				From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
			})
			if !errors.Is(err, access.ErrNotFound) {
				t.Fatalf("review = %v, want ErrNotFound", err)
			}
		})
	}
}
