package storetest

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// IPAllowlistStore is every port the IP allowlist suite exercises: namespaces
// for fixtures, ipallow.Store under test, audit.Store for the chain contract,
// and the portable export with stage and apply for the backup round trip.
type IPAllowlistStore interface {
	namespace.Store
	ipallow.Store
	audit.Store
	backup.PortableExporter
	backup.PortableStager
	backup.Applier
}

// IPAllowlistFactory returns a fresh, empty store for one subtest.
type IPAllowlistFactory func(t *testing.T) IPAllowlistStore

// RunIPAllowlist executes the conformance suite of the IP allowlist port
// (ipallow.Store) against stores produced by newStore.
func RunIPAllowlist(t *testing.T, newStore IPAllowlistFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s IPAllowlistStore)
		name string
	}{
		{name: "a new namespace has no rule", test: testIPAllowlistNewNamespaceEmpty},
		{name: "set then get round trips IPv4 and IPv6", test: testIPAllowlistRoundTrip},
		{name: "set replaces the list", test: testIPAllowlistReplace},
		{name: "the zero list removes the rule", test: testIPAllowlistZeroRemoves},
		{name: "no namespace has a list", test: testIPAllowlistNamespacesWithListEmpty},
		{name: "two namespaces with a list and one without", test: testIPAllowlistNamespacesWithListOrder},
		{name: "a removed list leaves no namespace", test: testIPAllowlistNamespacesWithListRemoved},
		{name: "a namespace without a list sorts first", test: testIPAllowlistNamespacesWithListSkipsEmpty},
		{name: "an unknown namespace is ErrNotFound", test: testIPAllowlistUnknownNamespace},
		{name: "a stale fence is ErrFenced and writes nothing", test: testIPAllowlistStaleFence},
		{name: "the audit entry joins the chain of the namespace", test: testIPAllowlistAuditEntry},
		{name: "an invalid entry changes nothing", test: testIPAllowlistInvalidEntry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}

	t.Run("a backup export and import carry the list", func(t *testing.T) {
		t.Parallel()
		testIPAllowlistBackupRoundTrip(t, newStore)
	})
}

// ipAllowlistEntry returns the audit entry the suite passes for a list change
// in ns: a change at the fixed time, which the store appends in the write
// transaction.
func ipAllowlistEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:            now(),
		Namespace:     ns,
		Subject:       "alice@example.com",
		IDPSubject:    "",
		ObjectID:      "",
		TokenID:       "",
		Action:        "namespace:set-ip-allowlist",
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "storetest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// mustSetIPAllowlist parses items and stores them for ns with the fence of
// ns, failing the test on error.
func mustSetIPAllowlist(
	t *testing.T, s IPAllowlistStore, ns namespace.Namespace, items []string, e audit.Entry,
) ipallow.List {
	t.Helper()

	l, err := ipallow.ParseList(items)
	if err != nil {
		t.Fatalf("parse list %q: %v", items, err)
	}

	if err := s.SetNamespaceList(t.Context(), ns.Name, l, ns.Fence(), e); err != nil {
		t.Fatalf("set list %q: %v", items, err)
	}

	return l
}

// auditTotal returns the entry count of the chain of ns.
func auditTotal(t *testing.T, s IPAllowlistStore, ns namespace.Name) int64 {
	t.Helper()

	listing, err := s.List(t.Context(), ns, audit.Page{Number: 1, Size: 1})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return listing.Total
}

// testIPAllowlistNewNamespaceEmpty checks a new namespace holds the zero List.
func testIPAllowlistNewNamespaceEmpty(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	got, err := s.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if !got.IsEmpty() {
		t.Fatalf("list of a new namespace = %v, want no rule", got.Strings())
	}
}

// testIPAllowlistRoundTrip checks a set list reads back with IPv4 and IPv6
// entries.
func testIPAllowlistRoundTrip(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	want := mustSetIPAllowlist(t, s, ns, []string{"10.0.0.0/8", "2001:db8::/32"}, ipAllowlistEntry(ns.Name))

	got, err := s.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if got.Encode() != want.Encode() {
		t.Fatalf("list = %q, want %q", got.Encode(), want.Encode())
	}
}

// testIPAllowlistReplace checks a second set replaces the first list.
func testIPAllowlistReplace(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustSetIPAllowlist(t, s, ns, []string{"10.0.0.0/8"}, ipAllowlistEntry(ns.Name))
	want := mustSetIPAllowlist(t, s, ns, []string{"192.0.2.0/24"}, ipAllowlistEntry(ns.Name))

	got, err := s.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if got.Encode() != want.Encode() {
		t.Fatalf("list = %q, want %q", got.Encode(), want.Encode())
	}
}

