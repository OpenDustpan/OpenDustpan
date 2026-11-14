package dustpan

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// groupLimitFixture is an installation whose access limits name the group
// sre in an unless clause and the group platform in a when clause, the
// groups, the member, and a caller who holds server:manage-groups and
// nothing else.
type groupLimitFixture struct {
	svc *principal.Service
	// store holds the access limits, so a test writes one that the save
	// refuses, as a restore writes it.
	store  *sqlite.Store
	caller identity.Principal
	// member is a member of each group, so a remove and a delete find it.
	member principal.Principal
	// added is no member of any group.
	added principal.Principal
	// named is the group sre of the unless clause: an add can lift it.
	named principal.Group
	// other is the group platform of the when clause: a remove can lift it.
	other principal.Group
	// free is the group readers, which no limit names.
	free principal.Group
	// gated is the group that the unless of a gate names: an add can lift
	// the gate.
	gated principal.Group
	// approved is the group that an approval rule names by ID: an add makes
	// the member an approver.
	approved principal.Group
}

// newGroupLimitFixture returns the fixture of TestGroupGuardReadsTheAccessLimits.
func newGroupLimitFixture(t *testing.T) groupLimitFixture {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.namespaceStore = st

	grants, err := newAccessService(cfg)
	if err != nil || grants == nil {
		t.Fatalf("newAccessService = %v, %v, want a service", grants, err)
	}

	svc, err := newPrincipalService(cfg, grants)
	if err != nil || svc == nil {
		t.Fatalf("newPrincipalService = %v, %v, want a service", svc, err)
	}

	seedNamingLimits(t, st)

	caller := identity.Principal{
		Subject: "grpadmin", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	stored := createPerson(t, svc, caller.Subject)
	grantGroupAdministrator(t, grants, stored.ID)

	member := createPerson(t, svc, "olga")
	named := createGroup(t, svc, "sre")
	other := createGroup(t, svc, "platform")
	free := createGroup(t, svc, "readers")
	gated := createGroup(t, svc, "gated")
	approved := createGroup(t, svc, "approved")

	// Each group holds the member, so a remove and a delete find it. The
	// administrator makes the adds: the guard of the caller refuses an add
	// to the group of the unless clause.
	for _, group := range []*principal.Group{&named, &other, &free, &gated, &approved} {
		if _, err := svc.AddMember(ctx, adminEntry(), admin(), group.ID, member.ID); err != nil {
			t.Fatalf("add the member to group %s: %v", group.Name, err)
		}
	}

	seedGateAndRule(t, st, gated.Name, approved.ID)

	return groupLimitFixture{
		svc:      svc,
		store:    st,
		caller:   caller,
		member:   member,
		added:    createPerson(t, svc, "peter"),
		named:    named,
		other:    other,
		free:     free,
		gated:    gated,
		approved: approved,
	}
}

// TestGroupGuardReadsTheAccessLimits proves the wiring and the rule: a
// caller who holds server:manage-groups and nothing else cannot make a
// change that can lift an access limit of a scope that names the group,
// and keeps each change that can only add a forbid, or that no limit names.
func TestGroupGuardReadsTheAccessLimits(t *testing.T) {
	t.Parallel()

	add := func(f groupLimitFixture, group principal.Group) error {
		_, err := f.svc.AddMember(t.Context(), adminEntry(), f.caller, group.ID, f.added.ID)

		return err
	}

	remove := func(f groupLimitFixture, group principal.Group) error {
		return f.svc.RemoveMember(t.Context(), adminEntry(), f.caller, group.ID, f.member.ID)
	}

	del := func(f groupLimitFixture, group principal.Group) error {
		return f.svc.DeleteGroup(t.Context(), adminEntry(), f.caller, group.ID)
	}

	rename := func(f groupLimitFixture, caller identity.Principal, group principal.Group, name string) error {
		_, err := f.svc.UpdateGroup(t.Context(), adminEntry(), caller, group.ID,
			principal.GroupUpdate{Name: &name, Description: nil, ClosedToIdentityProvider: nil})

		return err
	}

	describe := func(f groupLimitFixture, group principal.Group, description string) error {
		_, err := f.svc.UpdateGroup(t.Context(), adminEntry(), f.caller, group.ID,
			principal.GroupUpdate{Name: nil, Description: &description, ClosedToIdentityProvider: nil})

		return err
	}

	cases := []struct {
		name string
		run  func(f groupLimitFixture) error
		want error
	}{
		{
			name: "an add to a group of an unless clause is refused",
			run:  func(f groupLimitFixture) error { return add(f, f.named) },
			want: access.ErrEscalation,
		},
		{
			name: "a remove from a group of an unless clause keeps the grants check",
			run:  func(f groupLimitFixture) error { return remove(f, f.named) },
			want: nil,
		},
		{
			name: "an add to a group of a when clause keeps the grants check",
			run:  func(f groupLimitFixture) error { return add(f, f.other) },
			want: nil,
		},
		{
			name: "a remove from a group of a when clause is refused",
			run:  func(f groupLimitFixture) error { return remove(f, f.other) },
			want: access.ErrEscalation,
		},
		{
			name: "the delete of a group of an unless clause keeps the grants check",
			run:  func(f groupLimitFixture) error { return del(f, f.named) },
			want: nil,
		},
		{
			name: "the delete of a group of a when clause is refused",
			run:  func(f groupLimitFixture) error { return del(f, f.other) },
			want: access.ErrEscalation,
		},
		{
			name: "a group that no limit names keeps the grants check",
			run:  func(f groupLimitFixture) error { return add(f, f.free) },
			want: nil,
		},
		{
			name: "an add to a group of an unless clause of a gate is refused",
			run:  func(f groupLimitFixture) error { return add(f, f.gated) },
			want: access.ErrEscalation,
		},
		{
			name: "an add to a group of an approval rule is refused",
			run:  func(f groupLimitFixture) error { return add(f, f.approved) },
			want: access.ErrEscalation,
		},
		{
			name: "a rename to the name of a group of an unless clause is refused",
			run:  func(f groupLimitFixture) error { return rename(f, f.caller, f.free, f.named.Name) },
			want: access.ErrEscalation,
		},
		{
			name: "a rename to the name of a group that no group holds is refused",
			run:  func(f groupLimitFixture) error { return rename(f, f.caller, f.free, "auditors") },
			want: access.ErrEscalation,
		},
		{
			name: "a rename from the name of a group of a when clause is refused",
			run:  func(f groupLimitFixture) error { return rename(f, f.caller, f.other, "platform-two") },
			want: access.ErrEscalation,
		},
		{
			name: "a rename that no limit names keeps the grants check",
			run:  func(f groupLimitFixture) error { return rename(f, f.caller, f.free, "readers-two") },
			want: nil,
		},
		{
			name: "a description of a group of a when clause keeps the grants check",
			run:  func(f groupLimitFixture) error { return describe(f, f.other, "the platform group") },
			want: nil,
		},
		{
			name: "an administrator renames a group to a name of an unless clause",
			run:  func(f groupLimitFixture) error { return rename(f, admin(), f.free, "auditors") },
			want: nil,
		},
		{
			name: "an administrator adds to a group of an unless clause",
			run: func(f groupLimitFixture) error {
				_, err := f.svc.AddMember(t.Context(), adminEntry(), admin(), f.named.ID, f.added.ID)

				return err
			},
			want: nil,
		},
		{
			name: "an administrator removes from a group of a when clause",
			run: func(f groupLimitFixture) error {
				return f.svc.RemoveMember(t.Context(), adminEntry(), admin(), f.other.ID, f.member.ID)
			},
			want: nil,
		},
		{
			name: "an administrator adds to a group of an unless clause of a gate",
			run: func(f groupLimitFixture) error {
				_, err := f.svc.AddMember(t.Context(), adminEntry(), admin(), f.gated.ID, f.added.ID)

				return err
			},
			want: nil,
		},
		{
			name: "an administrator adds to a group of an approval rule",
			run: func(f groupLimitFixture) error {
				_, err := f.svc.AddMember(t.Context(), adminEntry(), admin(), f.approved.ID, f.added.ID)

				return err
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newGroupLimitFixture(t)

			if err := tc.run(f); !errors.Is(err, tc.want) {
				t.Fatalf("the change = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestGroupGuardRefusesALimitThatTheAPIWouldRefuse proves that the guard
// fails closed for an access limit that the save of the installation API
// refuses, written straight in the store as a restore writes it: one whose
// membership test of principal names no group, and one that does not parse.
// A caller who is not a site administrator cannot change the members of the
// group then, because the guard cannot tell whether the limit lifts for the
// member.
func TestGroupGuardRefusesALimitThatTheAPIWouldRefuse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		policy string
	}{
		{
			name:   "a membership test of an attribute of the resource",
			policy: `forbid (principal, action, resource) unless { principal in resource.owners };`,
		},
		{
			name: "a membership test of an attribute of the resource inside an if",
			policy: `forbid (principal, action, resource) unless { if principal in resource.owners ` +
				`then true else false };`,
		},
		{
			name: "a membership test of a set that holds a principal",
			policy: `forbid (principal, action, resource) unless { principal in [` +
				`OpenDustpan::Group::"sre", OpenDustpan::Principal::"olga"] };`,
		},
		{
			name:   "a policy that does not parse",
			policy: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newGroupLimitFixture(t)
			putLimit(t, f.store, gate.AccessLimit{
				Scope: gate.NamespaceScope("acme"), ID: "l9", Version: 0, Policy: tc.policy,
			})

			// No limit names the group readers, so only the limit that the
			// save refuses stops the add.
			_, err := f.svc.AddMember(t.Context(), adminEntry(), f.caller, f.free.ID, f.added.ID)
			if !errors.Is(err, access.ErrEscalation) {
				t.Fatalf("the add = %v, want ErrEscalation", err)
			}
		})
	}
}

// seedNamingLimits stores the namespace acme and the access limits of it:
// one that names the group sre in an unless clause, one that names the
// group platform in a when clause, and one that names the group auditors in
// an unless clause. No group holds the name auditors, so a group takes it
// only through a rename.
func seedNamingLimits(t *testing.T, st *sqlite.Store) {
	t.Helper()

	ctx := t.Context()

	nsErr := st.CreateNamespace(ctx, namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "acme",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if nsErr != nil {
		t.Fatalf("create namespace: %v", nsErr)
	}

	for _, limit := range []gate.AccessLimit{
		{
			Scope: gate.NamespaceScope("acme"), ID: "l1", Version: 0,
			Policy: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`,
		},
		{
			Scope: gate.NamespaceScope("acme"), ID: "l2", Version: 0,
			Policy: `forbid (principal, action, resource) when { principal in OpenDustpan::Group::"platform" };`,
		},
		{
			Scope: gate.NamespaceScope("acme"), ID: "l3", Version: 0,
			Policy: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"auditors" };`,
		},
	} {
		putLimit(t, st, limit)
	}
}

// seedGateAndRule stores the namespace acme and the gate and approval rule of
// it: the unless of the gate names the group gateName, and the approval rule
// names the group with the ID ruleGroupID as its approver.
func seedGateAndRule(t *testing.T, st *sqlite.Store, gateName, ruleGroupID string) {
	t.Helper()

	ctx := t.Context()
	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	e := namespaceChange("acme")

	_, err := st.Gates().Put(ctx, gate.Gate{
		Scope: gate.NamespaceScope("acme"), ID: "g1", Version: 0,
		DecisionPoint: gate.PointChangePlanned, Condition: "",
		Unless:  `principal in OpenDustpan::Group::"` + gateName + `"`,
		Outcome: gate.OutcomeDeny, Message: "", Mode: gate.ModeEnforce, OnError: gate.OnErrorFail,
		Final: false, Reads: nil,
		CheckedWith: gate.CheckedWith{
			Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0},
		},
		State: "",
	}, 0, fence, e)
	if err != nil {
		t.Fatalf("put gate: %v", err)
	}

	_, err = st.ApprovalRules().Put(ctx, gate.ApprovalRule{
		Scope: gate.NamespaceScope("acme"), ID: "r1", Version: 0,
		Approvers: gate.Approvers{
			Groups: []string{"approved"}, GroupIDs: []string{ruleGroupID}, Users: nil, UnitOwners: false,
		},
		Count: 1, AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
	}, 0, fence, e)
	if err != nil {
		t.Fatalf("put approval rule: %v", err)
	}
}

// putLimit stores one access limit of the namespace acme straight in the
// store, as a restore writes it. The store does not parse the policy: only
// the save of the installation API does.
func putLimit(t *testing.T, st *sqlite.Store, limit gate.AccessLimit) {
	t.Helper()

	_, err := st.AccessLimits().Put(t.Context(), limit, 0,
		namespace.Fence{Namespace: "acme", FailoverVersion: 1}, namespaceChange("acme"))
	if err != nil {
		t.Fatalf("put access limit %s: %v", limit.ID, err)
	}
}

// createGroup creates the group name and returns it.
func createGroup(t *testing.T, svc *principal.Service, name string) principal.Group {
	t.Helper()

	g, err := svc.CreateGroup(t.Context(), adminEntry(), principal.GroupInput{Name: name, Description: ""})
	if err != nil {
		t.Fatalf("create group %s: %v", name, err)
	}

	return g
}

// createPerson creates the principal subject and returns it.
func createPerson(t *testing.T, svc *principal.Service, subject string) principal.Principal {
	t.Helper()

	p, err := svc.CreatePrincipal(t.Context(), adminEntry(),
		principal.Input{Subject: subject, DisplayName: "", Kind: principal.KindPerson})
	if err != nil {
		t.Fatalf("create principal %s: %v", subject, err)
	}

	return p
}

// grantGroupAdministrator gives the principal principalID a role that holds
// server:manage-groups only.
func grantGroupAdministrator(t *testing.T, grants *access.Service, principalID string) {
	t.Helper()

	ctx := t.Context()

	role, err := grants.CreateRole(ctx, adminEntry(), admin(), access.RoleInput{
		Name: "group-administrator", Description: "", Actions: []identity.Action{identity.ActionServerManageGroups},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	grant := access.GrantInput{
		Object: identity.ServerObject(), Role: role.ID,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	_, err = grants.Grant(ctx, adminEntry(), admin(), grant)
	if err != nil {
		t.Fatalf("grant the role: %v", err)
	}
}

// admin returns the principal of a bootstrap administrator.
func admin() identity.Principal {
	return identity.Principal{
		Subject: "admin", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
}

// namespaceChange returns the audit entry of a change of the namespace ns.
func namespaceChange(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_760_000_000_000), Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-gates", Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}
