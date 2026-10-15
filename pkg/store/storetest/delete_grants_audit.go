package storetest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// scopeEntries lists every audit entry of the scope ns, oldest last (the
// store lists newest first).
func scopeEntries(t *testing.T, d *delegation, ns namespace.Name) []audit.Entry {
	t.Helper()

	listing, err := d.st.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries of %q: %v", ns, err)
	}

	return listing.Entries
}

// grantEntries returns the entries of entries with the record object.
func grantEntries(entries []audit.Entry, object string) []audit.Entry {
	var out []audit.Entry

	for i := range entries {
		if entries[i].Object == object {
			out = append(out, entries[i])
		}
	}

	return out
}

// revokeCheck is one expected namespace entry of a delete: the grant in the
// namespace ns gained one entry since before, which names the delete
// endedBy.
type revokeCheck struct {
	d       *delegation
	ns      namespace.Name
	grant   access.Grant
	before  []audit.Entry
	endedBy string
}

// check proves the entry of the check: one more entry of the grant than
// before, with the manage-access action of a manual revoke, the record of
// the grant, and the changes that name the grant and the delete. It returns
// the new entry.
func (c revokeCheck) check(t *testing.T) audit.Entry {
	t.Helper()

	g := c.grant
	after := scopeEntries(t, c.d, c.ns)
	old := grantEntries(c.before, access.GrantRef(g))
	now := grantEntries(after, access.GrantRef(g))

	if len(now) != len(old)+1 {
		t.Fatalf(
			"entries of %s in %q = %d, want one more than the %d before",
			access.GrantRef(g),
			c.ns,
			len(now),
			len(old),
		)
	}

	var got audit.Entry

	seen := make(map[int64]bool, len(old))
	for i := range old {
		seen[old[i].Sequence] = true
	}

	for i := range now {
		if !seen[now[i].Sequence] {
			got = now[i]
		}
	}

	if got.Action != string(identity.ActionNamespaceManageAccess) {
		t.Fatalf("action of the revoke entry = %q, want %q", got.Action, identity.ActionNamespaceManageAccess)
	}

	if got.Namespace != c.ns || got.Kind != audit.KindChange || got.Outcome != audit.OutcomeSuccess {
		t.Fatalf("revoke entry = %+v, want a change in %q with a success outcome", got, c.ns)
	}

	want := map[string]string{
		"grant":   g.ID,
		"subject": g.Subject.String(),
		"role":    g.Role,
		"object":  g.Object.String(),
		"endedBy": c.endedBy,
	}

	changes := make(map[string]string, len(got.Changes))
	for _, change := range got.Changes {
		changes[change.Key] = change.New
	}

	for key, value := range want {
		if changes[key] != value {
			t.Fatalf("change %q of the revoke entry = %q, want %q (changes %+v)", key, changes[key], value, got.Changes)
		}
	}

	return got
}

// checkServerDeleteAlone checks that the delete with the record object is the
// only new entry of @server since before: a grant on the installation takes
// no namespace entry and no extra installation entry.
func checkServerDeleteAlone(t *testing.T, d *delegation, object string, before []audit.Entry) audit.Entry {
	t.Helper()

	after := scopeEntries(t, d, audit.ServerScope)

	if len(after) != len(before)+1 {
		t.Fatalf("entries of @server = %d, want one more than the %d before", len(after), len(before))
	}

	seen := make(map[int64]bool, len(before))
	for i := range before {
		seen[before[i].Sequence] = true
	}

	var fresh []audit.Entry

	for i := range after {
		if !seen[after[i].Sequence] {
			fresh = append(fresh, after[i])
		}
	}

	if len(fresh) != 1 || fresh[0].Object != object {
		t.Fatalf("new entries of @server = %+v, want only the delete of %q", fresh, object)
	}

	return fresh[0]
}

