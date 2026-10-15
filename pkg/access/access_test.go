package access_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// collections is a fixed map from the wire form of a unit to its collection.
type collections map[string]string

func (c collections) CollectionOf(_ context.Context, unit identity.Object) (string, bool, error) {
	name, ok := c[unit.String()]

	return name, ok, nil
}

// fixture is one store with an authorizer and a service over it.
type fixture struct {
	store *sqlite.Store
	authz *access.Authorizer
	svc   *access.Service
	units collections
}

func newFixture(tb testing.TB) *fixture {
	tb.Helper()

	st, err := sqlite.Open(tb.Context(), filepath.Join(tb.TempDir(), "dustpan.db"))
	if err != nil {
		tb.Fatalf("open store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			tb.Errorf("close store: %v", closeErr)
		}
	})

	units := collections{}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: st, Collections: units, Units: st,
	})
	if err != nil {
		tb.Fatalf("new authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz, Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"},
	})
	if err != nil {
		tb.Fatalf("new service: %v", err)
	}

	f := &fixture{store: st, authz: authz, svc: svc, units: units}

	for _, ns := range []namespace.Name{"acme", "beta"} {
		f.namespace(tb, ns)
	}

	return f
}

func (f *fixture) namespace(tb testing.TB, ns namespace.Name) {
	tb.Helper()

	err := f.store.CreateNamespace(tb.Context(), namespace.Namespace{
		CreatedAt: time.Unix(0, 0), Name: ns, HomeRegion: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	})
	if err != nil {
		tb.Fatalf("create namespace %q: %v", ns, err)
	}
}

// person stores a principal with the ID "prn-<subject>" and returns its
// caller.
func (f *fixture) person(tb testing.TB, subject string) identity.Principal {
	tb.Helper()

	err := f.store.CreatePrincipal(tb.Context(), principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), ID: "prn-" + subject, Subject: subject,
		DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		DisabledAt: time.Time{},
	}, storetest.ServerChange(time.Unix(0, 0), "principal:prn-"+subject))
	if err != nil {
		tb.Fatalf("create principal %q: %v", subject, err)
	}

	return identity.Principal{Subject: subject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{}}
}

// group stores the group "grp-<name>" with the members.
func (f *fixture) group(tb testing.TB, name string, members ...string) access.Subject {
	tb.Helper()

	ctx := tb.Context()
	id := "grp-" + name

	grp := principal.Group{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), ID: id, Name: name, Description: "",
		Source: principal.SourceLocal, MemberCount: 0,
	}
	if err := f.store.CreateGroup(ctx, grp, storetest.ServerChange(time.Unix(0, 0), "group:"+id)); err != nil {
		tb.Fatalf("create group %q: %v", name, err)
	}

	for _, m := range members {
		mem := principal.Membership{
			AddedAt: time.Unix(0, 0), GroupID: id, PrincipalID: "prn-" + m,
		}
		if err := f.store.AddMember(ctx, mem, storetest.ServerChange(time.Unix(0, 0), "group:"+id)); err != nil {
			tb.Fatalf("add member %q: %v", m, err)
		}
	}

	return access.Subject{Kind: access.SubjectGroup, ID: id}
}

// unit returns the unit obj with its stable ID, and makes its stack first
// when it does not exist: a grant names a unit that exists.
func (f *fixture) unit(tb testing.TB, obj identity.Object) identity.Object {
	tb.Helper()

	ctx := tb.Context()

	if id, found, err := f.store.UnitID(ctx, obj); err != nil || found {
		if err != nil {
			tb.Fatalf("unit ID: %v", err)
		}

		return obj.WithUnitID(id)
	}

	ns, err := f.store.GetNamespace(ctx, obj.Namespace())
	if err != nil {
		tb.Fatalf("namespace of unit: %v", err)
	}

	project, name, _ := strings.Cut(obj.UnitName(), "/")

	ref, err := stack.ParseRef(obj.Namespace().String(), project, name)
	if err != nil {
		tb.Fatalf("stack ref: %v", err)
	}

	st := stack.Stack{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), Tags: map[string]string{}, Ref: ref,
		ID: stack.ID("stk-" + strings.ReplaceAll(obj.UnitName(), "/", "-")), Version: 0,
	}
	if err := f.store.CreateStack(ctx, st, ns.Fence()); err != nil {
		tb.Fatalf("create stack: %v", err)
	}

	return obj.WithUnitID(string(st.ID))
}

