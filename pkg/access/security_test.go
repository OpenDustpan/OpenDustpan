package access_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// principals returns a principal service over the fixture store, guarded by
// the access service, as pkg/dustpan builds it.
func (f *fixture) principals(tb testing.TB) *principal.Service {
	tb.Helper()

	return f.principalsWith(tb, f.svc)
}

// principalsWith returns a principal service over the fixture store whose
// Guard, GroupReach and Tokens are svc.
func (f *fixture) principalsWith(tb testing.TB, svc *access.Service) *principal.Service {
	tb.Helper()

	p, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      f.store,
			Guard:      svc,
			GroupReach: svc,
			Namespaces: nil,
			Tokens:     svc,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		tb.Fatalf("principal service: %v", err)
	}

	return p
}

// accountAdministrator stores the principal subject with the
// account-administrator role on the installation.
func (f *fixture) accountAdministrator(tb testing.TB, subject string) identity.Principal {
	tb.Helper()

	p := f.person(tb, subject)
	f.grant(tb, prn(subject), access.RoleAccountAdministrator, identity.ServerObject())

	return p
}

// TestRoleChangesNeedTheirActions proves that a custom role cannot hold more
// than its maker: an account administrator cannot make or change a role
// with operator actions, so it cannot give itself server:restore.
func TestRoleChangesNeedTheirActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "acct")

	operator := []identity.Action{identity.ActionServerRestore}

	_, err := f.svc.CreateRole(
		ctx,
		testEntry(),
		acct,
		access.RoleInput{Name: "restorer", Description: "", Actions: operator},
	)
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("create a role with an operator action = %v, want ErrEscalation", err)
	}

	r, err := f.svc.CreateRole(ctx, testEntry(), acct, access.RoleInput{
		Name: "reader", Description: "", Actions: []identity.Action{identity.ActionNamespaceRead},
	})
	if err != nil {
		t.Fatalf("create a data role: %v", err)
	}

	f.grant(t, prn("acct"), r.ID, identity.ServerObject())

	_, err = f.svc.UpdateRole(ctx, testEntry(), acct, r.ID, access.RoleInput{
		Name: "reader", Description: "",
		Actions: []identity.Action{
			identity.ActionNamespaceRead, identity.ActionServerRestore, identity.ActionServerBackup,
			identity.ActionServerMaintenance,
		},
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("add operator actions to a held role = %v, want ErrEscalation", err)
	}

	assertChecks(t, f.authz, acct, []check{{identity.ServerObject(), identity.ActionServerRestore, false}})

	// A role that a grant gives below the installation keeps data actions
	// only, even for the bootstrap admin.
	ns, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "lister", Description: "", Actions: []identity.Action{identity.ActionNamespaceRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.grant(t, prn("acct"), ns.ID, identity.NamespaceObject("acme"))

	_, err = f.svc.UpdateRole(ctx, testEntry(), admin(), ns.ID, access.RoleInput{
		Name: "lister", Description: "",
		Actions: []identity.Action{identity.ActionNamespaceRead, identity.ActionServerBackup},
	})
	if !errors.Is(err, access.ErrInUse) {
		t.Fatalf("add an operator action to a role granted on a namespace = %v, want ErrInUse", err)
	}
}

// TestMembershipIsAGrantByProxy proves that a membership change gives or
// takes the grants of the group: an account administrator cannot join a
// group that holds site-administrator, nor remove its members.
func TestMembershipIsAGrantByProxy(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	principals := f.principals(t)
	acct := f.accountAdministrator(t, "acct")
	f.person(t, "olga")

	ops := f.group(t, "ops", "olga")
	f.grant(t, ops, access.RoleSiteAdministrator, identity.ServerObject())

	readers := f.group(t, "readers")
	f.grant(t, readers, access.RoleViewer, identity.NamespaceObject("acme"))

	_, err := principals.AddMember(ctx, testEntry(), acct, ops.ID, "prn-acct")
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("join a group that holds site-administrator = %v, want ErrEscalation", err)
	}

	assertChecks(t, f.authz, acct, []check{{identity.ServerObject(), identity.ActionServerRestore, false}})

	err = principals.RemoveMember(ctx, testEntry(), acct, ops.ID, "prn-olga")
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("remove a member of that group = %v, want ErrEscalation", err)
	}

	if _, err := principals.AddMember(ctx, testEntry(), acct, readers.ID, "prn-olga"); err != nil {
		t.Fatalf("add a member to a group that gives what the caller holds: %v", err)
	}

	if _, err := principals.AddMember(ctx, testEntry(), admin(), ops.ID, "prn-acct"); err != nil {
		t.Fatalf("bootstrap admin adds a member: %v", err)
	}
}

