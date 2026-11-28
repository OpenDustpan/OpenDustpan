package accesstest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// housekeepingCases returns the cases of the grants that a created namespace
// gets for the housekeeping principals.
func housekeepingCases() []struct {
	test func(t *testing.T, s access.Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "namespace creation housekeeping grants: five grants", test: testNamespaceHousekeepingGrants},
		{
			name: "namespace creation housekeeping grants: a namespace without a creator",
			test: testNamespaceHousekeepingGrantsAlone,
		},
		{
			name: "namespace creation housekeeping grants: a failed insert leaves no namespace",
			test: testNamespaceHousekeepingGrantFails,
		},
		{
			name: "namespace creation housekeeping grants: a taken namespace",
			test: testNamespaceHousekeepingTaken,
		},
		{
			name: "namespace creation housekeeping grants: a grant with no ID",
			test: testNamespaceHousekeepingNoID,
		},
		{
			name: "namespace creation housekeeping grants: only a housekeeping grant gets the reason",
			test: testNamespaceHousekeepingReason,
		},
	}
}

// housekeepingNamespaceGrants returns the grants that a created namespace
// gets: one grant for each purpose that acts inside a namespace, with the
// server as the creator and the time of the namespace.
func housekeepingNamespaceGrants(ns namespace.Namespace) []access.Grant {
	purposes := principal.HousekeepingPurposes()
	grants := make([]access.Grant, 0, len(purposes))

	for _, p := range purposes {
		if !p.ActsInsideNamespace() {
			continue
		}

		g := newGrant(
			"grt-"+p.Name(), principalSubject(p.ID()), p.NamespaceRole(), identity.NamespaceObject(ns.Name),
		)
		g.CreatedBy = "@server"
		g.CreatedAt = ns.CreatedAt

		grants = append(grants, g)
	}

	return grants
}

// wantHousekeepingGrants checks that the grants on the namespace are exactly
// want, each with the server as its creator and the time of the namespace.
func wantHousekeepingGrants(t *testing.T, s access.Store, ns namespace.Namespace, want []access.Grant) {
	t.Helper()

	list, total, err := s.ListGrantsOn(t.Context(), identity.NamespaceObject(ns.Name), everyGrant())
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	if int(total) != len(want) || len(list) != len(want) {
		t.Fatalf("grants on %q = %d (total %d), want %d", ns.Name, len(list), total, len(want))
	}

	for i := range want {
		w := &want[i]

		got, getErr := s.GetGrant(t.Context(), w.ID)
		if getErr != nil {
			t.Fatalf("get grant %q: %v", w.ID, getErr)
		}

		assertGrant(t, got, *w)

		if got.CreatedBy != "@server" || !got.CreatedAt.Equal(ns.CreatedAt) {
			t.Fatalf(
				"grant %q creator = %q at %v, want @server at %v", w.ID, got.CreatedBy, got.CreatedAt, ns.CreatedAt,
			)
		}
	}
}

// wantNamespaceEntries checks that the chain of the namespace holds exactly
// one entry for each grant, written by the store from the entry e of @server:
// the action of e, the object of the grant and its time. It checks that the
// chain is valid. A store without the audit log skips it.
func wantNamespaceEntries(
	t *testing.T, s access.Store, ns namespace.Namespace, e audit.Entry, want []access.Grant,
) {
	t.Helper()

	log, ok := s.(audit.Store)
	if !ok {
		return
	}

	listing, err := log.List(t.Context(), ns.Name, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list audit of %q: %v", ns.Name, err)
	}

	if len(listing.Entries) != len(want) {
		t.Fatalf("entries of %q = %d, want one for each of the %d grants", ns.Name, len(listing.Entries), len(want))
	}

	for i := range want {
		checkGrantEntry(t, listing.Entries, e, &want[i])
	}

	check, err := log.Check(t.Context(), ns.Name)
	if err != nil || !check.Valid {
		t.Fatalf("check of %q = %+v, %v, want a valid chain", ns.Name, check, err)
	}
}

// checkGrantEntry checks that entries hold exactly one entry of the grant w,
// with the action of e, the kind change and a time not before the grant. The
// reason is access.NamespaceGrantsReason when a housekeeping principal holds
// w, and the reason of e for any other holder.
func checkGrantEntry(t *testing.T, entries []audit.Entry, e audit.Entry, w *access.Grant) {
	t.Helper()

	reason := e.Reason
	if access.HousekeepingSubject(w.Subject) {
		reason = access.NamespaceGrantsReason
	}

	found := 0

	for j := range entries {
		got := &entries[j]
		if got.Object != access.GrantRef(*w) {
			continue
		}

		found++

		// The time never goes back along the chain, so an entry after an
		// earlier grant can take its time.
		if got.Action != e.Action || got.Kind != audit.KindChange || got.At.Before(w.CreatedAt) {
			t.Fatalf("entry of grant %q = %+v, want the action %q from %v", w.ID, got, e.Action, w.CreatedAt)
		}

		if got.Reason != reason {
			t.Fatalf("entry of grant %q reason = %q, want %q", w.ID, got.Reason, reason)
		}
	}

	if found != 1 {
		t.Fatalf("entries of grant %q = %d, want 1", w.ID, found)
	}
}