// grant stores a grant of role on obj to subject, with no check. A unit
// gets its stack first.
func (f *fixture) grant(tb testing.TB, subject access.Subject, role string, obj identity.Object) {
	tb.Helper()

	if obj.IsUnit() {
		obj = f.unit(tb, obj)
	}

	fence, e := storetest.GrantWrite(tb, f.store, obj, time.Unix(0, 0), "grant:test")

	err := f.store.CreateGrant(tb.Context(), access.Grant{
		CreatedAt: time.Unix(0, 0), Condition: nil, Object: obj,
		ID:   fmt.Sprintf("grt-%s-%s-%s", subject.ID, role, obj.String()),
		Role: role, CreatedBy: "admin", Subject: subject,
	}, fence, e)
	if err != nil {
		tb.Fatalf("create grant: %v", err)
	}
}

// admin returns the bootstrap admin caller, which holds every action.
func admin() identity.Principal {
	return identity.Principal{Subject: "admin", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{}}
}

func prn(subject string) access.Subject {
	return access.Subject{Kind: access.SubjectPrincipal, ID: "prn-" + subject}
}

func unitObject(tb testing.TB, ns namespace.Name, name string) identity.Object {
	tb.Helper()

	obj, err := identity.UnitObject("pulumi", ns, name)
	if err != nil {
		tb.Fatalf("unit object: %v", err)
	}

	return obj
}

// check is one expected decision.
type check struct {
	obj    identity.Object
	action identity.Action
	allow  bool
}

func assertChecks(t *testing.T, authz *access.Authorizer, p identity.Principal, checks []check) {
	t.Helper()

	for i := range checks {
		c := &checks[i]
		err := authz.Authorize(t.Context(), p, c.action, c.obj)

		switch {
		case c.allow && err != nil:
			t.Errorf("%s %s on %q = %v, want allowed", p.Subject, c.action, c.obj.String(), err)
		case !c.allow && !errors.Is(err, identity.ErrDenied):
			t.Errorf("%s %s on %q = %v, want ErrDenied", p.Subject, c.action, c.obj.String(), err)
		default:
		}
	}
}

func TestContainment(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ns := f.person(t, "nsviewer")
	coll := f.person(t, "collviewer")
	site := f.person(t, "siteviewer")
	unitOnly := f.person(t, "unitviewer")

	web := unitObject(t, "acme", "web/prod")
	api := unitObject(t, "acme", "api/prod")
	other := unitObject(t, "beta", "web/prod")
	payments := identity.CollectionObject("acme", "payments")
	f.units[web.String()] = "payments"

	f.grant(t, prn("nsviewer"), access.RoleViewer, identity.NamespaceObject("acme"))
	f.grant(t, prn("collviewer"), access.RoleViewer, payments)
	f.grant(t, prn("siteviewer"), access.RoleViewer, identity.ServerObject())
	f.grant(t, prn("unitviewer"), access.RoleViewer, web)

	read := identity.ActionUnitRead

	assertChecks(t, f.authz, ns, []check{
		{web, read, true},
		{api, read, true},
		{payments, identity.ActionCollectionRead, true},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, true},
		{other, read, false},
		{web, identity.ActionUnitWrite, false},
	})
	assertChecks(t, f.authz, coll, []check{
		{web, read, true},
		{payments, identity.ActionCollectionRead, true},
		{api, read, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, false},
	})
	assertChecks(t, f.authz, site, []check{{web, read, true}, {api, read, true}, {other, read, true}})
	assertChecks(t, f.authz, unitOnly, []check{
		{web, read, true}, {api, read, false}, {payments, identity.ActionCollectionRead, false},
	})
}

func TestGroupGrants(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	member := f.person(t, "member")
	outsider := f.person(t, "outsider")
	ops := f.group(t, "ops", "member")
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, ops, access.RoleWriter, identity.NamespaceObject("acme"))

	assertChecks(t, f.authz, member, []check{{web, identity.ActionUnitWrite, true}})
	assertChecks(t, f.authz, outsider, []check{{web, identity.ActionUnitRead, false}})

	err := f.store.RemoveMember(
		t.Context(),
		ops.ID,
		"prn-member",
		storetest.ServerChange(time.Unix(0, 0), "group:x"),
	)
	if err != nil {
		t.Fatalf("remove member: %v", err)
	}

	assertChecks(t, f.authz, member, []check{{web, identity.ActionUnitRead, false}})
}