// TestRevokeAndDeleteNeedTheirActions proves that a revoke, and the delete
// of a principal or a group, need every action that the grants give. Two
// admins of one namespace hold the same actions there, so each can revoke
// the other: the RFC 19 Outcome accepts it.
func TestRevokeAndDeleteNeedTheirActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	principals := f.principals(t)
	acct := f.accountAdministrator(t, "acct")

	f.person(t, "boss")

	boss, err := f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
		Object: identity.ServerObject(), Role: access.RoleSiteAdministrator, Subject: prn("boss"),
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	_, revokeErr := f.svc.Revoke(ctx, testEntry(), acct, identity.ServerObject(), boss.ID)
	if !errors.Is(revokeErr, access.ErrEscalation) {
		t.Fatalf("revoke a site-administrator grant = %v, want ErrEscalation", revokeErr)
	}

	deleteErr := principals.DeletePrincipal(ctx, testEntry(), acct, "prn-boss")
	if !errors.Is(deleteErr, access.ErrEscalation) {
		t.Fatalf("delete a site administrator = %v, want ErrEscalation", deleteErr)
	}

	f.person(t, "olga")
	ops := f.group(t, "ops", "olga")
	f.grant(t, ops, access.RoleSiteAdministrator, identity.ServerObject())

	// The delete of olga ends her membership of ops, so it takes away
	// what ops gives.
	deleteErr = principals.DeletePrincipal(ctx, testEntry(), acct, "prn-olga")
	if !errors.Is(deleteErr, access.ErrEscalation) {
		t.Fatalf("delete a member of a site-administrator group = %v, want ErrEscalation", deleteErr)
	}

	deleteErr = principals.DeleteGroup(ctx, testEntry(), acct, ops.ID)
	if !errors.Is(deleteErr, access.ErrEscalation) {
		t.Fatalf("delete a group with site-administrator = %v, want ErrEscalation", deleteErr)
	}

	checkEqualAdmins(t, f)
}

// disableAllPersons disables every person with the inactivity sweep, so a
// test can enable them again one by one.
func (f *fixture) disableAllPersons(tb testing.TB) {
	tb.Helper()

	_, err := f.store.SweepInactive(tb.Context(), accesstoken.Sweep{
		Inactivity: time.Hour,
		Tokens:     false,
		Accounts:   true,
	}, storetest.ServerChange)
	if err != nil {
		tb.Fatalf("sweep inactive: %v", err)
	}
}

// disabled reports whether the principal with the ID id is disabled.
func disabled(tb testing.TB, principals *principal.Service, id string) bool {
	tb.Helper()

	p, err := principals.GetPrincipal(tb.Context(), id)
	if err != nil {
		tb.Fatalf("get principal %q: %v", id, err)
	}

	return !p.DisabledAt.IsZero()
}

