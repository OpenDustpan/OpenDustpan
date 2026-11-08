package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errGrantsUnavailable is what a failing grant port answers.
var errGrantsUnavailable = errors.New("grants unavailable")

// hiddenObject is what the refusal of the reach compare writes in place of
// an object that the caller cannot read.
const hiddenObject = "an object that you cannot read"

// failingGrants is a grant port whose reads fail, for the fail-closed tests.
type failingGrants struct{}

func (failingGrants) GrantsOf(context.Context, access.Subject) ([]access.Grant, error) {
	return nil, errGrantsUnavailable
}

func (failingGrants) GrantsOfSubjects(context.Context, []access.Subject) ([]access.Grant, error) {
	return nil, errGrantsUnavailable
}

func (failingGrants) RoleGrantedBelowInstallation(context.Context, string) (bool, error) {
	return false, errGrantsUnavailable
}

func (failingGrants) RoleGrantedOnPrincipal(context.Context, string) (bool, error) {
	return false, errGrantsUnavailable
}

// serviceWith returns an access service over the fixture store with the
// reach config reach.
func (f *fixture) serviceWith(tb testing.TB, reach access.ReachConfig) *access.Service {
	tb.Helper()

	return f.serviceOver(tb, reach, f.store)
}

// serviceOver returns an access service over the fixture store with the
// reach config reach and the directory dir.
func (f *fixture) serviceOver(
	tb testing.TB, reach access.ReachConfig, dir access.Directory,
) *access.Service {
	tb.Helper()

	svc, err := access.NewService(access.ServiceConfig{
		Store: f.store, Directory: dir, Namespaces: f.store, Authorizer: f.authz, Reach: reach,
		Now: nil, NewID: nil, BootstrapAdmins: []string{"admin"}, LimitGroups: nil,
	})
	if err != nil {
		tb.Fatalf("access service: %v", err)
	}

	return svc
}

// serviceOverStore returns an access service over store with the reach
// config reach, for a test of a store read that fails. Its authorizer reads
// the roles through the same store, so a failed role read reaches it too.
func (f *fixture) serviceOverStore(
	tb testing.TB, store access.Store, reach access.ReachConfig,
) *access.Service {
	tb.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: store, Callers: f.store, Collections: f.units, Units: f.store, Principals: f.store,
	})
	if err != nil {
		tb.Fatalf("authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: store, Directory: f.store, Namespaces: f.store, Authorizer: authz, Reach: reach,
		Now: nil, NewID: nil, BootstrapAdmins: []string{"admin"}, LimitGroups: nil,
	})
	if err != nil {
		tb.Fatalf("access service: %v", err)
	}

	return svc
}

// failingGroupGrants is a store whose read of the grants of a subject
// fails, for the fail-closed test of a membership add.
type failingGroupGrants struct {
	access.Store
}

func (failingGroupGrants) GrantsOf(context.Context, access.Subject) ([]access.Grant, error) {
	return nil, errGrantsUnavailable
}

// failingRoleStore is a store whose read of one role fails, for the
// fail-closed test of a membership add.
type failingRoleStore struct {
	access.Store
}

func (failingRoleStore) GetRole(context.Context, string) (access.Role, error) {
	return access.Role{}, errGrantsUnavailable
}

// namelessDirectory reads the store, and answers no principal by subject:
// the granter of the test has no principal record.
type namelessDirectory struct {
	access.Directory
}

func (namelessDirectory) GetPrincipalBySubject(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, principal.ErrNotFound
}

// reach returns the reach config of the fixture.
func (f *fixture) reach() access.ReachConfig {
	return access.ReachConfig{
		Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store,
		Collections: f.units, Units: f.store,
	}
}

// failingReach returns the reach config of the fixture with a grant port
// whose reads fail.
func (f *fixture) failingReach() access.ReachConfig {
	return access.ReachConfig{
		Grants: failingGrants{}, Roles: f.store, Callers: f.store, Principals: f.store,
		Collections: f.units, Units: f.store,
	}
}

// portlessReach returns a reach config with no port: the server cannot read
// a reach, so the compare refuses the change.
func portlessReach() access.ReachConfig {
	return access.ReachConfig{
		Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
	}
}

