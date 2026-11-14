package accesstoken_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// errReachRead is the failure of a port that the reach check reads.
var errReachRead = errors.New("reach read failed")

// failingGrants is a store whose read of the grants of a subject fails.
type failingGrants struct {
	*sqlite.Store
}

func (failingGrants) GrantsOf(context.Context, access.Subject) ([]access.Grant, error) {
	return nil, errReachRead
}

// failingTargetGrants is a store whose read of the grants of the subject
// id fails, and whose other reads work.
type failingTargetGrants struct {
	*sqlite.Store

	id string
}

func (f failingTargetGrants) GrantsOf(ctx context.Context, s access.Subject) ([]access.Grant, error) {
	if s.ID == f.id {
		return nil, errReachRead
	}

	return f.Store.GrantsOf(ctx, s)
}

// failingPrincipals is a store whose read of a principal fails.
type failingPrincipals struct {
	*sqlite.Store
}

func (failingPrincipals) GetPrincipal(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, errReachRead
}

// failingSubject is a principal reader whose read of the principal of a
// subject fails.
type failingSubject struct {
	*sqlite.Store
}

func (failingSubject) GetPrincipalBySubject(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, errReachRead
}

// liveUnits is a unit port that reports one unit as current, so that a
// grant on that unit gives its actions.
type liveUnits struct {
	id  string
	obj identity.Object
}

func (l liveUnits) UnitID(context.Context, identity.Object) (string, bool, error) {
	return l.id, true, nil
}

func (l liveUnits) UnitObjects(context.Context, []string) (map[string]identity.Object, error) {
	return map[string]identity.Object{l.id: l.obj}, nil
}

// failingCollections is a collection port whose read of the collection of a
// unit fails.
type failingCollections struct{}

func (failingCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, errReachRead
}

