package accesstest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fenceOf returns the fence of a grant write in ns: the zero fence for the
// installation (ns ""), and the fence of ns otherwise. A grant inside a
// namespace checks the fence of its namespace, so the store must keep
// namespaces. fenceOf makes ns when it does not exist.
func fenceOf(t *testing.T, s access.Store, ns namespace.Name) namespace.Fence {
	t.Helper()

	if ns == "" {
		return namespace.Fence{Namespace: "", FailoverVersion: 0}
	}

	nss, ok := s.(namespace.Store)
	if !ok {
		t.Fatal("the store keeps no namespaces: a grant inside a namespace checks the fence of its namespace")
	}

	got, err := nss.GetNamespace(t.Context(), ns)
	if errors.Is(err, namespace.ErrNotFound) {
		got = newNamespace(ns)
		err = nss.CreateNamespace(t.Context(), got)
	}

	if err != nil {
		t.Fatalf("namespace %q: %v", ns, err)
	}

	return got.Fence()
}

// scoped returns e in the audit scope of a grant write in ns.
func scoped(e audit.Entry, ns namespace.Name) audit.Entry {
	if ns != "" {
		e.Namespace = ns
	}

	return e
}

// createGrant writes g with the fence of its namespace and e in the audit
// scope of that namespace.
func createGrant(t *testing.T, s access.Store, g access.Grant, e audit.Entry) error {
	t.Helper()

	ns := g.Object.Namespace()

	return s.CreateGrant(t.Context(), g, fenceOf(t, s, ns), scoped(e, ns))
}

// revokeGrant revokes the grant id in ns with the fence of ns and e in the
// audit scope of ns.
func revokeGrant(t *testing.T, s access.Store, id string, ns namespace.Name, e audit.Entry) error {
	t.Helper()

	return s.RevokeGrant(t.Context(), id, fenceOf(t, s, ns), scoped(e, ns))
}

// testGrantFence checks the fence of a grant write inside a namespace: a
// stale fence, the fence of another namespace and an entry in another scope
// change nothing, and the entry of a grant and of its revoke is in the
// namespace of the grant.
func testGrantFence(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")
	fence := fenceOf(t, s, "acme")
	beta := fenceOf(t, s, "beta")
	g := newGrant("grt-1", principalSubject("prn-a"), access.RoleViewer, acme)

	stale := fence
	stale.FailoverVersion++

	refusals := []struct {
		fence namespace.Fence
		want  error
		e     audit.Entry
		name  string
	}{
		{stale, namespace.ErrFenced, scoped(entry(), "acme"), "a stale fence"},
		{beta, access.ErrInvalid, scoped(entry(), "beta"), "the fence of another namespace"},
		{namespace.Fence{Namespace: "", FailoverVersion: 0}, access.ErrInvalid, entry(), "no fence"},
		{fence, audit.ErrInvalidEntry, entry(), "an entry in @server"},
	}

	for i := range refusals {
		rc := &refusals[i]
		if err := s.CreateGrant(ctx, g, rc.fence, rc.e); !errors.Is(err, rc.want) {
			t.Errorf("grant with %s = %v, want %v", rc.name, err, rc.want)
		}
	}

	if _, err := s.GetGrant(ctx, "grt-1"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant after the refusals = %v, want ErrNotFound", err)
	}

	mustCreateGrant(t, s, g)

	if err := s.RevokeGrant(ctx, "grt-1", stale, scoped(entry(), "acme")); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("revoke with a stale fence = %v, want ErrFenced", err)
	}

	if err := s.RevokeGrant(ctx, "grt-1", beta, scoped(entry(), "beta")); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("revoke through another namespace = %v, want ErrNotFound", err)
	}

	if err := s.RevokeGrant(ctx, "grt-1", fenceOf(t, s, ""), entry()); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("revoke through the installation = %v, want ErrNotFound", err)
	}

	if _, err := s.GetGrant(ctx, "grt-1"); err != nil {
		t.Fatalf("grant after the refused revokes: %v", err)
	}

	if err := revokeGrant(t, s, "grt-1", "acme", entry()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	checkScopeEntries(t, s, "acme", 2)
	checkScopeEntries(t, s, "beta", 0)
}

// checkScopeEntries checks that the scope ns holds want entries, when the
// store also keeps the audit log.
func checkScopeEntries(t *testing.T, s access.Store, ns namespace.Name, want int64) {
	t.Helper()

	log, ok := s.(audit.Store)
	if !ok {
		return
	}

	listing, err := log.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries of %q: %v", ns, err)
	}

	if listing.Total != want {
		t.Errorf("entries of %q = %d, want %d", ns, listing.Total, want)
	}
}