// enableSetup stores a disabled site administrator with a direct grant
// (boss) and one with a grant through a group (olga), plus a site
// administrator caller (sam), then enables the callers again, so a test can
// enable one target as sam.
//
//nolint:nonamedreturns // The names distinguish the two principals at callers.
func enableSetup(tb testing.TB) (principals *principal.Service, acct, sam identity.Principal) {
	tb.Helper()

	f := newFixture(tb)
	ctx := tb.Context()
	principals = f.principals(tb)
	acct = f.accountAdministrator(tb, "acct")
	sam = f.person(tb, "sam")
	f.grant(tb, prn("sam"), access.RoleSiteAdministrator, identity.ServerObject())

	f.person(tb, "boss")

	_, err := f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
		Object: identity.ServerObject(), Role: access.RoleSiteAdministrator, Subject: prn("boss"),
	})
	if err != nil {
		tb.Fatalf("grant: %v", err)
	}

	f.person(tb, "olga")

	ops := f.group(tb, "ops", "olga")
	f.grant(tb, ops, access.RoleSiteAdministrator, identity.ServerObject())

	f.disableAllPersons(tb)

	// The sweep disables every person, so enable the callers again and keep
	// only the targets disabled.
	for _, id := range []string{"prn-acct", "prn-sam"} {
		if err := principals.EnablePrincipal(ctx, testEntry(), admin(), id); err != nil {
			tb.Fatalf("enable %s: %v", id, err)
		}
	}

	return principals, acct, sam
}

// TestEnableNeedsTheirActions proves that an enable needs every action
// that a direct grant of the principal gives: an account administrator
// cannot enable a disabled site administrator, but a site administrator
// that is not a bootstrap admin can.
func TestEnableNeedsTheirActions(t *testing.T) {
	t.Parallel()

	principals, acct, sam := enableSetup(t)
	ctx := t.Context()

	if !disabled(t, principals, "prn-boss") {
		t.Fatal("boss is enabled after the sweep, want disabled")
	}

	if err := principals.EnablePrincipal(ctx, testEntry(), acct, "prn-boss"); !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("enable a site administrator = %v, want ErrEscalation", err)
	}

	if !disabled(t, principals, "prn-boss") {
		t.Fatal("boss is enabled after a refused enable, want disabled")
	}

	if err := principals.EnablePrincipal(ctx, testEntry(), sam, "prn-boss"); err != nil {
		t.Fatalf("enable as a site administrator: %v", err)
	}

	if disabled(t, principals, "prn-boss") {
		t.Fatal("boss is disabled after an allowed enable, want enabled")
	}
}

// TestEnableThroughGroupNeedsTheirActions proves that an enable needs
// every action that a grant through a group gives: an account administrator
// cannot enable a disabled member of a site-administrator group, but a site
// administrator that is not a bootstrap admin can.
func TestEnableThroughGroupNeedsTheirActions(t *testing.T) {
	t.Parallel()

	principals, acct, sam := enableSetup(t)
	ctx := t.Context()

	if !disabled(t, principals, "prn-olga") {
		t.Fatal("olga is enabled after the sweep, want disabled")
	}

	// The enable of olga restores what ops gives through her membership.
	if err := principals.EnablePrincipal(ctx, testEntry(), acct, "prn-olga"); !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("enable a member of a site-administrator group = %v, want ErrEscalation", err)
	}

	if !disabled(t, principals, "prn-olga") {
		t.Fatal("olga is enabled after a refused enable, want disabled")
	}

	if err := principals.EnablePrincipal(ctx, testEntry(), sam, "prn-olga"); err != nil {
		t.Fatalf("enable a group member as a site administrator: %v", err)
	}

	if disabled(t, principals, "prn-olga") {
		t.Fatal("olga is disabled after an allowed enable, want enabled")
	}
}

// checkEqualAdmins proves that one admin of a namespace can revoke the
// admin grant of another, the creator's grant included: they hold the same
// actions there.
func checkEqualAdmins(t *testing.T, f *fixture) {
	t.Helper()

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")
	alice := f.person(t, "alice")
	bob := f.person(t, "bob")
	f.grant(t, prn("alice"), access.RoleAdmin, acme)

	_, err := f.svc.Grant(ctx, testEntry(), alice, access.GrantInput{
		Object: acme, Role: access.RoleAdmin, Subject: prn("bob"),
	})
	if err != nil {
		t.Fatalf("namespace admin grants admin: %v", err)
	}

	page, err := f.svc.ListGrants(ctx, acme, access.PageRequest{Cursor: "", Holder: "", Role: "", Limit: 0})
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	for i := range page.Grants {
		if page.Grants[i].Grant.Subject != prn("alice") {
			continue
		}

		if _, revokeErr := f.svc.Revoke(ctx, testEntry(), bob, acme, page.Grants[i].Grant.ID); revokeErr != nil {
			t.Fatalf("an admin revokes the grant of an admin of equal power: %v", revokeErr)
		}
	}
}