// TestGrantOnAServicePrincipalNeedsTheReachOfTheGranter proves the reach
// rule of a grant of a delegation role on a service principal: the granter
// holds the reach of that principal, so a grant of the token role never
// gives a right to act as a principal that reaches past the granter. An
// administrator passes, and a reach that the server cannot read refuses the
// grant.
func TestGrantOnAServicePrincipalNeedsTheReachOfTheGranter(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	acct := f.accountAdministrator(t, "acct")
	f.person(t, "bob")
	f.person(t, "carol")
	f.person(t, "dave")
	f.person(t, "erin")
	f.installationService(t, "prn-svc-ops", "svc-ops")
	f.installationService(t, "prn-svc-plain", "svc-plain")
	group := f.group(t, "token-holders")

	// The operator principal reaches the operator actions, which the
	// account administrator does not hold. The plain principal reads the
	// installation, which the account administrator covers.
	f.grant(t, prn("svc-ops"), access.RoleInstallationOperator, identity.ServerObject())
	f.grant(t, prn("svc-plain"), access.RoleViewer, identity.ServerObject())

	// The minder holds server:manage-access only, so it may make the grant
	// and it reads no principal: its refusal names no principal.
	minderRole, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "minder", Description: "", Actions: []identity.Action{identity.ActionServerManageAccess},
	})
	if err != nil {
		t.Fatalf("create the minder role: %v", err)
	}

	f.grant(t, prn("minder"), minderRole.ID, identity.ServerObject())

	minder := f.person(t, "minder")

	failing := f.serviceWith(t, f.failingReach())
	nameless := f.serviceOver(t, f.reach(), namelessDirectory{Directory: f.store})
	portless := f.serviceWith(t, portlessReach())

	ops := identity.PrincipalObject("prn-svc-ops")
	plain := identity.PrincipalObject("prn-svc-plain")

	tests := []grantRow{
		{
			name:    "the account administrator grants the token role on an operator principal to itself",
			service: nil, granter: acct, object: ops, subject: prn("acct"), want: access.ErrEscalation,
			names: "prn-svc-ops", hides: "", cause: nil,
		},
		{
			name:    "the account administrator grants the token role on an operator principal to a group",
			service: nil, granter: acct, object: ops, subject: group, want: access.ErrEscalation,
			names: "prn-svc-ops", hides: "", cause: nil,
		},
		{
			name:    "the account administrator grants the token role on an operator principal to a second principal",
			service: nil, granter: acct, object: ops, subject: prn("bob"), want: access.ErrEscalation,
			names: "prn-svc-ops", hides: "", cause: nil,
		},
		{
			name:    "a granter that cannot read the principal",
			service: nil, granter: minder, object: ops, subject: prn("carol"), want: access.ErrEscalation,
			names: hiddenObject, hides: "prn-svc-ops", cause: nil,
		},
		{
			name:    "the account administrator grants the token role on a plain principal",
			service: nil, granter: acct, object: plain, subject: prn("bob"), want: nil,
			names: "", hides: "", cause: nil,
		},
		{
			name:    "an administrator grants the token role on an operator principal",
			service: nil, granter: admin(), object: ops, subject: prn("carol"), want: nil,
			names: "", hides: "", cause: nil,
		},
		{
			name:    "a failed read of the reach refuses the grant",
			service: failing, granter: acct, object: ops, subject: prn("erin"), want: access.ErrEscalation,
			names: hiddenObject, hides: "", cause: errGrantsUnavailable,
		},
		{
			name:    "a granter with no principal record is refused",
			service: nameless, granter: acct, object: ops, subject: prn("dave"), want: access.ErrEscalation,
			names: hiddenObject, hides: "", cause: principal.ErrNotFound,
		},
		{
			name:    "a missing reach port refuses the grant",
			service: portless, granter: acct, object: ops, subject: prn("dave"), want: access.ErrEscalation,
			names: hiddenObject, hides: "", cause: access.ErrMissingDependency,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkGrantOfTokenRole(ctx, t, f, tc)
		})
	}
}

// grantRow is one case of the reach rule of a grant on a service principal.
type grantRow struct {
	name    string
	service *access.Service
	granter identity.Principal
	object  identity.Object
	subject access.Subject
	want    error
	// names holds text that the refusal must hold, and hides text that it
	// must not hold.
	names string
	hides string
	// cause is a sentinel of the error chain of the refusal.
	cause error
}

// checkGrantOfTokenRole runs one grantRow: it makes the grant through the
// service of the row, or the fixture service, and checks the error.
func checkGrantOfTokenRole(ctx context.Context, t *testing.T, f *fixture, tc *grantRow) {
	t.Helper()

	svc := tc.service
	if svc == nil {
		svc = f.svc
	}

	g, err := svc.Grant(ctx, testEntry(), tc.granter,
		access.GrantInput{Object: tc.object, Role: access.RoleTokenManager, Subject: tc.subject})

	if !errors.Is(err, tc.want) {
		t.Fatalf("grant = %v, want %v", err, tc.want)
	}

	if tc.want == nil && (g.Role != access.RoleTokenManager || g.Object.String() != tc.object.String()) {
		t.Fatalf("grant = %+v, want the token role on %q", g, tc.object.String())
	}

	if tc.names != "" && !strings.Contains(err.Error(), tc.names) {
		t.Errorf("refusal %q does not hold %q", err, tc.names)
	}

	if tc.hides != "" && strings.Contains(err.Error(), tc.hides) {
		t.Errorf("refusal %q holds %q", err, tc.hides)
	}

	if tc.cause != nil && !errors.Is(err, tc.cause) {
		t.Fatalf("grant = %v, want %v", err, tc.cause)
	}
}