// testIPAllowlistZeroRemoves checks the zero List removes the rule.
func testIPAllowlistZeroRemoves(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustSetIPAllowlist(t, s, ns, []string{"10.0.0.0/8"}, ipAllowlistEntry(ns.Name))

	entry := ipAllowlistEntry(ns.Name)

	if err := s.SetNamespaceList(t.Context(), ns.Name, ipallow.List{}, ns.Fence(), entry); err != nil {
		t.Fatalf("remove the rule: %v", err)
	}

	got, err := s.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if !got.IsEmpty() {
		t.Fatalf("list after a remove = %v, want no rule", got.Strings())
	}
}

// testIPAllowlistNamespacesWithListEmpty checks a store with no list answers
// an empty result.
func testIPAllowlistNamespacesWithListEmpty(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	newNamespace(t, s, "acme")

	got, err := s.NamespacesWithList(t.Context())
	if err != nil {
		t.Fatalf("list namespaces with a list: %v", err)
	}

	if got == nil || len(got) != 0 {
		t.Fatalf("namespaces with a list = %v, want an empty slice", got)
	}
}

// testIPAllowlistNamespacesWithListOrder checks the result holds only the
// namespaces with a list, in name order.
func testIPAllowlistNamespacesWithListOrder(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	beta := newNamespace(t, s, "beta")
	newNamespace(t, s, "gamma")
	acme := newNamespace(t, s, "acme")

	mustSetIPAllowlist(t, s, beta, []string{"10.0.0.0/8"}, ipAllowlistEntry(beta.Name))
	mustSetIPAllowlist(t, s, acme, []string{"192.0.2.0/24"}, ipAllowlistEntry(acme.Name))

	got, err := s.NamespacesWithList(t.Context())
	if err != nil {
		t.Fatalf("list namespaces with a list: %v", err)
	}

	want := []namespace.Name{"acme", "beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("namespaces with a list = %v, want %v", got, want)
	}
}

// testIPAllowlistNamespacesWithListRemoved checks a namespace that lost its
// list is not in the result.
func testIPAllowlistNamespacesWithListRemoved(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	acme := newNamespace(t, s, "acme")
	mustSetIPAllowlist(t, s, acme, []string{"10.0.0.0/8"}, ipAllowlistEntry(acme.Name))

	err := s.SetNamespaceList(t.Context(), acme.Name, ipallow.List{}, acme.Fence(), ipAllowlistEntry(acme.Name))
	if err != nil {
		t.Fatalf("remove the rule: %v", err)
	}

	got, err := s.NamespacesWithList(t.Context())
	if err != nil {
		t.Fatalf("list namespaces with a list: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("namespaces with a list = %v, want none", got)
	}
}

// testIPAllowlistNamespacesWithListSkipsEmpty checks a namespace whose name
// sorts first and whose list is empty is not in the result.
func testIPAllowlistNamespacesWithListSkipsEmpty(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	first := newNamespace(t, s, "aardvark")
	err := s.SetNamespaceList(t.Context(), first.Name, ipallow.List{}, first.Fence(), ipAllowlistEntry(first.Name))
	if err != nil {
		t.Fatalf("set the empty list: %v", err)
	}

	beta := newNamespace(t, s, "beta")
	mustSetIPAllowlist(t, s, beta, []string{"10.0.0.0/8"}, ipAllowlistEntry(beta.Name))

	got, err := s.NamespacesWithList(t.Context())
	if err != nil {
		t.Fatalf("list namespaces with a list: %v", err)
	}

	want := []namespace.Name{"beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("namespaces with a list = %v, want %v", got, want)
	}
}

// testIPAllowlistUnknownNamespace checks both methods report ErrNotFound for
// an unknown namespace.
func testIPAllowlistUnknownNamespace(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	if _, err := s.NamespaceList(t.Context(), "ghost"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get the list of an unknown namespace: err = %v, want ErrNotFound", err)
	}

	ghost := namespace.Fence{Namespace: "ghost", FailoverVersion: 1}

	err := s.SetNamespaceList(t.Context(), "ghost", ipallow.List{}, ghost, ipAllowlistEntry("ghost"))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("set the list of an unknown namespace: err = %v, want ErrNotFound", err)
	}
}