func TestUnknownAndDeletedPrincipals(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	deleted := f.person(t, "deleted")
	unknown := identity.Principal{
		Subject:        "nobody",
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, prn("deleted"), access.RoleSiteAdministrator, identity.ServerObject())

	err := f.store.DeletePrincipal(
		t.Context(),
		"prn-deleted",
		storetest.ServerChange(time.Unix(0, 0), "p"),
	)
	if err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	for _, p := range []identity.Principal{deleted, unknown} {
		assertChecks(t, f.authz, p, []check{
			{web, identity.ActionUnitRead, false},
			{identity.ServerObject(), identity.ActionServerBackup, false},
			{identity.CallerObject(), identity.ActionCallerRead, true},
		})
	}
}

func TestUnclassifiedActionIsDenied(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	site := f.person(t, "site")
	f.grant(t, prn("site"), access.RoleSiteAdministrator, identity.ServerObject())

	assertChecks(t, f.authz, site, []check{{identity.ServerObject(), identity.Action("server:future"), false}})

	admin := identity.Principal{
		Subject:        "admin",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	assertChecks(t, f.authz, admin, []check{{identity.ServerObject(), identity.Action("server:future"), true}})
}

// TestOperatorHoldsNoAdministrationOrData checks the operator refusals that
// the families exist for: an operator grant on the installation cannot make
// a principal, cannot grant, and cannot read the state of a unit.
func TestOperatorHoldsNoAdministrationOrData(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	op := f.person(t, "operator")
	f.person(t, "target")
	f.grant(t, prn("operator"), access.RoleInstallationOperator, identity.ServerObject())

	web := unitObject(t, "acme", "web/prod")
	server := identity.ServerObject()

	assertChecks(t, f.authz, op, []check{
		{server, identity.ActionServerBackup, true},
		{server, identity.ActionServerRestore, true},
		{server, identity.ActionServerMaintenance, true},
		{server, identity.ActionServerManagePrincipals, false},
		{server, identity.ActionServerReadPrincipals, false},
		{server, identity.ActionServerManageAccess, false},
		{server, identity.ActionServerCreateNamespace, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceManageAccess, false},
		{web, identity.ActionUnitRead, false},
		{web, identity.ActionUnitReadSecrets, false},
	})

	_, err := f.svc.Grant(t.Context(), testEntry(), op, access.GrantInput{
		Object: identity.NamespaceObject("acme"), Role: access.RoleViewer, Subject: prn("target"),
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("operator grants viewer = %v, want ErrEscalation", err)
	}
}

// familiesOf returns the sorted families of the actions of r.
func familiesOf(r access.Role) []access.Family {
	var out []access.Family

	for _, a := range r.Actions {
		family, _ := access.FamilyOf(a)
		if !slices.Contains(out, family) {
			out = append(out, family)
		}
	}

	slices.Sort(out)

	return out
}

func TestBuiltInRoleFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role string
		want []access.Family
	}{
		{access.RoleInstallationOperator, []access.Family{access.FamilyOperator}},
		{
			access.RoleAccountAdministrator,
			[]access.Family{access.FamilyAdministration, access.FamilyData, access.FamilyDelegation},
		},
		{
			access.RoleSiteAdministrator,
			[]access.Family{
				access.FamilyAdministration, access.FamilyData, access.FamilyDelegation, access.FamilyOperator,
			},
		},
		{access.RoleAdmin, []access.Family{access.FamilyData}},
		{access.RoleViewer, []access.Family{access.FamilyData}},
		{access.RoleTokenManager, []access.Family{access.FamilyDelegation}},
	}

	for _, tc := range cases {
		r, ok := access.BuiltInRole(tc.role)
		if !ok {
			t.Fatalf("no built-in role %q", tc.role)
		}

		if got := familiesOf(r); !slices.Equal(got, tc.want) {
			t.Errorf("families of %q = %v, want %v", tc.role, got, tc.want)
		}
	}

	site, _ := access.BuiltInRole(access.RoleSiteAdministrator)
	families := []access.Family{
		access.FamilyOperator, access.FamilyAdministration, access.FamilyData, access.FamilyDelegation,
	}
	for _, f := range families {
		for _, a := range access.Actions(f) {
			if !site.Has(a) {
				t.Errorf("site administrator lacks %s", a)
			}
		}
	}
}