// TestAddMemberToAGroupNeedsTheReachOfTheCaller proves the reach rule of a
// membership add: a group that holds the token role on a service principal
// gives each member the right to act as that principal, so the caller holds
// the reach of every such principal. An administrator passes, and a reach
// that the server cannot read refuses the add.
func TestAddMemberToAGroupNeedsTheReachOfTheCaller(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	acct := f.accountAdministrator(t, "acct")
	f.person(t, "bob")
	f.person(t, "carol")
	f.person(t, "dave")
	f.installationService(t, "prn-svc-ops", "svc-ops")
	f.installationService(t, "prn-svc-plain", "svc-plain")

	f.grant(t, prn("svc-ops"), access.RoleInstallationOperator, identity.ServerObject())
	f.grant(t, prn("svc-plain"), access.RoleViewer, identity.ServerObject())

	operators := f.group(t, "operators")
	f.grant(t, operators, access.RoleTokenManager, identity.PrincipalObject("prn-svc-ops"))

	viewers := f.group(t, "viewers")
	f.grant(t, viewers, access.RoleTokenManager, identity.PrincipalObject("prn-svc-plain"))

	readers := f.group(t, "readers")

	// The custom group holds one custom role, so a failed read of a custom
	// role refuses the check of its reach.
	customRole, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "custom-token-manager", Description: "",
		Actions: []identity.Action{identity.ActionPrincipalManageTokens},
	})
	if err != nil {
		t.Fatalf("create the custom role: %v", err)
	}

	custom := f.group(t, "custom-holders")
	f.grant(t, custom, customRole.ID, identity.PrincipalObject("prn-svc-ops"))

	failing := f.principalsWith(t, f.serviceWith(t, f.failingReach()))
	portless := f.principalsWith(t, f.serviceWith(t, portlessReach()))

	tests := []memberRow{
		{
			name:    "the account administrator adds itself to a group that holds the token role on an operator principal",
			service: nil,
			caller:  acct,
			group:   operators,
			member:  "prn-acct",
			want:    access.ErrEscalation,
			cause:   access.ErrEscalation,
		},
		{
			name:    "the account administrator adds itself to a group that holds the token role on a plain principal",
			service: nil, caller: acct, group: viewers, member: "prn-acct", want: nil, cause: nil,
		},
		{
			name:    "the account administrator adds itself to a group with no such grant",
			service: nil, caller: acct, group: readers, member: "prn-acct", want: nil, cause: nil,
		},
		{
			name:    "an administrator adds a member to a group that holds the token role on an operator principal",
			service: nil, caller: admin(), group: operators, member: "prn-bob", want: nil, cause: nil,
		},
		{
			name:    "a failed read of the reach refuses the add",
			service: failing, caller: acct, group: operators, member: "prn-carol", want: access.ErrEscalation,
			cause: errGrantsUnavailable,
		},
		{
			name:    "a missing reach port refuses the add",
			service: portless, caller: acct, group: operators, member: "prn-dave", want: access.ErrEscalation,
			cause: access.ErrMissingDependency,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkAddMemberRow(ctx, t, f, tc)
		})
	}

	// A read of the grants of the group, and a read of the role of one of
	// them, fail: the check of the reach refuses, and the error names the
	// read.
	reads := []struct {
		name  string
		store access.Store
		group access.Subject
	}{
		{name: "the grants of the group", store: failingGroupGrants{Store: f.store}, group: operators},
		{name: "the role of a grant", store: failingRoleStore{Store: f.store}, group: custom},
	}

	for i := range reads {
		tc := &reads[i]
		t.Run("a failed read of "+tc.name+" refuses the check", func(t *testing.T) {
			t.Parallel()

			svc := f.serviceOverStore(t, tc.store, f.reach())

			err := svc.CheckGroupReach(ctx, acct, tc.group.ID)
			if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, errGrantsUnavailable) {
				t.Fatalf("check the reach of the group = %v, want identity.ErrDenied and the read error", err)
			}
		})
	}
}

// memberRow is one case of the reach rule of a membership add.
type memberRow struct {
	name    string
	service *principal.Service
	caller  identity.Principal
	group   access.Subject
	member  string
	want    error
	// cause is a sentinel of the error chain of the refusal.
	cause error
}

// checkAddMemberRow runs one memberRow: it adds the member through the
// service of the row, or the fixture service, and checks the error.
func checkAddMemberRow(ctx context.Context, t *testing.T, f *fixture, tc *memberRow) {
	t.Helper()

	svc := tc.service
	if svc == nil {
		svc = f.principals(t)
	}

	_, err := svc.AddMember(ctx, testEntry(), tc.caller, tc.group.ID, tc.member)
	if !errors.Is(err, tc.want) {
		t.Fatalf("add member = %v, want %v", err, tc.want)
	}

	if err != nil && !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("add member = %v, want identity.ErrDenied", err)
	}

	if !errors.Is(err, tc.cause) {
		t.Fatalf("add member = %v, want %v", err, tc.cause)
	}
}
