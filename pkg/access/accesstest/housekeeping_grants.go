package accesstest

import (
	"errors"
	"slices"
	"strings"
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
		{name: "a created namespace has the five housekeeping grants", test: testNamespaceHousekeepingGrants},
		{name: "a namespace without a creator has the housekeeping grants", test: testNamespaceHousekeepingGrantsAlone},
		{name: "a grant that fails to insert leaves no namespace", test: testNamespaceHousekeepingGrantFails},
		{name: "a taken namespace is not given the housekeeping grants again", test: testNamespaceHousekeepingTaken},
		{name: "grants and grant entries of different length are ErrInvalid", test: testNamespaceHousekeepingLength},
	}
}

// housekeepingNamespaceGrants returns the grants and the entries that a
// created namespace gets: one grant for each purpose that acts inside a
// namespace, with the server as the creator and the time of the namespace.
func housekeepingNamespaceGrants(ns namespace.Namespace) ([]access.Grant, []audit.Entry) {
	purposes := principal.HousekeepingPurposes()
	grants := make([]access.Grant, 0, len(purposes))
	entries := make([]audit.Entry, 0, len(purposes))

	for _, p := range purposes {
		if !p.ActsInsideNamespace() {
			continue
		}

		g := newGrant("grt-"+p.Name(), principalSubject(p.ID()), p.Role(), identity.NamespaceObject(ns.Name))
		g.CreatedBy = "@server"
		g.CreatedAt = ns.CreatedAt

		e := entry()
		e.Object = access.GrantRef(g)
		e.Reason = "housekeeping grants of namespace creation"

		grants = append(grants, g)
		entries = append(entries, e)
	}

	return grants, entries
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

// wantNamespaceEntries checks that the chain of the namespace holds one entry
// for each grant, with the reason of the housekeeping grants, and checks as
// valid. A store without the audit log skips it.
func wantNamespaceEntries(t *testing.T, s access.Store, ns namespace.Namespace, want []access.Grant) {
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
		t.Fatalf("entries of %q = %d, want %d", ns.Name, len(listing.Entries), len(want))
	}

	objects := make([]string, 0, len(want))
	for i := range want {
		objects = append(objects, access.GrantRef(want[i]))
	}

	for i := range listing.Entries {
		e := &listing.Entries[i]
		housekeeping := strings.Contains(e.Object, " housekeeping-")
		hasReason := e.Reason == "housekeeping grants of namespace creation"
		if !slices.Contains(objects, e.Object) || housekeeping != hasReason {
			t.Fatalf("entry of %q = %+v, want a housekeeping grant entry", ns.Name, e)
		}
	}

	check, err := log.Check(t.Context(), ns.Name)
	if err != nil || !check.Valid {
		t.Fatalf("check of %q = %+v, %v, want a valid chain", ns.Name, check, err)
	}
}

func testNamespaceHousekeepingGrants(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants, entries := housekeepingNamespaceGrants(ns)

	if len(grants) != 5 {
		t.Fatalf("housekeeping grants of a namespace = %d, want 5", len(grants))
	}

	creator := newGrant(
		"grt-creator", principalSubject("prn-a"), access.RoleNamespaceAdmin, identity.NamespaceObject("acme"),
	)
	creatorEntry := entry()
	creatorEntry.Object = access.GrantRef(creator)

	all := append([]access.Grant{creator}, grants...)
	allEntries := append([]audit.Entry{creatorEntry}, entries...)

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, all, creatorEntry, allEntries); err != nil {
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

	wantNamespaceEntries(t, s, ns, all)
}

func testNamespaceHousekeepingGrantsAlone(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants, entries := housekeepingNamespaceGrants(ns)

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry(), entries); err != nil {
		t.Fatalf("create namespace with grants: %v", err)
	}

	wantHousekeepingGrants(t, s, ns, grants)
	wantNamespaceEntries(t, s, ns, grants)
}

func testNamespaceHousekeepingGrantFails(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants, entries := housekeepingNamespaceGrants(ns)

	// The last grant repeats the ID of the first: its insert fails.
	grants[len(grants)-1].ID = grants[0].ID

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry(), entries); err == nil {
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
	grants, entries := housekeepingNamespaceGrants(ns)

	if err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry(), entries); err != nil {
		t.Fatalf("create namespace with grants: %v", err)
	}

	again, againEntries := housekeepingNamespaceGrants(ns)
	for i := range again {
		again[i].ID += "-again"
	}

	err := s.CreateNamespaceWithGrants(t.Context(), ns, again, entry(), againEntries)
	if !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create taken namespace = %v, want namespace.ErrAlreadyExists", err)
	}

	wantHousekeepingGrants(t, s, ns, grants)
}

func testNamespaceHousekeepingLength(t *testing.T, s access.Store) {
	t.Helper()

	ns := newNamespace("acme")
	grants, entries := housekeepingNamespaceGrants(ns)

	err := s.CreateNamespaceWithGrants(t.Context(), ns, grants, entry(), entries[:len(entries)-1])
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("create with a missing grant entry = %v, want ErrInvalid", err)
	}

	if nss, ok := s.(namespace.Store); ok {
		if _, getErr := nss.GetNamespace(t.Context(), "acme"); !errors.Is(getErr, namespace.ErrNotFound) {
			t.Fatalf("namespace after a refused create = %v, want namespace.ErrNotFound", getErr)
		}
	}
}