func (failingCollections) CollectionID(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

// reachFixture is a token service that decides with the grants of the
// fixture store, with the persons and the service principals of the reach
// cases.
type reachFixture struct {
	fixture

	reach  access.ReachConfig
	tokens *accesstoken.Service

	// account holds account-administrator on the installation, site holds
	// site-administrator, tm holds token-manager on the installation,
	// holder holds token-manager on the operator service principal, member
	// holds it through a group, and bob holds nothing.
	account principal.Principal
	site    principal.Principal
	tm      principal.Principal
	holder  principal.Principal
	member  principal.Principal
	bob     principal.Principal

	// ops holds installation-operator on the installation, plain holds
	// viewer on the installation, and bare holds nothing.
	ops   principal.Principal
	plain principal.Principal
	bare  principal.Principal
}

// newReachFixture returns the fixture with the grants of the reach cases.
func newReachFixture(t *testing.T) reachFixture {
	t.Helper()

	var rf reachFixture
	rf.fixture = newFixture(t, nil)
	rf.reach = access.ReachConfig{
		Grants: rf.store, Roles: rf.store, Callers: rf.store, Principals: rf.store, Collections: nil, Units: rf.store,
	}

	rf.account = rf.personOf(t, "account@example.com")
	rf.site = rf.personOf(t, "site@example.com")
	rf.tm = rf.personOf(t, "tm@example.com")
	rf.holder = rf.personOf(t, "holder@example.com")
	rf.member = rf.personOf(t, "member@example.com")
	rf.bob = rf.personOf(t, "bob@example.com")

	rf.ops = rf.serviceOf(t, "ci-ops")
	rf.plain = rf.serviceOf(t, "ci-plain")
	rf.bare = rf.serviceOf(t, "ci-bare")

	server := identity.ServerObject()

	rf.grant(t, "grt-account", principalSubject(rf.account), access.RoleAccountAdministrator, server)
	rf.grant(t, "grt-site", principalSubject(rf.site), access.RoleSiteAdministrator, server)
	rf.grant(t, "grt-tm", principalSubject(rf.tm), access.RoleTokenManager, server)
	rf.grant(t, "grt-holder", principalSubject(rf.holder), access.RoleTokenManager, identity.PrincipalObject(rf.ops.ID))
	rf.grant(t, "grt-ops", principalSubject(rf.ops), access.RoleInstallationOperator, server)
	rf.grant(t, "grt-plain", principalSubject(rf.plain), access.RoleViewer, server)

	group, err := rf.principals.CreateGroup(t.Context(), caller(),
		principal.GroupInput{Name: "ops-holders", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	rf.grant(t, "grt-group", access.Subject{Kind: access.SubjectGroup, ID: group.ID}, access.RoleTokenManager,
		identity.PrincipalObject(rf.ops.ID))

	if _, err := rf.principals.AddMember(t.Context(), caller(), admin(), group.ID, rf.member.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	rf.tokens = rf.newService(t, rf.reach)

	return rf
}

// personOf stores a person with the subject.
func (f *reachFixture) personOf(t *testing.T, subject string) principal.Principal {
	t.Helper()

	p, err := f.principals.CreatePrincipal(t.Context(), caller(),
		principal.Input{Subject: subject, DisplayName: "", Kind: principal.KindPerson})
	if err != nil {
		t.Fatalf("create person %q: %v", subject, err)
	}

	return p
}

// serviceOf stores a service principal with the subject.
func (f *reachFixture) serviceOf(t *testing.T, subject string) principal.Principal {
	t.Helper()

	p, err := f.principals.CreatePrincipal(t.Context(), caller(),
		principal.Input{Subject: subject, DisplayName: "", Kind: principal.KindService})
	if err != nil {
		t.Fatalf("create service principal %q: %v", subject, err)
	}

	return p
}

// grant writes the grant of role on obj to subject.
func (f *reachFixture) grant(
	t *testing.T, id string, subject access.Subject, role string, obj identity.Object,
) {
	t.Helper()

	at := f.clock.Now()
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: id, Role: role, CreatedBy: "admin", Subject: subject,
	}

	fence, e := storetest.GrantWrite(t, f.store, obj, at, access.GrantRef(g))

	if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("grant %s: %v", id, err)
	}
}

// principalSubject returns the grant subject of the principal p.
func principalSubject(p principal.Principal) access.Subject {
	return access.Subject{Kind: access.SubjectPrincipal, ID: p.ID}
}

// grantTokenRoleAs grants the token role on obj to subject for granter,
// through every check of the access service, so a test makes a grant as the
// server makes it.
func (f *reachFixture) grantTokenRoleAs(
	t *testing.T, granter identity.Principal, subject access.Subject, obj identity.Object,
) {
	t.Helper()

	svc, err := access.NewService(access.ServiceConfig{
		Store:           f.store,
		Directory:       f.store,
		Namespaces:      f.store,
		Authorizer:      f.authorizerOf(t),
		Reach:           f.reach,
		Now:             f.clock.Now,
		NewID:           nil,
		BootstrapAdmins: []string{"admin"},
		Settings:        nil,
		LimitGroups:     nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	_, err = svc.Grant(t.Context(), caller(), granter,
		access.GrantInput{Object: obj, Role: access.RoleTokenManager, Subject: subject})
	if err != nil {
		t.Fatalf("grant %s on %q to %s: %v", access.RoleTokenManager, obj.String(), subject.String(), err)
	}
}

// authorizerOf returns the authorizer of the grants of the store.
func (f *reachFixture) authorizerOf(t *testing.T) *access.Authorizer {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: nil, Units: f.store, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	return authz
}

// newService returns a token service over the store of f with the reach
// config reach and the authorizer of the grants of the store.
func (f *reachFixture) newService(t *testing.T, reach access.ReachConfig) *accesstoken.Service {
	t.Helper()

	return f.newServiceWith(t, reach, f.store)
}

// newServiceWith returns a token service over the store of f with the reach
// config reach, the principal reader principals and the authorizer of the
// grants of the store.
func (f *reachFixture) newServiceWith(
	t *testing.T, reach access.ReachConfig, principals accesstoken.PrincipalReader,
) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      f.store, Principals: principals, Freshness: nil, Authorizer: f.authorizerOf(t), Reach: reach,
		Logger: nil, Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return svc
}

// missingActions returns the actions of the refusal err, in the order of the
// message, and checks that the message names the principal principalID and
// that the actions are sorted.
func missingActions(t *testing.T, err error, principalID string) []string {
	t.Helper()

	const marker = " holds actions that you do not hold: "

	text := err.Error()
	prefix := fmt.Sprintf("principal %q", principalID) + marker

	if !strings.Contains(text, prefix) {
		t.Fatalf("refusal %q does not hold %q", text, prefix)
	}

	_, list, ok := strings.Cut(text, marker)
	if !ok {
		t.Fatalf("refusal %q does not hold %q", text, marker)
	}

	parts := strings.Split(list, ", ")
	actions := make([]string, 0, len(parts))

	for i := range parts {
		action, _, hasObject := strings.Cut(parts[i], " on ")
		if !hasObject {
			t.Fatalf("refusal part %q names no object", parts[i])
		}

		actions = append(actions, action)
	}

	if !slices.IsSorted(actions) {
		t.Errorf("refusal %q names the actions out of order", text)
	}

	return actions
}

// refusal says how a token change must fail: a change that refused wraps
// identity.ErrDenied; a refusal that reachExceeded wraps
// accesstoken.ErrReachExceeded; and missing holds the parts that the text
// of the refusal must name.
type refusal struct {
	refused       bool
	reachExceeded bool
	missing       []string
}

// passes is the refusal of a change that must succeed.
func passes() refusal {
	return refusal{refused: false, reachExceeded: false, missing: nil}
}

// exceeds holds the actions of the principal that the caller does not
// hold, as the text of the refusal names them.
func exceeds(parts ...string) refusal {
	return refusal{refused: true, reachExceeded: true, missing: parts}
}

// createAs makes a service token for target as p and returns the token.
func (f *reachFixture) createAs(
	ctx context.Context, p identity.Principal, target principal.Principal,
) (accesstoken.Token, error) {
	tok, _, err := f.tokens.Create(ctx, p, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: target.ID, Name: "ci"})

	return tok, err
}

// checkChange checks the answer err of a token change for the principal
// principalID against want: a change that want.refused passes with no
// error; any refusal wraps identity.ErrDenied; a refusal that
// want.reachExceeded wraps accesstoken.ErrReachExceeded and names the
// actions of want.missing, sorted.
func checkChange(t *testing.T, err error, principalID string, want refusal) {
	t.Helper()

	if !want.refused {
		if err != nil {
			t.Fatalf("change = %v, want it to pass", err)
		}

		return
	}

	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("change = %v, want identity.ErrDenied", err)
	}

	if !want.reachExceeded {
		if errors.Is(err, accesstoken.ErrReachExceeded) {
			t.Fatalf("change = %v, want a refusal without accesstoken.ErrReachExceeded", err)
		}

		return
	}

	if !errors.Is(err, accesstoken.ErrReachExceeded) {
		t.Fatalf("change = %v, want accesstoken.ErrReachExceeded", err)
	}

	missingActions(t, err, principalID)

	for _, part := range want.missing {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("refusal %q does not name %q", err, part)
		}
	}
}