func testNamespaceHousekeepingGrants(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants := housekeepingNamespaceGrants(ns)

	if len(grants) != 5 {
		t.Fatalf("housekeeping grants of a namespace = %d, want 5", len(grants))
	}

	creator := newGrant(
		"grt-creator", principalSubject("prn-a"), access.RoleNamespaceAdmin, identity.NamespaceObject("acme"),
	)
	creatorEntry := entry()
	creatorEntry.Object = access.GrantRef(creator)

	all := append([]access.Grant{creator}, grants...)

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, all, creatorEntry); err != nil {
		t.Fatalf("create namespace with grants: %v", err)
	}

	list, _, err := s.ListGrantsOn(t.Context(), identity.NamespaceObject("acme"), everyGrant())
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	if len(list) != 6 {
		t.Fatalf("grants on acme = %d, want the creator and five housekeeping grants", len(list))
	}

	for i := range grants {
		w := &grants[i]

		got, getErr := s.GetGrant(t.Context(), w.ID)
		if getErr != nil {
			t.Fatalf("get grant %q: %v", w.ID, getErr)
		}

		if got.CreatedBy != "@server" || !got.CreatedAt.Equal(ns.CreatedAt) || got.Object != w.Object {
			t.Fatalf("grant %q = %+v, want the server as creator at %v on %v", w.ID, got, ns.CreatedAt, w.Object)
		}
	}

	wantNamespaceEntries(t, s, ns, creatorEntry, all)
}

func testNamespaceHousekeepingGrantsAlone(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants := housekeepingNamespaceGrants(ns)

	// The entry of @server names the namespace: the store builds the entry of
	// each grant.
	e := entry()
	e.Object = "namespace:acme"

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, e); err != nil {
		t.Fatalf("create namespace with grants: %v", err)
	}

	wantHousekeepingGrants(t, s, ns, grants)
	wantNamespaceEntries(t, s, ns, e, grants)
}

func testNamespaceHousekeepingGrantFails(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants := housekeepingNamespaceGrants(ns)

	// The last grant repeats the ID of the first: its insert fails.
	grants[len(grants)-1].ID = grants[0].ID

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry()); err == nil {
		t.Fatal("create namespace with a duplicate grant ID = nil, want an error")
	}

	if nss, ok := s.(namespace.Store); ok {
		if _, err := nss.GetNamespace(t.Context(), "acme"); !errors.Is(err, namespace.ErrNotFound) {
			t.Fatalf("namespace after a failed grant = %v, want namespace.ErrNotFound", err)
		}
	}

	if _, err := s.GetGrant(t.Context(), grants[0].ID); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant after a failed create = %v, want ErrNotFound", err)
	}
}

func testNamespaceHousekeepingTaken(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants := housekeepingNamespaceGrants(ns)

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry()); err != nil {
		t.Fatalf("create namespace with grants: %v", err)
	}

	again := housekeepingNamespaceGrants(ns)
	for i := range again {
		again[i].ID += "-again"
	}

	err := s.CreateNamespaceWithGrants(t.Context(), ns, again, entry())
	if !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create taken namespace = %v, want namespace.ErrAlreadyExists", err)
	}

	wantHousekeepingGrants(t, s, ns, grants)
}

// testNamespaceHousekeepingReason checks that the store sets
// access.NamespaceGrantsReason only on the entry of a grant that a
// housekeeping principal holds. The entry of the creator grant keeps the
// reason of e.
func testNamespaceHousekeepingReason(t *testing.T, s access.Store) {
	t.Helper()

	log, ok := s.(audit.Store)
	if !ok {
		t.Skip("the store has no audit log")
	}

	ns := newNamespace("acme")
	creator := newGrant(
		"grt-creator", principalSubject("prn-a"), access.RoleNamespaceAdmin, identity.NamespaceObject("acme"),
	)
	housekeeping := housekeepingNamespaceGrants(ns)

	e := entry()
	e.Object = access.GrantRef(creator)
	e.Reason = "namespace made by a person"

	all := append([]access.Grant{creator}, housekeeping...)

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, all, e); err != nil {
		t.Fatalf("create namespace with grants: %v", err)
	}

	listing, err := log.List(t.Context(), ns.Name, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list audit of %q: %v", ns.Name, err)
	}

	want := map[string]string{access.GrantRef(creator): e.Reason}
	for i := range housekeeping {
		want[access.GrantRef(housekeeping[i])] = access.NamespaceGrantsReason
	}

	if len(listing.Entries) != len(want) {
		t.Fatalf("entries of %q = %d, want %d", ns.Name, len(listing.Entries), len(want))
	}

	for i := range listing.Entries {
		got := &listing.Entries[i]
		if reason, found := want[got.Object]; !found || got.Reason != reason {
			t.Fatalf("entry of %q reason = %q, want %q", got.Object, got.Reason, reason)
		}
	}
}

func testNamespaceHousekeepingNoID(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants := housekeepingNamespaceGrants(ns)
	grants[len(grants)-1].ID = ""

	err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry())
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("create with a grant with no ID = %v, want ErrInvalid", err)
	}

	if nss, ok := s.(namespace.Store); ok {
		if _, getErr := nss.GetNamespace(t.Context(), "acme"); !errors.Is(getErr, namespace.ErrNotFound) {
			t.Fatalf("namespace after a refused create = %v, want namespace.ErrNotFound", getErr)
		}
	}
}