// TestUnitGrantsFollowTheStableID proves that a grant on a unit follows its
// stable ID: a new unit with the name of a deleted one does not get the
// grants of the old one.
func TestUnitGrantsFollowTheStableID(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	alice := f.person(t, "alice")
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, prn("alice"), access.RoleViewer, web)
	assertChecks(t, f.authz, alice, []check{{web, identity.ActionUnitRead, true}})

	ns, err := f.store.GetNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("stack ref: %v", err)
	}

	gone := testEntry()
	gone.At, gone.Namespace, gone.Action = time.Unix(1, 0), "acme", "unit:manage"
	gone.Object, gone.Kind, gone.Outcome = web.String(), audit.KindChange, audit.OutcomeSuccess

	deleteErr := f.store.DeleteStack(
		ctx,
		ref,
		ns.Fence(),
		stack.EmptyGuard(),
		inventory.Deletion{Entry: gone, PurgeAfter: gone.At.Add(inventory.DeletedRetention)},
	)
	if deleteErr != nil {
		t.Fatalf("delete stack: %v", deleteErr)
	}

	again := stack.Stack{
		NamespaceID: "",
		CreatedAt:   time.Unix(1, 0), UpdatedAt: time.Unix(1, 0), Tags: map[string]string{}, Ref: ref,
		ID: "stk-web-prod-again", Version: 0,
	}
	if createErr := f.store.CreateStack(ctx, again, ns.Fence()); createErr != nil {
		t.Fatalf("create stack again: %v", createErr)
	}

	assertChecks(t, f.authz, alice, []check{{web, identity.ActionUnitRead, false}})

	scope, err := f.authz.ListObjects(access.WithRequestCache(ctx), alice, identity.ActionUnitRead)
	if err != nil || len(scope.Units) != 0 {
		t.Fatalf("scope after the new unit = %+v, %v; want no unit", scope, err)
	}

	page, err := f.svc.ListGrants(ctx, web, access.PageRequest{Cursor: "", Holder: "", Role: "", Limit: 0})
	if err != nil || len(page.Grants) != 0 {
		t.Fatalf("grants on the new unit = %+v, %v; want none", page.Grants, err)
	}

	missing := unitObject(t, "acme", "api/prod")

	_, err = f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
		Object: missing, Role: access.RoleViewer, Subject: prn("alice"),
	})
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant on a unit that does not exist = %v, want ErrNotFound", err)
	}
}

// TestGrantRacesADelete proves that a grant and the delete of its subject
// cannot both win: after the two run at the same time, the deleted
// principal holds no current grant, in each of 50 runs.
func TestGrantRacesADelete(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	principals := f.principals(t)

	const runs = 50

	for i := range runs {
		subject := fmt.Sprintf("racer%02d", i)
		f.person(t, subject)

		var wg sync.WaitGroup

		wg.Go(func() {
			// The grant may lose the race: then it answers ErrNotFound.
			_, err := f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
				Object: identity.NamespaceObject("acme"), Role: access.RoleViewer, Subject: prn(subject),
			})
			if err != nil && !errors.Is(err, access.ErrNotFound) && !errors.Is(err, principal.ErrNotFound) {
				t.Errorf("grant to %s: %v", subject, err)
			}
		})
		wg.Go(func() {
			if err := principals.DeletePrincipal(ctx, testEntry(), admin(), "prn-"+subject); err != nil {
				t.Errorf("delete %s: %v", subject, err)
			}
		})
		wg.Wait()

		grants, err := f.store.GrantsOf(ctx, prn(subject))
		if err != nil {
			t.Fatalf("grants of %s: %v", subject, err)
		}

		if len(grants) != 0 {
			t.Fatalf("run %d: the deleted principal holds %d current grants, want 0", i, len(grants))
		}
	}
}

