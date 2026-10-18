package notifytest

import (
	"context"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ContactsHarness is one fresh, empty identity store for one case of
// RunContacts.
type ContactsHarness struct {
	Contacts notify.Contacts
	People   People
}

// People makes the principals, groups and grants that the cases of
// RunContacts read, through the ports of the identity store.
type People interface {
	// AddPerson creates the enabled principal id with the contacts cs.
	AddPerson(ctx context.Context, id string, cs []notify.Contact) error
	// Disable disables the principal id.
	Disable(ctx context.Context, id string) error
	// AddGroup creates the group name with the members ids.
	AddGroup(ctx context.Context, name string, ids []string) error
	// Grant gives the principal id the action on scope.
	Grant(ctx context.Context, id, action string, scope namespace.Name) error
}

// ContactsFactory returns a fresh harness for one case of RunContacts.
type ContactsFactory func(t *testing.T) ContactsHarness

// RunContacts executes the conformance suite of notify.Contacts against
// harnesses from newHarness. The identity package runs it with its store.
func RunContacts(t *testing.T, newHarness ContactsFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h ContactsHarness)
	}{
		{"contacts of a person, and none for a disabled or unknown person", testContactsOf},
		{"enabled members of a group, and none for an unknown group", testMembers},
		{"enabled administrators hold the action to manage the notifications of the scope", testAdmins},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// email returns the email contact address.
func email(address string) notify.Contact {
	return notify.Contact{Kind: notify.SinkEmail, Address: address}
}

// chat returns the chat contact id.
func chat(id string) notify.Contact {
	return notify.Contact{Kind: notify.SinkChat, Address: id}
}

// addPeople creates alice with an email address and a chat identity, bob
// with an email address, and carol with no contact.
func addPeople(t *testing.T, h ContactsHarness) {
	t.Helper()

	people := []struct {
		id string
		cs []notify.Contact
	}{
		{"alice", []notify.Contact{email("alice@example.com"), chat("U-ALICE")}},
		{"bob", []notify.Contact{email("bob@example.com")}},
		{"carol", nil},
	}

	for _, p := range people {
		if err := h.People.AddPerson(t.Context(), p.id, p.cs); err != nil {
			t.Fatalf("AddPerson %s: %v", p.id, err)
		}
	}
}

func testContactsOf(t *testing.T, h ContactsHarness) {
	t.Helper()

	addPeople(t, h)

	if err := h.People.Disable(t.Context(), "bob"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	cases := []struct {
		id   string
		want []notify.Contact
	}{
		{"alice", []notify.Contact{email("alice@example.com"), chat("U-ALICE")}},
		{"bob", nil},
		{"carol", nil},
		{"nobody", nil},
	}

	for _, tc := range cases {
		got, err := h.Contacts.ContactsOf(t.Context(), tc.id)
		if err != nil {
			t.Fatalf("ContactsOf %s: %v", tc.id, err)
		}

		if !sameContacts(got, tc.want) {
			t.Fatalf("ContactsOf %s = %+v, want %+v", tc.id, got, tc.want)
		}
	}
}

// sameContacts reports whether a and b hold the same contacts, in any
// order.
func sameContacts(a, b []notify.Contact) bool {
	key := func(c notify.Contact) string { return string(c.Kind) + "\x00" + c.Address }

	ka, kb := make([]string, 0, len(a)), make([]string, 0, len(b))
	for _, c := range a {
		ka = append(ka, key(c))
	}

	for _, c := range b {
		kb = append(kb, key(c))
	}

	slices.Sort(ka)
	slices.Sort(kb)

	return slices.Equal(ka, kb)
}

func testMembers(t *testing.T, h ContactsHarness) {
	t.Helper()

	addPeople(t, h)

	for name, ids := range map[string][]string{"network": {"alice", "bob"}, "empty": nil} {
		if err := h.People.AddGroup(t.Context(), name, ids); err != nil {
			t.Fatalf("AddGroup %s: %v", name, err)
		}
	}

	if err := h.People.AddPerson(t.Context(), "erin", nil); err != nil {
		t.Fatalf("AddPerson erin: %v", err)
	}

	if err := h.People.AddGroup(t.Context(), "with-disabled", []string{"alice", "erin"}); err != nil {
		t.Fatalf("AddGroup with-disabled: %v", err)
	}

	if err := h.People.Disable(t.Context(), "erin"); err != nil {
		t.Fatalf("Disable erin: %v", err)
	}

	cases := []struct {
		group string
		want  []string
	}{
		{"network", []string{"alice", "bob"}},
		{"with-disabled", []string{"alice"}},
		{"empty", nil},
		{"unknown", nil},
	}

	for _, tc := range cases {
		got, err := h.Contacts.Members(t.Context(), tc.group)
		if err != nil {
			t.Fatalf("Members %s: %v", tc.group, err)
		}

		if !sameIDs(got, tc.want) {
			t.Fatalf("Members %s = %v, want %v", tc.group, got, tc.want)
		}
	}
}

// sameIDs reports whether a and b hold the same IDs, in any order.
func sameIDs(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(a, b)
}

func testAdmins(t *testing.T, h ContactsHarness) {
	t.Helper()

	addPeople(t, h)

	for _, id := range []string{"dave", "erin"} {
		if err := h.People.AddPerson(t.Context(), id, nil); err != nil {
			t.Fatalf("AddPerson %s: %v", id, err)
		}
	}

	grants := []struct {
		id, action string
		scope      namespace.Name
	}{
		{"alice", notify.ActionManage, shop},
		{"bob", notify.ActionRead, shop},
		{"bob", notify.ActionManage, other},
		{"carol", notify.ActionManageServer, audit.ServerScope},
		{"dave", notify.ActionManage, shop},
		{"erin", notify.ActionManage, shop},
	}

	for _, g := range grants {
		if err := h.People.Grant(t.Context(), g.id, g.action, g.scope); err != nil {
			t.Fatalf("Grant %s %s on %s: %v", g.id, g.action, g.scope, err)
		}
	}

	// erin holds the action on shop, but a disabled principal is not an
	// administrator.
	if err := h.People.Disable(t.Context(), "erin"); err != nil {
		t.Fatalf("Disable erin: %v", err)
	}

	cases := []struct {
		scope namespace.Name
		want  []string
	}{
		{shop, []string{"alice", "dave"}},
		{other, []string{"bob"}},
		{audit.ServerScope, []string{"carol"}},
		{"empty", nil},
	}

	for _, tc := range cases {
		got, err := h.Contacts.Admins(t.Context(), tc.scope)
		if err != nil {
			t.Fatalf("Admins of %s: %v", tc.scope, err)
		}

		if !sameIDs(got, tc.want) {
			t.Fatalf("Admins of %s = %v, want %v", tc.scope, got, tc.want)
		}
	}
}
