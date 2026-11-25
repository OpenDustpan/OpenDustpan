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
		if _, found := got[p.InstallationRole()]; found {
			t.Errorf("namespace has a grant of the installation role %q", p.InstallationRole())
		}

		if !p.ActsInsideNamespace() {
			continue
		}

		want++

		role := p.NamespaceRole()

		g, found := got[role]
		if !found {
			t.Errorf("namespace has no grant of role %q", role)

			continue
		}

		if g.CreatedBy != "@server" || !g.CreatedAt.Equal(ns.CreatedAt) ||
			g.Subject != (access.Subject{Kind: access.SubjectPrincipal, ID: p.ID()}) {
			t.Errorf("grant of role %q = %+v, want the server as creator at %v", role, g, ns.CreatedAt)
		}
	}

	if want != namespaceHousekeepingRoles() || len(got) != want {
		t.Fatalf("housekeeping grants = %d, want %d", len(got), want)
	}
}

func TestCreateNamespaceHousekeepingGrantsOfBootstrapAdmin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	g, err := f.svc.CreateNamespace(t.Context(), testEntry(), admin(), housekeepingNamespace("delta"))
	if err != nil || g != nil {
		t.Fatalf("bootstrap admin create namespace = %v, %v, want no creator grant", g, err)
	}

	if got, want := housekeepingGrantsOn(t, f, "delta"), namespaceHousekeepingRoles(); len(got) != want {
		t.Fatalf("housekeeping grants = %d, want %d", len(got), want)
	}
}

func TestCreateNamespaceHousekeepingGrantStaysRevoked(t *testing.T) {
	t.Parallel()

	testHousekeepingGrantStaysRevoked(t)
}

func testHousekeepingGrantStaysRevoked(t *testing.T) {
	t.Helper()

	f := newFixture(t)
	account := f.person(t, "account")
	caller := admin()

	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, housekeepingNamespace("gamma")); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	revoked := housekeepingGrantsOn(t, f, "gamma")["housekeeping-job-reconcile"]
	if revoked.ID == "" {
		t.Fatal("no grant of the role housekeeping-job-reconcile on gamma")
	}

	_, err := f.svc.Revoke(t.Context(), testEntry(), caller, identity.NamespaceObject("gamma"), revoked.ID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, housekeepingNamespace("omega")); err != nil {
		t.Fatalf("create second namespace: %v", err)
	}

	got := housekeepingGrantsOn(t, f, "gamma")
	if _, found := got["housekeeping-job-reconcile"]; found || len(got) != namespaceHousekeepingRoles()-1 {
		t.Fatalf("housekeeping grants on gamma = %d, want %d without the revoked one",
			len(got), namespaceHousekeepingRoles()-1)
	}

	if other, want := housekeepingGrantsOn(t, f, "omega"), namespaceHousekeepingRoles(); len(other) != want {
		t.Fatalf("housekeeping grants on omega = %d, want %d", len(other), want)
	}
}

// TestRevokedNamespaceGrantDeniesManageAgentRuns checks that a revoke of the
// grant of the agent reaper on a namespace denies namespace:manage-agent-runs
// on that namespace. The grant of the agent reaper on @server reaches each
// namespace, so that grant must hold no namespace action. The sweep of
// reveals on @server stays allowed.
func TestRevokedNamespaceGrantDeniesManageAgentRuns(t *testing.T) {
	t.Parallel()

	checkRevokeDeniesManageAgentRuns(t)
}

func checkRevokeDeniesManageAgentRuns(t *testing.T) {
	t.Helper()

	f := newFixture(t)
	account := f.person(t, "account")
	revoker := admin()

	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, housekeepingNamespace("gamma")); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	reaper := agentReaperPurpose(t)
	caller := principal.Housekeeping(reaper)
	gamma := identity.NamespaceObject("gamma")

	assertChecks(t, f.authz, caller, []check{
		{obj: gamma, action: identity.ActionNamespaceManageAgentRuns, allow: true},
		{obj: identity.ServerObject(), action: identity.ActionServerSweepAgentReveals, allow: true},
	})

	revoked := housekeepingGrantsOn(t, f, "gamma")[access.RoleHousekeepingAgentRunReaper]
	if revoked.ID == "" {
		t.Fatalf("no grant of the role %s on gamma", access.RoleHousekeepingAgentRunReaper)
	}

	if _, err := f.svc.Revoke(t.Context(), testEntry(), revoker, gamma, revoked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	assertChecks(t, f.authz, caller, []check{
		{obj: gamma, action: identity.ActionNamespaceManageAgentRuns, allow: false},
		{obj: identity.ServerObject(), action: identity.ActionServerSweepAgentReveals, allow: true},
	})
}

// agentReaperPurpose returns the purpose that holds the role of the reaper of
// agent runs on each namespace.
func agentReaperPurpose(t *testing.T) principal.HousekeepingPurpose {
	t.Helper()

	for _, p := range principal.HousekeepingPurposes() {
		if p.NamespaceRole() == access.RoleHousekeepingAgentRunReaper {
			return p
		}
	}

	t.Fatalf("no purpose holds the role %s", access.RoleHousekeepingAgentRunReaper)

	return principal.HousekeepingPurpose{}
}

// namespaceHousekeepingRoles returns the count of the purposes that hold a
// role on each namespace.
func namespaceHousekeepingRoles() int {
	n := 0

	for _, p := range principal.HousekeepingPurposes() {
		if p.ActsInsideNamespace() {
			n++
		}
	}

	return n
}
