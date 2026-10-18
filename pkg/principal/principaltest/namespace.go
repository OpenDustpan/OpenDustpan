package principaltest

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// NamespaceStore is what the suite of the principals of a namespace needs:
// the principal port, the namespaces whose fences the writes check, and the
// audit log that the writes append to.
type NamespaceStore interface {
	principal.Store
	namespace.Store
	audit.Store
}

// NamespaceFactory returns a fresh, empty store for one subtest.
type NamespaceFactory func(t *testing.T) NamespaceStore

// RunNamespace executes the conformance suite of principal.NamespaceRecordStore
// against stores produced by newStore.
func RunNamespace(t *testing.T, newStore NamespaceFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s NamespaceStore)
		name string
	}{
		{name: "a principal of a namespace round trips", test: testNamespacePrincipalRoundTrip},
		{name: "a write audits in the namespace and checks its fence", test: testNamespacePrincipalFence},
		{name: "a subject is unique on the whole installation", test: testNamespaceSubjectUnique},
		{name: "the writes of the installation do not act on it", test: testInstallationWritesRefuse},
		{name: "update and delete act in its own namespace only", test: testNamespacePrincipalUpdateDelete},
		{name: "a principal of a namespace cannot join a group", test: testNamespacePrincipalNoGroup},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			for _, ns := range []namespace.Name{"acme", "beta"} {
				mustCreateNamespace(t, s, ns)
			}

			tc.test(t, s)
		})
	}
}

// mustCreateNamespace stores the namespace ns with the failover version 1.
func mustCreateNamespace(t *testing.T, s NamespaceStore, ns namespace.Name) {
	t.Helper()

	err := s.CreateNamespace(t.Context(), namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: time.Unix(0, 0), Name: ns, HomeRegion: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace %q: %v", ns, err)
	}
}

// fenceOf returns the fence of the namespace ns with the failover version 1.
func fenceOf(ns namespace.Name) namespace.Fence {
	return namespace.Fence{Namespace: ns, FailoverVersion: 1}
}

// nsEntry returns entry() in the audit log of the namespace ns.
func nsEntry(ns namespace.Name) audit.Entry {
	e := entry()
	e.Namespace = ns

	return e
}

// nsPrincipal returns a service principal of the namespace ns.
func nsPrincipal(id, subject string, ns namespace.Name) principal.Principal {
	p := newPrincipal(id, subject, principal.KindService)
	p.Namespace = ns

	return p
}

// mustCreateNamespacePrincipal stores p in its namespace.
func mustCreateNamespacePrincipal(t *testing.T, s NamespaceStore, p principal.Principal) {
	t.Helper()

	if err := s.CreateNamespacePrincipal(t.Context(), p, fenceOf(p.Namespace), nsEntry(p.Namespace)); err != nil {
		t.Fatalf("create principal %q in %q: %v", p.Subject, p.Namespace, err)
	}
}

func testNamespacePrincipalRoundTrip(t *testing.T, s NamespaceStore) {
	t.Helper()

	ctx := t.Context()
	p := nsPrincipal("prn-1", "ci-deploy", "acme")
	mustCreateNamespacePrincipal(t, s, p)
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "alice", principal.KindPerson))

	for _, read := range []func() (principal.Principal, error){
		func() (principal.Principal, error) { return s.GetPrincipal(ctx, "prn-1") },
		func() (principal.Principal, error) { return s.GetPrincipalBySubject(ctx, "ci-deploy") },
	} {
		got, err := read()
		if err != nil || got != p {
			t.Fatalf("read principal = %+v, %v; want %+v", got, err, p)
		}
	}

	checkNamespaceLists(t, s, p)
}

// checkNamespaceLists checks that p, a principal of acme, is in the list of
// acme and of the installation, and not in the list of beta.
func checkNamespaceLists(t *testing.T, s NamespaceStore, p principal.Principal) {
	t.Helper()

	ctx := t.Context()

	rows, total, err := s.ListNamespacePrincipals(ctx, "acme", all())
	if err != nil || total != 1 || len(rows) != 1 || rows[0] != p {
		t.Fatalf("principals of acme = %+v, %d, %v; want only %q", rows, total, err, p.ID)
	}

	rows, total, err = s.ListNamespacePrincipals(ctx, "beta", all())
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("principals of beta = %+v, %d, %v; want none", rows, total, err)
	}

	rows, total, err = s.ListPrincipals(ctx, all(), "")
	if err != nil || total != 2 || !slices.Contains(rows, p) {
		t.Fatalf("principals of the installation = %+v, %d, %v; want both, with the namespace", rows, total, err)
	}
}