// TestRestoreAndPurgeGrants proves the grants of the ADR 47 actions: a
// maintainer may restore a deleted unit but not purge it, because a purge
// cannot be undone, and an admin may do both.
func TestRestoreAndPurgeGrants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role        string
		wantRestore bool
		wantPurge   bool
	}{
		{role: access.RoleViewer, wantRestore: false, wantPurge: false},
		{role: access.RoleWriter, wantRestore: false, wantPurge: false},
		{role: access.RoleMaintainer, wantRestore: true, wantPurge: false},
		{role: access.RoleAdmin, wantRestore: true, wantPurge: true},
	}

	for _, tc := range cases {
		r, ok := access.BuiltInRole(tc.role)
		if !ok {
			t.Fatalf("no built-in role %q", tc.role)
		}

		if got := r.Has(identity.ActionUnitRestore); got != tc.wantRestore {
			t.Errorf("%s has unit:restore = %v, want %v", tc.role, got, tc.wantRestore)
		}

		if got := r.Has(identity.ActionUnitPurge); got != tc.wantPurge {
			t.Errorf("%s has unit:purge = %v, want %v", tc.role, got, tc.wantPurge)
		}
	}
}

func TestEscalation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	nsAdmin := f.person(t, "nsadmin")
	collAdmin := f.person(t, "colladmin")
	account := f.person(t, "account")
	f.person(t, "target")

	payments := identity.CollectionObject("acme", "payments")
	f.grant(t, prn("nsadmin"), access.RoleAdmin, identity.NamespaceObject("acme"))
	f.grant(t, prn("colladmin"), access.RoleAdmin, payments)
	f.grant(t, prn("account"), access.RoleAccountAdministrator, identity.ServerObject())

	cases := []struct {
		obj     identity.Object
		granter identity.Principal
		name    string
		role    string
		want    error
	}{
		{
			name: "namespace admin gives writer on a unit", granter: nsAdmin, role: access.RoleWriter,
			obj: unitObject(t, "acme", "web/prod"), want: nil,
		},
		{
			name: "namespace admin gives admin on its namespace", granter: nsAdmin, role: access.RoleAdmin,
			obj: identity.NamespaceObject("acme"), want: nil,
		},
		{
			name: "namespace admin gives viewer on another namespace", granter: nsAdmin, role: access.RoleViewer,
			obj: identity.NamespaceObject("beta"), want: access.ErrEscalation,
		},
		{
			name: "namespace admin gives viewer on the installation", granter: nsAdmin, role: access.RoleViewer,
			obj: identity.ServerObject(), want: access.ErrEscalation,
		},
		{
			name: "collection admin gives viewer on its namespace", granter: collAdmin, role: access.RoleViewer,
			obj: identity.NamespaceObject("acme"), want: access.ErrEscalation,
		},
		{
			// No collection exists until the collections are wired (#478).
			name: "collection admin gives writer on its collection", granter: collAdmin, role: access.RoleWriter,
			obj: payments, want: access.ErrNotFound,
		},
		{
			name: "account administrator gives operator", granter: account, role: access.RoleInstallationOperator,
			obj: identity.ServerObject(), want: access.ErrEscalation,
		},
		{
			name: "account administrator gives account administrator", granter: account,
			role: access.RoleAccountAdministrator, obj: identity.ServerObject(), want: nil,
		},
		{
			name: "an administration role on a namespace", granter: account, role: access.RoleAccountAdministrator,
			obj: identity.NamespaceObject("acme"), want: access.ErrInvalid,
		},
	}

	for _, tc := range cases {
		if tc.obj.IsUnit() {
			f.unit(t, tc.obj)
		}

		_, err := f.svc.Grant(t.Context(), testEntry(), tc.granter, access.GrantInput{
			Object: tc.obj, Role: tc.role, Subject: prn("target"),
		})

		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s = %v, want nil", tc.name, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s = %v, want %v", tc.name, err, tc.want)
		default:
		}
	}
}

func TestGrantChecksSubjectAndNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	admin := identity.Principal{
		Subject:        "admin",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	f.person(t, "target")

	cases := []struct {
		in   access.GrantInput
		want error
	}{
		{
			access.GrantInput{Object: identity.NamespaceObject("acme"), Role: access.RoleViewer, Subject: prn("ghost")},
			principal.ErrNotFound,
		},
		{access.GrantInput{
			Object: identity.NamespaceObject("acme"), Role: access.RoleViewer,
			Subject: access.Subject{Kind: access.SubjectGroup, ID: "grp-ghost"},
		}, principal.ErrNotFound},
		{
			access.GrantInput{
				Object:  identity.NamespaceObject("gamma"),
				Role:    access.RoleViewer,
				Subject: prn("target"),
			},
			namespace.ErrNotFound,
		},
		{
			access.GrantInput{Object: identity.NamespaceObject("acme"), Role: "rol-ghost", Subject: prn("target")},
			access.ErrNotFound,
		},
		{
			access.GrantInput{Object: identity.CallerObject(), Role: access.RoleViewer, Subject: prn("target")},
			access.ErrInvalid,
		},
	}

	for _, tc := range cases {
		if _, err := f.svc.Grant(t.Context(), testEntry(), admin, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("grant %+v = %v, want %v", tc.in, err, tc.want)
		}
	}
}

// TestPlacementRuleDoesNotAuthorize checks that the right to create a unit
// is checked on the collection that the unit goes to. A placement rule that
// routes a unit to a collection where the caller has no grant is refused,
// although the caller can create units in another collection.
func TestPlacementRuleDoesNotAuthorize(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	dev := f.person(t, "dev")
	f.grant(t, prn("dev"), access.RoleMaintainer, identity.CollectionObject("acme", "sandbox"))

	// A placement rule routes "payments-*" units to the collection payments.
	target := identity.CollectionObject("acme", "payments")

	assertChecks(t, f.authz, dev, []check{
		{identity.CollectionObject("acme", "sandbox"), identity.ActionCollectionCreateUnit, true},
		{target, identity.ActionCollectionCreateUnit, false},
	})
}

func TestExplain(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.person(t, "alice")
	ops := f.group(t, "ops", "alice")
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, prn("alice"), access.RoleViewer, identity.NamespaceObject("acme"))
	f.grant(t, ops, access.RoleWriter, web)
	f.grant(t, ops, access.RoleWriter, unitObject(t, "acme", "api/prod"))

	got, err := f.authz.Explain(t.Context(), p, web)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	writer, _ := access.BuiltInRole(access.RoleWriter)
	if !slices.Equal(got.Actions, writer.Actions) {
		t.Errorf("actions = %v, want %v", got.Actions, writer.Actions)
	}

	if len(got.Grants) != 2 {
		t.Errorf("grants = %+v, want the namespace grant and the unit grant", got.Grants)
	}
}

// TestExplainListsOnlyGrantsThatGive proves that an answer lists only the
// grants that give an action: not a grant whose custom role no longer
// exists, and not a grant below the installation whose role gives no data
// action. The store refuses to delete a role that a grant holds, and the
// Service refuses an operator role below the installation, so the test
// reads the role through a store that has lost it and stores the operator
// role directly, as a role change that races a grant could leave them.
func TestExplainListsOnlyGrantsThatGive(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	p := f.person(t, "alice")
	acme := identity.NamespaceObject("acme")

	gone, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "gone", Description: "", Actions: []identity.Action{identity.ActionUnitRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	at := time.Unix(0, 0)
	backup := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: "rol-backup", Name: "backup", Description: "", BuiltIn: false,
		Actions: []identity.Action{identity.ActionServerBackup},
	}
	err = f.store.CreateRole(ctx, backup, storetest.ServerChange(at, "role:rol-backup"))
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.grant(t, prn("alice"), access.RoleViewer, acme)
	f.grant(t, prn("alice"), gone.ID, acme)
	f.grant(t, prn("alice"), backup.ID, acme)

	lost := &lostRoleStore{Store: f.store, role: gone.ID}

	authz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: lost, Callers: lost, Collections: nil, Units: lost},
	)
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	got, err := authz.Explain(ctx, p, acme)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	viewer, _ := access.BuiltInRole(access.RoleViewer)
	if !slices.Equal(got.Actions, viewer.Actions) {
		t.Errorf("actions = %v, want %v", got.Actions, viewer.Actions)
	}

	if len(got.Grants) != 1 || got.Grants[0].Role != access.RoleViewer {
		t.Errorf("grants = %+v, want the viewer grant only", got.Grants)
	}
}