// TestGrantBelowInstallationGivesDataActionsOnly proves the second line of
// the placement rule: a grant on a namespace whose role holds an action of
// another family gives the data actions of the role only. The Service
// refuses such a role below the installation, so the test stores the row
// directly, as a role change that races a grant could leave it.
func TestGrantBelowInstallationGivesDataActionsOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	p := f.person(t, "mixed")

	at := time.Unix(0, 0)
	r := access.Role{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          "rol-mixed",
		Name:        "mixed",
		Description: "",
		BuiltIn:     false,
		Actions: []identity.Action{
			identity.ActionServerBackup,
			identity.ActionServerManageAccess,
			identity.ActionUnitRead,
		},
	}
	if err := f.store.CreateRole(ctx, r, storetest.ServerChange(at, "role:rol-mixed")); err != nil {
		t.Fatalf("create role: %v", err)
	}

	acme := identity.NamespaceObject("acme")
	f.grant(t, prn("mixed"), r.ID, acme)

	assertChecks(t, f.authz, p, []check{
		{acme, identity.ActionUnitRead, true},
		{acme, identity.ActionServerBackup, false},
		{acme, identity.ActionServerManageAccess, false},
	})

	got, err := f.authz.Explain(ctx, p, acme)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if len(got.Actions) != 1 || got.Actions[0] != identity.ActionUnitRead {
		t.Fatalf("actions on acme = %v, want unit:read only", got.Actions)
	}

	scope, err := f.authz.ListObjects(ctx, p, identity.ActionServerBackup)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if scope.CoversEveryObject() || len(scope.Namespaces) != 0 {
		t.Fatalf("scope of server:backup = %+v, want nothing", scope)
	}
}