// TestServiceTokenReachOnCreate proves the reach rule of a create of a
// service token: a holder of principal:manage-tokens on the installation
// makes the token only when the reach of the principal fits inside its own
// reach.
func TestServiceTokenReachOnCreate(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	tests := []struct {
		name   string
		caller principal.Principal
		target principal.Principal
		admin  bool
		want   refusal
	}{
		{
			name:   "an account administrator makes no token for an operator service principal",
			caller: f.account, target: f.ops, admin: false,
			want: exceeds("server:backup on the installation"),
		},
		{
			name:   "an account administrator makes a token for a service principal that holds less",
			caller: f.account, target: f.plain, admin: false, want: passes(),
		},
		{
			name:   "an account administrator makes a token for a service principal that holds nothing",
			caller: f.account, target: f.bare, admin: false, want: passes(),
		},
		{
			name:   "a site administrator makes a token for an operator service principal",
			caller: f.site, target: f.ops, admin: false, want: passes(),
		},
		{
			name:   "a bootstrap administrator makes a token for an operator service principal",
			caller: f.account, target: f.ops, admin: true, want: passes(),
		},
		{
			name:   "a holder on the principal makes its token",
			caller: f.holder, target: f.ops, admin: false, want: passes(),
		},
		{
			name:   "a holder on the principal through a group makes its token",
			caller: f.member, target: f.ops, admin: false, want: passes(),
		},
		{
			name:   "a holder on the installation makes a token for a service principal that holds nothing",
			caller: f.tm, target: f.bare, admin: false, want: passes(),
		},
		{
			name:   "a principal without the action makes no token",
			caller: f.bob,
			target: f.ops,
			admin:  false,
			want:   refusal{refused: true, reachExceeded: false, missing: nil},
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := as(tc.caller)
			if tc.admin {
				p = admin()
			}

			tok, err := f.createAs(ctx, p, tc.target)

			checkChange(t, err, tc.target.ID, tc.want)

			if !tc.want.refused && tok.PrincipalID != tc.target.ID {
				t.Fatalf("token = %+v, want a token of %q", tok, tc.target.ID)
			}
		})
	}
}