// lostRoleStore reads the custom role with the ID role as deleted.
type lostRoleStore struct {
	*sqlite.Store

	role string
}

func (l *lostRoleStore) GetRoles(ctx context.Context, ids []string) ([]access.Role, error) {
	roles, err := l.Store.GetRoles(ctx, ids)

	return slices.DeleteFunc(roles, func(r access.Role) bool { return r.ID == l.role }), err
}

func TestListObjects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.person(t, "alice")
	ops := f.group(t, "ops", "alice")
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, prn("alice"), access.RoleViewer, identity.NamespaceObject("acme"))
	f.grant(t, ops, access.RoleWriter, web)
	f.grant(t, ops, access.RoleViewer, identity.CollectionObject("beta", "payments"))

	scope, err := f.authz.ListObjects(t.Context(), p, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if scope.All || len(scope.Namespaces) != 1 || len(scope.Collections) != 1 || len(scope.Units) != 1 {
		t.Fatalf("read scope = %+v, want one namespace, one collection and one unit", scope)
	}

	scope, err = f.authz.ListObjects(t.Context(), p, identity.ActionUnitWrite)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if scope.All || len(scope.Namespaces) != 0 || len(scope.Units) != 1 || scope.Units[0].String() != web.String() {
		t.Fatalf("write scope = %+v, want the unit only", scope)
	}
}

func TestCreateNamespaceGrantsTheCreator(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	account := f.person(t, "account")
	admin := identity.Principal{
		Subject:        "admin",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}

	ns := namespace.Namespace{
		CreatedAt: time.Unix(0, 0), Name: "gamma", HomeRegion: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}

	g, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, ns)
	if err != nil || g == nil {
		t.Fatalf("create namespace = %v, %v, want a grant", g, err)
	}

	if g.Role != access.RoleNamespaceAdmin || g.Subject != prn("account") || g.Object.String() != "gamma" {
		t.Fatalf("grant = %+v, want admin on gamma to the creator", g)
	}

	assertChecks(t, f.authz, account, []check{
		{identity.NamespaceObject("gamma"), identity.ActionNamespaceManageAccess, true},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, false},
	})

	ns.Name = "delta"

	g, err = f.svc.CreateNamespace(t.Context(), testEntry(), admin, ns)
	if err != nil || g != nil {
		t.Fatalf("bootstrap admin create namespace = %v, %v, want no grant", g, err)
	}
}

func TestCustomRoles(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	r, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "deployer", Description: "Runs updates",
		Actions: []identity.Action{identity.ActionUnitWrite, identity.ActionUnitRead, identity.ActionUnitWrite},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	if want := []identity.Action{identity.ActionUnitRead, identity.ActionUnitWrite}; !slices.Equal(r.Actions, want) {
		t.Fatalf("actions = %v, want %v", r.Actions, want)
	}

	invalid := []access.RoleInput{
		{Name: "Viewer", Description: "", Actions: []identity.Action{identity.ActionUnitRead}},
		{Name: "bad name", Description: "", Actions: []identity.Action{identity.ActionUnitRead}},
		{Name: "empty", Description: "", Actions: nil},
		{Name: "future", Description: "", Actions: []identity.Action{"unit:future"}},
		{Name: "self", Description: "", Actions: []identity.Action{identity.ActionCallerRead}},
	}

	for _, in := range invalid {
		if _, createErr := f.svc.CreateRole(ctx, testEntry(), admin(), in); !errors.Is(createErr, access.ErrInvalid) {
			t.Errorf("create role %+v = %v, want ErrInvalid", in, createErr)
		}
	}

	in := access.RoleInput{
		Name: "x", Description: "", Actions: []identity.Action{identity.ActionUnitRead},
	}
	_, err = f.svc.UpdateRole(ctx, testEntry(), admin(), access.RoleViewer, in)
	if !errors.Is(err, access.ErrBuiltIn) {
		t.Errorf("update built-in role = %v, want ErrBuiltIn", err)
	}

	if err := f.svc.DeleteRole(ctx, testEntry(), access.RoleViewer); !errors.Is(err, access.ErrBuiltIn) {
		t.Errorf("delete built-in role = %v, want ErrBuiltIn", err)
	}

	p := f.person(t, "deployer")
	web := unitObject(t, "acme", "web/prod")
	f.grant(t, prn("deployer"), r.ID, web)

	assertChecks(t, f.authz, p, []check{{web, identity.ActionUnitWrite, true}, {web, identity.ActionUnitManage, false}})
}

