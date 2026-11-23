package access_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// housekeepingNamespace returns a namespace with the name name.
func housekeepingNamespace(name namespace.Name) namespace.Namespace {
	return namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: name, PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}
}

// housekeepingGrantsOn returns the grants on the namespace name that hold a
// housekeeping role, by role.
func housekeepingGrantsOn(t *testing.T, f *fixture, name namespace.Name) map[string]access.Grant {
	t.Helper()

	list, _, err := f.store.ListGrantsOn(t.Context(), identity.NamespaceObject(name), access.GrantQuery{
		AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: access.MaxLimit,
	})
	if err != nil {
		t.Fatalf("list grants on %q: %v", name, err)
	}

	out := make(map[string]access.Grant)

	for i := range list {
		if g := list[i].Grant; len(g.Role) > len("housekeeping-") && g.Role[:len("housekeeping-")] == "housekeeping-" {
			out[g.Role] = g
		}
	}

	return out
}

func TestCreateNamespaceHousekeepingGrants(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	account := f.person(t, "account")

	ns := housekeepingNamespace("gamma")
	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	got := housekeepingGrantsOn(t, f, "gamma")

	var want int

	for _, p := range principal.HousekeepingPurposes() {
		if !p.ActsInsideNamespace() {
			if _, found := got[p.Role()]; found {
				t.Errorf("namespace has a grant of role %q, which does not act inside a namespace", p.Role())
			}

			continue
		}

		want++

		g, found := got[p.Role()]
		if !found {
			t.Errorf("namespace has no grant of role %q", p.Role())

			continue
		}

		if g.CreatedBy != "@server" || !g.CreatedAt.Equal(ns.CreatedAt) ||
			g.Subject != (access.Subject{Kind: access.SubjectPrincipal, ID: p.ID()}) {
			t.Errorf("grant of role %q = %+v, want the server as creator at %v", p.Role(), g, ns.CreatedAt)
		}
	}

	if want != 5 || len(got) != want {
		t.Fatalf("housekeeping grants = %d, want 5 (purposes: %d)", len(got), want)
	}
}

func TestCreateNamespaceHousekeepingGrantsOfBootstrapAdmin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	g, err := f.svc.CreateNamespace(t.Context(), testEntry(), admin(), housekeepingNamespace("delta"))
	if err != nil || g != nil {
		t.Fatalf("bootstrap admin create namespace = %v, %v, want no creator grant", g, err)
	}

	if got := housekeepingGrantsOn(t, f, "delta"); len(got) != 5 {
		t.Fatalf("housekeeping grants = %d, want 5", len(got))
	}
}

func TestCreateNamespaceHousekeepingGrantStaysRevoked(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	account := f.person(t, "account")

	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, housekeepingNamespace("gamma")); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	revoked := housekeepingGrantsOn(t, f, "gamma")["housekeeping-job-reconcile"]
	if revoked.ID == "" {
		t.Fatal("no grant of the role housekeeping-job-reconcile on gamma")
	}

	if _, err := f.svc.Revoke(t.Context(), testEntry(), admin(), identity.NamespaceObject("gamma"), revoked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, housekeepingNamespace("omega")); err != nil {
		t.Fatalf("create second namespace: %v", err)
	}

	got := housekeepingGrantsOn(t, f, "gamma")
	if _, found := got["housekeeping-job-reconcile"]; found || len(got) != 4 {
		t.Fatalf("housekeeping grants on gamma = %d, want 4 without the revoked one", len(got))
	}

	if other := housekeepingGrantsOn(t, f, "omega"); len(other) != 5 {
		t.Fatalf("housekeeping grants on omega = %d, want 5", len(other))
	}
}