// TestUpdateRoleNeedsTheActionsItRemoves proves that a role change takes
// away only what its maker holds: each current grant of the role loses the
// removed actions at once, as a revoke would.
func TestUpdateRoleNeedsTheActionsItRemoves(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	acct := f.accountAdministrator(t, "remover")

	r, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "ops", Description: "",
		Actions: []identity.Action{identity.ActionServerBackup, identity.ActionNamespaceRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	_, err = f.svc.UpdateRole(ctx, testEntry(), acct, r.ID, access.RoleInput{
		Name: "ops", Description: "", Actions: []identity.Action{identity.ActionNamespaceRead},
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("remove an operator action without holding it = %v, want ErrEscalation", err)
	}

	// The bootstrap admin holds every action, so it can take any away.
	_, err = f.svc.UpdateRole(ctx, testEntry(), admin(), r.ID, access.RoleInput{
		Name: "ops", Description: "", Actions: []identity.Action{identity.ActionNamespaceRead},
	})
	if err != nil {
		t.Fatalf("remove an operator action as the bootstrap admin: %v", err)
	}
}

// TestTokenActionsHaveTheirFamilies places the access token actions of ADR
// 53: an account administrator manages the tokens of others, an operator
// does not, and every signed-in principal manages its own.
func TestTokenActionsHaveTheirFamilies(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acct := f.accountAdministrator(t, "tokens-admin")
	ops := f.person(t, "ops")
	plain := f.person(t, "plain")

	f.grant(t, prn("ops"), access.RoleInstallationOperator, identity.ServerObject())

	server := identity.ServerObject()
	manage := []identity.Action{
		identity.ActionServerReadTokens, identity.ActionServerManageTokens, identity.ActionServerManageTokenPolicy,
	}

	for _, a := range manage {
		if family, ok := access.FamilyOf(a); !ok || family != access.FamilyAdministration {
			t.Errorf("family of %s = %q, %t; want administration", a, family, ok)
		}

		assertChecks(t, f.authz, acct, []check{{server, a, true}})
		assertChecks(t, f.authz, ops, []check{{server, a, false}})
	}

	if family, ok := access.FamilyOf(identity.ActionPrincipalManageTokens); !ok || family != access.FamilyDelegation {
		t.Errorf("family of %s = %q, %t; want delegation", identity.ActionPrincipalManageTokens, family, ok)
	}

	assertChecks(t, f.authz, acct, []check{{server, identity.ActionPrincipalManageTokens, true}})
	assertChecks(t, f.authz, ops, []check{{server, identity.ActionPrincipalManageTokens, false}})
	assertChecks(t, f.authz, plain, []check{{identity.CallerObject(), identity.ActionTokenManageOwn, true}})
}

// TestSignInSettingsAreSiteOnly places server:manage-sign-in: it is in the
// administration family, the site administrator holds it, and the account
// administrator and the operator do not. An account administrator cannot
// give it to itself through a custom role.
func TestSignInSettingsAreSiteOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acct := f.accountAdministrator(t, "sign-in-acct")
	site := f.person(t, "sign-in-site")
	ops := f.person(t, "sign-in-ops")

	f.grant(t, prn("sign-in-site"), access.RoleSiteAdministrator, identity.ServerObject())
	f.grant(t, prn("sign-in-ops"), access.RoleInstallationOperator, identity.ServerObject())

	a := identity.ActionServerManageSignIn
	if family, ok := access.FamilyOf(a); !ok || family != access.FamilyAdministration {
		t.Errorf("family of %s = %q, %t; want administration", a, family, ok)
	}

	server := identity.ServerObject()
	assertChecks(t, f.authz, site, []check{{server, a, true}})
	assertChecks(t, f.authz, acct, []check{{server, a, false}})
	assertChecks(t, f.authz, ops, []check{{server, a, false}})

	_, err := f.svc.CreateRole(t.Context(), testEntry(), acct, access.RoleInput{
		Name: "sign-in", Description: "", Actions: []identity.Action{a},
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("create a role with %s = %v, want ErrEscalation", a, err)
	}
}

// TestRestoreAccessIsSiteOnly places server:restore-access: it is in the
// administration family, and of the built-in roles only the site
// administrator holds it. A restore with the access tables replaces who
// has access, so the account administrator and the operator do not hold
// it, and an account administrator cannot give it to itself through a
// custom role.
func TestRestoreAccessIsSiteOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acct := f.accountAdministrator(t, "restore-acct")
	site := f.person(t, "restore-site")
	ops := f.person(t, "restore-ops")

	f.grant(t, prn("restore-site"), access.RoleSiteAdministrator, identity.ServerObject())
	f.grant(t, prn("restore-ops"), access.RoleInstallationOperator, identity.ServerObject())

	a := identity.ActionServerRestoreAccess
	if family, ok := access.FamilyOf(a); !ok || family != access.FamilyAdministration {
		t.Errorf("family of %s = %q, %t; want administration", a, family, ok)
	}

	for _, r := range access.BuiltInRoles() {
		want := r.ID == access.RoleSiteAdministrator
		if got := r.Has(a); got != want {
			t.Errorf("built-in role %q has %s = %t, want %t", r.ID, a, got, want)
		}
	}

	server := identity.ServerObject()
	assertChecks(t, f.authz, site, []check{{server, a, true}})
	assertChecks(t, f.authz, acct, []check{{server, a, false}})
	assertChecks(t, f.authz, ops, []check{{server, a, false}})

	_, err := f.svc.CreateRole(t.Context(), testEntry(), acct, access.RoleInput{
		Name: "restorer", Description: "", Actions: []identity.Action{a},
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("create a role with %s = %v, want ErrEscalation", a, err)
	}
}

// TestManageSignInCatalogIsSiteOnly walks the role catalog: only the site
// administrator holds server:manage-sign-in, and the operator family holds
// no such action, so no operator role gains it by a family change.
func TestManageSignInCatalogIsSiteOnly(t *testing.T) {
	t.Parallel()

	a := identity.ActionServerManageSignIn

	for _, r := range access.BuiltInRoles() {
		want := r.ID == access.RoleSiteAdministrator
		if got := r.Has(a); got != want {
			t.Errorf("role %q holds %s = %t, want %t", r.ID, a, got, want)
		}
	}

	for _, o := range access.Actions(access.FamilyOperator) {
		if o == a {
			t.Errorf("operator family holds %s, want no such action", a)
		}
	}
}