// testIPAllowlistStaleFence checks a write with a stale fence is refused, and
// that the list is unchanged and no audit entry is appended.
func testIPAllowlistStaleFence(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustSetIPAllowlist(t, s, ns, []string{"10.0.0.0/8"}, ipAllowlistEntry(ns.Name))
	before := auditTotal(t, s, ns.Name)

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	next, err := ipallow.ParseList([]string{"192.0.2.0/24"})
	if err != nil {
		t.Fatalf("parse list: %v", err)
	}

	setErr := s.SetNamespaceList(t.Context(), ns.Name, next, stale, ipAllowlistEntry(ns.Name))
	if !errors.Is(setErr, namespace.ErrFenced) {
		t.Fatalf("set with a stale fence: err = %v, want ErrFenced", setErr)
	}

	got, err := s.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if got.Encode() != "10.0.0.0/8" {
		t.Fatalf("list after a refused write = %q, want the stored list", got.Encode())
	}

	if total := auditTotal(t, s, ns.Name); total != before {
		t.Fatalf("audit entries after a refused write = %d, want %d", total, before)
	}
}

// testIPAllowlistAuditEntry checks the entry of a list change appears in the
// chain of the namespace with the action and the stamped time.
func testIPAllowlistAuditEntry(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	want := ipAllowlistEntry(ns.Name)
	mustSetIPAllowlist(t, s, ns, []string{"10.0.0.0/8"}, want)

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if len(listing.Entries) != 1 {
		t.Fatalf("audit entries = %+v, want the entry of the change", listing.Entries)
	}

	got := listing.Entries[0]
	if got.Kind != audit.KindChange || got.Object != want.Object || got.Action != want.Action ||
		!got.At.Equal(want.At) || got.Sequence != 1 {
		t.Fatalf("audit entry = %+v, want %+v as sequence 1", got, want)
	}
}

// testIPAllowlistInvalidEntry checks an entry that fails validation and an
// entry of another namespace each fail with audit.ErrInvalidEntry, and the
// list is unchanged.
func testIPAllowlistInvalidEntry(t *testing.T, s IPAllowlistStore) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	mustSetIPAllowlist(t, s, ns, []string{"10.0.0.0/8"}, ipAllowlistEntry(ns.Name))

	invalid := ipAllowlistEntry(ns.Name)
	invalid.Subject = ""

	elsewhere := ipAllowlistEntry("other")

	next, parseErr := ipallow.ParseList([]string{"192.0.2.0/24"})
	if parseErr != nil {
		t.Fatalf("parse list: %v", parseErr)
	}

	refused := []audit.Entry{invalid, elsewhere}
	for i := range refused {
		e := &refused[i]

		err := s.SetNamespaceList(t.Context(), ns.Name, next, ns.Fence(), *e)
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("set with entry %+v: err = %v, want ErrInvalidEntry", e, err)
		}
	}

	got, err := s.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}

	if got.Encode() != "10.0.0.0/8" {
		t.Fatalf("list after a refused write = %q, want the stored list", got.Encode())
	}
}

// testIPAllowlistBackupRoundTrip exports a store whose namespace holds a
// list, checks the namespaces file carries it, and stages and applies the
// export into a fresh store: the restored store holds the same list.
func testIPAllowlistBackupRoundTrip(t *testing.T, newStore IPAllowlistFactory) {
	t.Helper()

	src := newStore(t)
	ns := newNamespace(t, src, "acme")
	want := mustSetIPAllowlist(t, src, ns, []string{"10.0.0.0/8", "2001:db8::/32"}, ipAllowlistEntry(ns.Name))

	sink, sum, files := exportPortable(t, ipAllowlistPortable(t, src))

	namespaces := readPortableLines(
		t,
		backup.PortablePath("namespaces"),
		sink.files[backup.PortablePath("namespaces")].Bytes(),
	)
	acme := findPortableRow(t, namespaces, "name", ns.Name.String())

	raw, isText := acme["ip_allowlist"].(string)
	if !isText || raw != want.Encode() {
		t.Fatalf("ip_allowlist = %v, want %q for the namespace with a list", acme["ip_allowlist"], want.Encode())
	}

	dst := newStore(t)
	staged := ipAllowlistPortable(t, dst)

	if _, err := staged.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, staged)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := staged.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	got, err := dst.NamespaceList(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get the restored list: %v", err)
	}

	if got.Encode() != want.Encode() {
		t.Fatalf("restored list = %q, want %q", got.Encode(), want.Encode())
	}
}

// ipAllowlistPortable returns s as a portable restore store for the backup
// round trip, which uses the shared export helpers.
func ipAllowlistPortable( //nolint:ireturn // The shared export helpers take the interface.
	t *testing.T, s IPAllowlistStore,
) PortableRestoreStore {
	t.Helper()

	ps, ok := any(s).(PortableRestoreStore)
	if !ok {
		t.Fatal("the store does not export the portable format")
	}

	return ps
}