// TestServiceTokenReachOnRotate proves that a rotation follows the reach
// rule of a create.
func TestServiceTokenReachOnRotate(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	tests := []struct {
		name   string
		target principal.Principal
		want   refusal
	}{
		{
			name: "a rotation of an operator service principal is refused", target: f.ops,
			want: exceeds("server:backup on the installation"),
		},
		{name: "a rotation of a service principal that holds less passes", target: f.plain, want: passes()},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			made, _, err := f.tokens.Create(ctx, admin(), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: tc.target.ID, Name: "ci"})
			if err != nil {
				t.Fatalf("create by the administrator: %v", err)
			}

			_, _, err = f.tokens.Rotate(ctx, as(f.account), caller(), accesstoken.RotateInput{
				ID: made.ID, Scope: accesstoken.ScopeService, ExpiresAt: time.Time{}, OldEndsAt: nil,
			})

			checkChange(t, err, tc.target.ID, tc.want)
		})
	}
}

// TestDirectTokenHolderIsNotCompared proves that a holder of
// principal:manage-tokens on the principal itself is not compared: it
// makes the token although a read of the reach of the principal fails.
func TestDirectTokenHolderIsNotCompared(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	svc := f.newService(t, access.ReachConfig{
		Grants: f.store, Roles: f.store, Callers: f.store,
		Principals: failingPrincipals{Store: f.store}, Collections: nil, Units: f.store,
	})

	_, _, err := svc.Create(ctx, as(f.holder), caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.ops.ID, Name: "ci"})
	if err != nil {
		t.Fatalf("create by a holder on the principal = %v, want it to pass", err)
	}
}

// TestInstallationAndPrincipalHolderIsNotCompared proves that a caller that
// holds principal:manage-tokens on the installation and on the target
// principal is not compared at token time: the hold on the principal decides
// alone, directly or through a group. The compare of the reach at grant time
// is what protects this path: the grants of this setup come from an
// administrator, whose reach holds the reach of the operator principal.
func TestInstallationAndPrincipalHolderIsNotCompared(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	direct := f.personOf(t, "both-direct@example.com")
	f.grantTokenRoleAs(t, admin(), principalSubject(direct), identity.ServerObject())
	f.grantTokenRoleAs(t, admin(), principalSubject(direct), identity.PrincipalObject(f.ops.ID))

	group, err := f.principals.CreateGroup(ctx, caller(), principal.GroupInput{Name: "both-holders", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	member := f.personOf(t, "both-group@example.com")

	if _, err := f.principals.AddMember(ctx, caller(), admin(), group.ID, member.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	groupSubject := access.Subject{Kind: access.SubjectGroup, ID: group.ID}
	f.grantTokenRoleAs(t, admin(), groupSubject, identity.ServerObject())
	f.grantTokenRoleAs(t, admin(), groupSubject, identity.PrincipalObject(f.ops.ID))

	// The principals port fails: a compare would refuse the change.
	svc := f.newService(t, access.ReachConfig{
		Grants: f.store, Roles: f.store, Callers: f.store,
		Principals: failingPrincipals{Store: f.store}, Collections: nil, Units: f.store,
	})

	for _, tc := range []struct {
		name   string
		caller principal.Principal
	}{
		{name: "a direct hold", caller: direct},
		{name: "a hold through a group", caller: member},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := svc.Create(ctx, as(tc.caller), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.ops.ID, Name: "ci"})
			if err != nil {
				t.Fatalf("create by a holder on the installation and the principal = %v, want it to pass", err)
			}
		})
	}
}