// testDeletePrincipalGrantsAudit deletes a principal that holds grants in two
// namespaces and one grant on the installation: each namespace chain gains
// one entry that names its grant and the delete, and @server gains only the
// entry of the delete.
func testDeletePrincipalGrantsAudit(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	d.namespaceNamed(t, "beta")
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)

	gAcme := d.mustGrant(t, bootstrap(), alice, access.RoleViewer, identity.NamespaceObject("acme"))
	gBeta := d.mustGrant(t, bootstrap(), alice, access.RoleViewer, identity.NamespaceObject("beta"))
	d.mustGrant(t, bootstrap(), alice, access.RoleViewer, identity.ServerObject())

	beforeAcme := scopeEntries(t, d, "acme")
	beforeBeta := scopeEntries(t, d, "beta")
	beforeServer := scopeEntries(t, d, audit.ServerScope)

	err := d.principals.DeletePrincipal(
		ctx, changeEntry(bootstrap(), identity.ActionServerManagePrincipals), bootstrap(), alice.ID,
	)
	if err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	grants, err := d.st.GrantsOf(ctx, access.Subject{Kind: access.SubjectPrincipal, ID: alice.ID})
	if err != nil || len(grants) != 0 {
		t.Fatalf("grants of a deleted principal = %v, %v; want none", grants, err)
	}

	endedBy := "principal delete " + alice.ID
	revokeAcme := revokeCheck{d: d, ns: "acme", grant: gAcme, before: beforeAcme, endedBy: endedBy}.check(t)
	revokeBeta := revokeCheck{d: d, ns: "beta", grant: gBeta, before: beforeBeta, endedBy: endedBy}.check(t)
	deleteEntry := checkServerDeleteAlone(t, d, principal.Ref(alice.ID), beforeServer)

	if !revokeAcme.At.Equal(deleteEntry.At) || !revokeBeta.At.Equal(deleteEntry.At) {
		t.Fatalf(
			"revoke entries at %v and %v, want the delete time %v",
			revokeAcme.At,
			revokeBeta.At,
			deleteEntry.At,
		)
	}
}

// groupGrant gives role on obj to the group with the ID id, for the
// bootstrap admin.
func groupGrant(t *testing.T, d *delegation, id, role string, obj identity.Object) access.Grant {
	t.Helper()

	g, err := d.access.Grant(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageAccess), bootstrap(),
		access.GrantInput{
			Object: obj, Role: role, Subject: access.Subject{Kind: access.SubjectGroup, ID: id},
		})
	if err != nil {
		t.Fatalf("grant %s on %q to group %s: %v", role, obj.String(), id, err)
	}

	return g
}

// testDeleteGroupGrantsAudit deletes a group that holds grants in two
// namespaces and one grant on the installation: each namespace chain gains
// one entry that names its grant and the delete, and @server gains only the
// entry of the delete.
func testDeleteGroupGrantsAudit(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	d.namespaceNamed(t, "beta")

	group, err := d.principals.CreateGroup(ctx, changeEntry(bootstrap(), identity.ActionServerManageGroups),
		principal.GroupInput{Name: "ops", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	gAcme := groupGrant(t, d, group.ID, access.RoleViewer, identity.NamespaceObject("acme"))
	gBeta := groupGrant(t, d, group.ID, access.RoleViewer, identity.NamespaceObject("beta"))
	groupGrant(t, d, group.ID, access.RoleViewer, identity.ServerObject())

	beforeAcme := scopeEntries(t, d, "acme")
	beforeBeta := scopeEntries(t, d, "beta")
	beforeServer := scopeEntries(t, d, audit.ServerScope)

	err = d.principals.DeleteGroup(
		ctx, changeEntry(bootstrap(), identity.ActionServerManageGroups), bootstrap(), group.ID,
	)
	if err != nil {
		t.Fatalf("delete group: %v", err)
	}

	grants, err := d.st.GrantsOf(ctx, access.Subject{Kind: access.SubjectGroup, ID: group.ID})
	if err != nil || len(grants) != 0 {
		t.Fatalf("grants of a deleted group = %v, %v; want none", grants, err)
	}

	endedBy := "group delete " + group.ID
	revokeCheck{d: d, ns: "acme", grant: gAcme, before: beforeAcme, endedBy: endedBy}.check(t)
	revokeCheck{d: d, ns: "beta", grant: gBeta, before: beforeBeta, endedBy: endedBy}.check(t)
	checkServerDeleteAlone(t, d, principal.GroupRef(group.ID), beforeServer)
}