func TestRevokeChecksTheObject(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	admin := identity.Principal{
		Subject:        "admin",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}
	f.person(t, "target")

	g, err := f.svc.Grant(t.Context(), testEntry(), admin, access.GrantInput{
		Object: identity.NamespaceObject("acme"), Role: access.RoleViewer, Subject: prn("target"),
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	_, err = f.svc.Revoke(t.Context(), testEntry(), admin, identity.NamespaceObject("beta"), g.ID)
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("revoke through another namespace = %v, want ErrNotFound", err)
	}

	_, err = f.svc.Revoke(t.Context(), testEntry(), admin, identity.NamespaceObject("acme"), g.ID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
}

// testEntry is the entry of a caller, as the HTTP adapter builds it.
func testEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "tester", IDPSubject: "", TokenID: "",
		Action: "server:manage-access", Object: "", Version: "", ValuePath: "", ValueCount: nil,
		Changes:  nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "",
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
		ObjectID: "",
	}
}

// countingStore counts the reads that a decision makes.
type countingStore struct {
	*sqlite.Store

	subjects atomic.Int64
	roles    atomic.Int64
}

func (c *countingStore) SubjectsOf(ctx context.Context, subject string) ([]access.Subject, error) {
	c.subjects.Add(1)

	return c.Store.SubjectsOf(ctx, subject)
}

func (c *countingStore) GetRoles(ctx context.Context, ids []string) ([]access.Role, error) {
	c.roles.Add(1)

	return c.Store.GetRoles(ctx, ids)
}

// TestRequestCache proves that the decisions of one request read the
// subjects of the caller once and the custom roles once, and that a new
// request reads them again, so a change takes effect on the next request.
func TestRequestCache(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := f.person(t, "alice")
	ops := f.group(t, "ops", "alice")
	dev := f.group(t, "dev", "alice")

	ctx := t.Context()

	reader, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "reader", Description: "", Actions: []identity.Action{identity.ActionUnitRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.grant(t, ops, reader.ID, identity.NamespaceObject("acme"))
	f.grant(t, dev, reader.ID, identity.NamespaceObject("beta"))

	counted := &countingStore{Store: f.store, subjects: atomic.Int64{}, roles: atomic.Int64{}}

	authz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: counted, Callers: counted, Collections: nil, Units: counted},
	)
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	request := access.WithRequestCache(ctx)

	for _, obj := range []identity.Object{
		unitObject(t, "acme", "web/prod"), unitObject(t, "acme", "api/prod"), unitObject(t, "beta", "web/prod"),
	} {
		err = authz.Authorize(request, p, identity.ActionUnitRead, obj)
		if err != nil {
			t.Fatalf("authorize %q: %v", obj.String(), err)
		}
	}

	if got := counted.subjects.Load(); got != 1 {
		t.Errorf("subject reads in one request = %d, want 1", got)
	}

	if got := counted.roles.Load(); got != 1 {
		t.Errorf("role reads in one request = %d, want 1", got)
	}

	err = f.store.RemoveMember(ctx, ops.ID, "prn-alice", storetest.ServerChange(time.Unix(0, 0), "m"))
	if err != nil {
		t.Fatalf("remove member: %v", err)
	}

	next := access.WithRequestCache(ctx)

	err = authz.Authorize(next, p, identity.ActionUnitRead, unitObject(t, "acme", "web/prod"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("authorize after the removal = %v, want ErrDenied on the next request", err)
	}

	if got := counted.subjects.Load(); got != 2 {
		t.Errorf("subject reads after a new request = %d, want 2", got)
	}
}