// TestServiceTokenReachFailsClosed proves that a zero or incomplete reach
// config, and a read that fails, refuse the change.
func TestServiceTokenReachFailsClosed(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	// The target of the last case holds viewer on a unit of the namespace
	// acme. The unit port of that case reports the unit as current, so the
	// reach of the target holds the unit, and its collection port fails:
	// the compare of the reaches reads the collection of the unit.
	ns := namespace.Namespace{
		CreatedAt:           f.clock.Now(),
		ID:                  namespace.NewID(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}

	if err := f.store.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	unit := f.serviceOf(t, "ci-unit")

	unitObj, err := identity.UnitObject("pulumi", ns.Name, "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	unitObj = unitObj.WithUnitID("unit-live")

	f.grant(t, "grt-unit", access.Subject{Kind: access.SubjectPrincipal, ID: unit.ID}, access.RoleViewer, unitObj)

	tests := []struct {
		name       string
		reach      access.ReachConfig
		principals accesstoken.PrincipalReader
		target     principal.Principal
	}{
		{
			name: "a zero reach config",
			reach: access.ReachConfig{
				Grants:      nil,
				Roles:       nil,
				Callers:     nil,
				Principals:  nil,
				Collections: nil,
				Units:       nil,
			},
			principals: nil,
			target:     f.plain,
		},
		{
			name: "a reach config with no principals",
			reach: access.ReachConfig{
				Grants: f.store, Roles: f.store, Callers: f.store, Principals: nil, Collections: nil, Units: f.store,
			},
			principals: nil,
			target:     f.plain,
		},
		{
			name: "a failed read of the grants of the caller",
			reach: access.ReachConfig{
				Grants: failingGrants{Store: f.store}, Roles: f.store, Callers: f.store, Principals: f.store,
				Collections: nil, Units: f.store,
			},
			principals: nil,
			target:     f.plain,
		},
		{
			name: "a failed read of the grants of the principal",
			reach: access.ReachConfig{
				Grants: failingTargetGrants{Store: f.store, id: f.plain.ID}, Roles: f.store, Callers: f.store,
				Principals: f.store, Collections: nil, Units: f.store,
			},
			principals: nil,
			target:     f.plain,
		},
		{
			name: "a failed read of the principals",
			reach: access.ReachConfig{
				Grants: f.store, Roles: f.store, Callers: f.store, Principals: failingPrincipals{Store: f.store},
				Collections: nil, Units: f.store,
			},
			principals: nil,
			target:     f.plain,
		},
		{
			name: "a failed read of the principal of the caller",
			reach: access.ReachConfig{
				Grants:      f.store,
				Roles:       f.store,
				Callers:     f.store,
				Principals:  f.store,
				Collections: nil,
				Units:       f.store,
			},
			principals: failingSubject{Store: f.store},
			target:     f.plain,
		},
		{
			name: "a failed read of the collection of a unit",
			reach: access.ReachConfig{
				Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store,
				Collections: failingCollections{}, Units: liveUnits{id: unitObj.UnitID(), obj: unitObj},
			},
			principals: nil,
			target:     unit,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			principals := tc.principals
			if principals == nil {
				principals = f.store
			}

			svc := f.newServiceWith(t, tc.reach, principals)

			_, _, err := svc.Create(ctx, as(f.account), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: tc.target.ID, Name: "ci"})

			if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, accesstoken.ErrReachExceeded) {
				t.Fatalf("create with %s = %v, want ErrDenied and ErrReachExceeded", tc.name, err)
			}

			want := fmt.Sprintf("cannot check the reach of principal %q", tc.target.ID)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("create with %s = %q, want it to hold %q", tc.name, err, want)
			}
		})
	}
}