func testNamespacePrincipalFence(t *testing.T, s NamespaceStore) {
	t.Helper()

	ctx := t.Context()
	p := nsPrincipal("prn-1", "ci-deploy", "acme")

	stale := namespace.Fence{Namespace: "acme", FailoverVersion: 2}
	wantErr(t, "create with a stale fence", s.CreateNamespacePrincipal(ctx, p, stale, nsEntry("acme")),
		namespace.ErrFenced)
	wantErr(t, "create with the fence of another namespace",
		s.CreateNamespacePrincipal(ctx, p, fenceOf("beta"), nsEntry("beta")), principal.ErrInvalid)
	wantErr(t, "create with an entry of another namespace",
		s.CreateNamespacePrincipal(ctx, p, fenceOf("acme"), nsEntry("beta")), audit.ErrInvalidEntry)

	if _, err := s.GetPrincipal(ctx, "prn-1"); err == nil {
		t.Fatal("a refused write stored the principal")
	}

	mustCreateNamespacePrincipal(t, s, p)

	listing, err := s.List(ctx, "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil || len(listing.Entries) != 1 || listing.Entries[0].Object != entry().Object {
		t.Fatalf("audit of acme = %+v, %v; want the entry of the create", listing.Entries, err)
	}

	server, err := s.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil || server.Total != 0 {
		t.Fatalf("audit of the installation = %d entries, %v; want none", server.Total, err)
	}
}

func testNamespaceSubjectUnique(t *testing.T, s NamespaceStore) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "ci-deploy", principal.KindService))

	err := s.CreateNamespacePrincipal(t.Context(), nsPrincipal("prn-2", "ci-deploy", "acme"), fenceOf("acme"),
		nsEntry("acme"))
	wantErr(t, "a subject of the installation in a namespace", err, principal.ErrAlreadyExists)

	mustCreateNamespacePrincipal(t, s, nsPrincipal("prn-3", "ci-other", "acme"))

	err = s.CreateNamespacePrincipal(t.Context(), nsPrincipal("prn-4", "ci-other", "beta"), fenceOf("beta"),
		nsEntry("beta"))
	wantErr(t, "a subject of one namespace in another", err, principal.ErrAlreadyExists)
}

func testInstallationWritesRefuse(t *testing.T, s NamespaceStore) {
	t.Helper()

	ctx := t.Context()
	p := nsPrincipal("prn-1", "ci-deploy", "acme")

	wantErr(t, "create through the installation", s.CreatePrincipal(ctx, p, entry()), principal.ErrInvalid)

	mustCreateNamespacePrincipal(t, s, p)

	p.DisplayName = "Renamed"
	wantErr(t, "update through the installation", s.UpdatePrincipal(ctx, p, entry()), principal.ErrNotFound)
	wantErr(t, "delete through the installation", s.DeletePrincipal(ctx, "prn-1", entry()), principal.ErrNotFound)

	if got, err := s.GetPrincipal(ctx, "prn-1"); err != nil || got.DisplayName != "Name of ci-deploy" {
		t.Fatalf("principal after refused writes = %+v, %v; want it unchanged", got, err)
	}
}

func testNamespacePrincipalUpdateDelete(t *testing.T, s NamespaceStore) {
	t.Helper()

	ctx := t.Context()
	p := nsPrincipal("prn-1", "ci-deploy", "acme")
	mustCreateNamespacePrincipal(t, s, p)

	other := p
	other.Namespace = "beta"
	other.DisplayName = "Moved"

	wantErr(t, "update in another namespace",
		s.UpdateNamespacePrincipal(ctx, other, fenceOf("beta"), nsEntry("beta")), principal.ErrNotFound)
	wantErr(t, "delete in another namespace",
		s.DeleteNamespacePrincipal(ctx, "prn-1", fenceOf("beta"), nsEntry("beta")), principal.ErrNotFound)

	p.DisplayName = "Deploy bot"
	p.UpdatedAt = at(5)

	if err := s.UpdateNamespacePrincipal(ctx, p, fenceOf("acme"), nsEntry("acme")); err != nil {
		t.Fatalf("update: %v", err)
	}

	if got, err := s.GetPrincipal(ctx, "prn-1"); err != nil || got != p {
		t.Fatalf("principal after update = %+v, %v; want %+v", got, err, p)
	}

	if err := s.DeleteNamespacePrincipal(ctx, "prn-1", fenceOf("acme"), nsEntry("acme")); err != nil {
		t.Fatalf("delete: %v", err)
	}

	wantErr(t, "read after delete", func() error { _, err := s.GetPrincipal(ctx, "prn-1"); return err }(),
		principal.ErrNotFound)
}

func testNamespacePrincipalNoGroup(t *testing.T, s NamespaceStore) {
	t.Helper()

	mustCreateNamespacePrincipal(t, s, nsPrincipal("prn-1", "ci-deploy", "acme"))
	mustCreateGroup(t, s, newGroup("grp-1", "ops"))

	m := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: "grp-1", PrincipalID: "prn-1",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	wantErr(
		t,
		"membership of a principal of a namespace",
		s.AddMember(t.Context(), m, entry(), nil),
		principal.ErrInvalid,
	)
}
