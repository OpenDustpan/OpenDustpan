package access_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestReviewShowsTheStateOfEachUnit proves that an export keeps the grants
// of a deleted unit (with its name at the delete) and of a purged unit, so
// a reviewer sees them and can clean them up.
func TestReviewShowsTheStateOfEachUnit(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	f.person(t, "alice")

	web := unitObject(t, "acme", "web/prod")
	f.grant(t, prn("alice"), access.RoleViewer, web)

	purged := unitObject(t, "acme", "old/prod").WithUnitID("stk-purged")
	fence, e := storetest.GrantWrite(t, f.store, purged, time.Unix(0, 0), "grant:grt-purged")

	err := f.store.CreateGrant(ctx, access.Grant{
		CreatedAt: time.Unix(0, 0), Condition: nil, Object: purged, ID: "grt-purged", Role: access.RoleViewer,
		CreatedBy: "admin", Subject: prn("alice"),
	}, fence, e)
	if err != nil {
		t.Fatalf("grant on a purged unit: %v", err)
	}

	deleteStack(t, f, "web", "prod")

	page, err := f.svc.Review(ctx, identity.NamespaceObject("acme"), access.ReviewRequest{
		From: nil, To: nil, Subject: "principal:prn-alice", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	states := map[string]string{}
	for i := range page.Entries {
		states[page.Entries[i].Row.Grant.Object.String()] = page.Entries[i].ObjectState
	}

	want := map[string]string{"pulumi:acme/web/prod": access.ObjectDeleted, "pulumi:acme/old/prod": access.ObjectPurged}
	if len(states) != len(want) {
		t.Fatalf("states = %v, want %v", states, want)
	}

	for obj, state := range want {
		if states[obj] != state {
			t.Errorf("state of %s = %q, want %q", obj, states[obj], state)
		}
	}
}

// deleteStack soft-deletes the stack project/name of acme.
func deleteStack(t *testing.T, f *fixture, project, name string) {
	t.Helper()

	ctx := t.Context()

	ns, err := f.store.GetNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", project, name)
	if err != nil {
		t.Fatalf("stack ref: %v", err)
	}

	gone := testEntry()
	gone.At, gone.Namespace, gone.Action = time.Unix(1, 0), "acme", "unit:manage"
	gone.Object, gone.Kind, gone.Outcome = "pulumi:acme/"+project+"/"+name, audit.KindChange, audit.OutcomeSuccess

	if err := f.store.DeleteStack(ctx, ref, ns.Fence(), stack.EmptyGuard(), gone); err != nil {
		t.Fatalf("delete stack: %v", err)
	}
}

// TestReviewEffectiveTimes proves the times that a row gave access: the
// overlap of the grant and of the membership, and the snapshot that shows a
// later revoke as not yet done.
func TestReviewEffectiveTimes(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	f.person(t, "alice")
	ops := f.group(t, "ops", "alice")

	acme := identity.NamespaceObject("acme")
	f.grant(t, ops, access.RoleWriter, acme)

	from, to := time.Unix(0, 0), time.Now().Add(time.Hour)

	page, err := f.svc.Review(ctx, acme, access.ReviewRequest{
		From: &from, To: &to, Subject: "principal:prn-alice", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	var via *access.ReviewEntry

	for i := range page.Entries {
		if page.Entries[i].Row.Via != nil {
			via = &page.Entries[i]
		}
	}

	if via == nil {
		t.Fatalf("history of alice = %+v, want a row through ops", page.Entries)
	}

	// The grant is older than the membership, so access starts with the
	// membership. A history leaves out the member count.
	if !via.EffectiveFrom.Equal(via.Row.Via.AddedAt) || via.EffectiveUntil != nil || via.Row.MemberCount != nil ||
		page.AsOf != nil || page.Total == nil {
		t.Fatalf("row through ops = %+v (as of %v), want access from the membership, no end, no count", via, page.AsOf)
	}
}

// TestReviewBootstrapRows proves that each bootstrap admin token has a row
// on the first page of an export, and none with a subject filter.
func TestReviewBootstrapRows(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	page, err := f.svc.Review(ctx, identity.NamespaceObject("acme"), access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	if len(page.Entries) != 1 {
		t.Fatalf("export of acme = %+v, want the bootstrap admin only", page.Entries)
	}

	e := page.Entries[0]
	if e.Holder != access.BootstrapHolder || e.Row.SubjectName != "admin" || !e.CanGrantAccess ||
		e.InheritedFrom == nil || !e.InheritedFrom.IsServer() || *page.Total != 1 {
		t.Fatalf("bootstrap row = %+v, want the admin token on @server", e)
	}

	page, err = f.svc.Review(ctx, identity.NamespaceObject("acme"), access.ReviewRequest{
		From: nil, To: nil, Subject: "principal:prn-alice", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("export of alice = %+v, %v; want no row", page.Entries, err)
	}
}

// TestReviewBootstrapRowsCountInTheLimit pages an export one row at a
// time: the bootstrap row fills the first page, and the grant comes next.
// With no grant, the bootstrap row is the last page.
func TestReviewBootstrapRowsCountInTheLimit(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")

	if empty := pageOf(t, f, acme, ""); len(empty.Entries) != 1 || empty.NextCursor != "" {
		t.Fatalf("export of acme with no grant = %+v; want the bootstrap row only, no next page", empty)
	}

	f.person(t, "alice")
	f.grant(t, prn("alice"), access.RoleViewer, acme)

	first := pageOf(t, f, acme, "")
	if len(first.Entries) != 1 || first.Entries[0].Holder != access.BootstrapHolder ||
		first.NextCursor == "" || *first.Total != 2 {
		t.Fatalf("first page = %+v; want the bootstrap row, a next page and a total of 2", first)
	}

	if next := pageOf(t, f, acme, first.NextCursor); len(next.Entries) != 1 || next.Entries[0].Holder != "" ||
		next.NextCursor != "" {
		t.Fatalf("second page = %+v; want the grant of alice and no next page", next)
	}
}

// pageOf reads the page of the snapshot export of obj after cursor, one
// row at a time.
func pageOf(t *testing.T, f *fixture, obj identity.Object, cursor string) access.ReviewPage {
	t.Helper()

	page, err := f.svc.Review(t.Context(), obj, access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: cursor, Limit: 1, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	return page
}

// TestReviewMarksWhoCanGrantAccess checks canGrantAccess: a role that can
// change who has access (here, the members of groups) is marked, and a
// role that manages service tokens is not, because a service token gets no
// access without a grant.
func TestReviewMarksWhoCanGrantAccess(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	server := identity.ServerObject()

	for _, r := range []struct {
		name   string
		action identity.Action
	}{
		{"groups", identity.ActionServerManageGroups},
		{"service-tokens", identity.ActionServerManageServiceTokens},
	} {
		role, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
			Name: r.name, Description: "", Actions: []identity.Action{r.action},
		})
		if err != nil {
			t.Fatalf("create role %s: %v", r.name, err)
		}

		f.person(t, r.name)
		f.grant(t, prn(r.name), role.ID, server)
	}

	page, err := f.svc.Review(ctx, server, access.ReviewRequest{
		From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
	})
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	got := map[string]bool{}
	for i := range page.Entries {
		got[page.Entries[i].Row.SubjectName] = page.Entries[i].CanGrantAccess
	}

	if !got["groups"] || got["service-tokens"] {
		t.Fatalf("canGrantAccess = %v, want groups true and service-tokens false", got)
	}
}