// TestReachRefusalNamesOnlyReadableObjects proves that the refusal of the
// reach check names the object of an action only when the reach of the
// caller holds the read action of that object: a namespace that the caller
// cannot read stays unnamed, and one that it reads is named.
func TestReachRefusalNamesOnlyReadableObjects(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	ns := namespace.Namespace{
		CreatedAt:           f.clock.Now(),
		ID:                  namespace.NewID(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}

	if err := f.store.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	// The reader holds the token role on the installation and reads the
	// namespace, so a refusal may name it. The token manager reads no
	// namespace.
	reader := f.personOf(t, "reader@example.com")

	f.grant(t, "grt-reader-token", access.Subject{Kind: access.SubjectPrincipal, ID: reader.ID},
		access.RoleTokenManager, identity.ServerObject())
	f.grant(t, "grt-reader-viewer", access.Subject{Kind: access.SubjectPrincipal, ID: reader.ID},
		access.RoleViewer, identity.NamespaceObject(ns.Name))

	// The blind principal reaches the reads of the namespace, which the
	// token manager does not hold. The writer principal reaches more than
	// the reader holds.
	blind := f.serviceOf(t, "ci-blind")
	f.grant(t, "grt-blind-viewer", access.Subject{Kind: access.SubjectPrincipal, ID: blind.ID},
		access.RoleViewer, identity.NamespaceObject(ns.Name))

	writer := f.serviceOf(t, "ci-writer")
	f.grant(t, "grt-writer-writer", access.Subject{Kind: access.SubjectPrincipal, ID: writer.ID},
		access.RoleWriter, identity.NamespaceObject(ns.Name))

	// The wide principal reaches the reads of two namespaces that the token
	// manager cannot read. The refusal must not tell how many there are.
	other := ns
	other.ID = namespace.NewID()
	other.Name = "globex"

	if err := f.store.CreateNamespace(ctx, other); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	wide := f.serviceOf(t, "ci-wide")
	f.grant(t, "grt-wide-viewer-acme", access.Subject{Kind: access.SubjectPrincipal, ID: wide.ID},
		access.RoleViewer, identity.NamespaceObject(ns.Name))
	f.grant(t, "grt-wide-viewer-globex", access.Subject{Kind: access.SubjectPrincipal, ID: wide.ID},
		access.RoleViewer, identity.NamespaceObject(other.Name))

	const hidden = "objects that you cannot read"

	tests := []struct {
		name    string
		caller  principal.Principal
		target  principal.Principal
		want    []string
		notWant []string
	}{
		{
			name:   "a caller that cannot read the namespace",
			caller: f.tm, target: blind,
			want: []string{hidden}, notWant: []string{ns.Name.String()},
		},
		{
			name:   "a caller that reads the namespace",
			caller: reader, target: writer,
			want: []string{fmt.Sprintf("namespace %q", ns.Name)}, notWant: []string{hidden},
		},
		{
			name:   "a caller that cannot read two namespaces",
			caller: f.tm, target: wide,
			want: []string{hidden}, notWant: []string{ns.Name.String(), other.Name.String()},
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := f.createAs(ctx, as(tc.caller), tc.target)
			if !errors.Is(err, accesstoken.ErrReachExceeded) {
				t.Fatalf("create = %v, want accesstoken.ErrReachExceeded", err)
			}

			checkRefusalText(t, err, tc.want, tc.notWant)

			if n := strings.Count(err.Error(), hidden); n > 1 {
				t.Errorf("refusal %q holds %q %d times, want at most once", err, hidden, n)
			}
		})
	}
}

// checkRefusalText checks that the refusal err holds every text of want,
// and no text of notWant.
func checkRefusalText(t *testing.T, err error, want, notWant []string) {
	t.Helper()

	for _, text := range want {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("refusal %q does not hold %q", err, text)
		}
	}

	for _, text := range notWant {
		if strings.Contains(err.Error(), text) {
			t.Errorf("refusal %q holds %q", err, text)
		}
	}
}

// TestBootstrapAdministratorPassesTheReachCheck proves that a caller with
// Admin set passes the reach check: the check never reads the reach of such
// a caller, so a zero reach config does not refuse it.
func TestBootstrapAdministratorPassesTheReachCheck(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)

	svc := f.newService(t, access.ReachConfig{
		Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
	})

	p := admin()

	_, _, err := svc.Create(t.Context(), p, caller(), accesstoken.ScopeService,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.ops.ID, Name: "ci"})
	if err != nil {
		t.Fatalf("create by a bootstrap administrator = %v, want it to pass", err)
	}
}

// TestCheckReachFollowsTheTokenRule proves that CheckReach answers the
// reach rule of a create or a rotation of a service token for a caller that
// holds principal:manage-tokens on the installation: the refusal wraps
// identity.ErrDenied and accesstoken.ErrReachExceeded and names the actions
// of the principal that the caller does not hold, and a holder of the
// action on the principal itself passes.
func TestCheckReachFollowsTheTokenRule(t *testing.T) {
	t.Parallel()

	f := newReachFixture(t)
	ctx := t.Context()

	tests := []struct {
		name   string
		caller principal.Principal
		target principal.Principal
		want   refusal
	}{
		{
			name:   "an account administrator checks an operator service principal",
			caller: f.account, target: f.ops, want: exceeds("server:backup on the installation"),
		},
		{
			name:   "an account administrator checks a service principal that holds less",
			caller: f.account, target: f.plain, want: passes(),
		},
		{
			name:   "a holder on the principal checks the operator service principal",
			caller: f.holder, target: f.ops, want: passes(),
		},
		{
			name:   "a holder on the installation checks a service principal that holds nothing",
			caller: f.tm, target: f.bare, want: passes(),
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkChange(t, f.tokens.CheckReach(ctx, as(tc.caller), tc.target.ID), tc.target.ID, tc.want)
		})
	}
}
