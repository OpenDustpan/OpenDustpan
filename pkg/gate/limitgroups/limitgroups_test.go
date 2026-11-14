package limitgroups_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/limitgroups"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errRead is the error of a read that fails.
var errRead = errors.New("read failed")

// fakeStore is the access limits, gates and approval rules of an
// installation, and the error of their read.
type fakeStore struct {
	gate.Store

	limits   []gate.AccessLimit
	gates    []gate.Gate
	rules    []gate.ApprovalRule
	err      error
	gatesErr error
	rulesErr error
}

func (s fakeStore) AccessLimits() gate.Objects[gate.AccessLimit] {
	return fakeLimits{Objects: nil, store: s}
}

func (s fakeStore) Gates() gate.Objects[gate.Gate] {
	return fakeGates{Objects: nil, store: s}
}

func (s fakeStore) ApprovalRules() gate.Objects[gate.ApprovalRule] {
	return fakeRules{Objects: nil, store: s}
}

// fakeLimits is the port of the access limits of fakeStore.
type fakeLimits struct {
	gate.Objects[gate.AccessLimit]

	store fakeStore
}

func (l fakeLimits) ListEveryScope(context.Context) ([]gate.AccessLimit, error) {
	if l.store.err != nil {
		return nil, l.store.err
	}

	return l.store.limits, nil
}

// fakeGates is the port of the gates of fakeStore.
type fakeGates struct {
	gate.Objects[gate.Gate]

	store fakeStore
}

func (g fakeGates) ListEveryScope(context.Context) ([]gate.Gate, error) {
	if g.store.gatesErr != nil {
		return nil, g.store.gatesErr
	}

	return g.store.gates, nil
}

// fakeRules is the port of the approval rules of fakeStore.
type fakeRules struct {
	gate.Objects[gate.ApprovalRule]

	store fakeStore
}

func (r fakeRules) ListEveryScope(context.Context) ([]gate.ApprovalRule, error) {
	if r.store.rulesErr != nil {
		return nil, r.store.rulesErr
	}

	return r.store.rules, nil
}

// fakeGroups reads the group of an ID from a table of names.
type fakeGroups struct {
	names map[string]string
	err   error
}

func (g fakeGroups) GetGroup(_ context.Context, id string) (principal.Group, error) {
	if g.err != nil {
		return principal.Group{}, g.err
	}

	name, ok := g.names[id]
	if !ok {
		return principal.Group{}, principal.ErrNotFound
	}

	return principal.Group{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, ID: id, Name: name, Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}, nil
}

// limit returns one access limit with the text text.
func limit(scope gate.Scope, id, text string) gate.AccessLimit {
	return gate.AccessLimit{Scope: scope, ID: gate.ID(id), Version: 1, Policy: text}
}

// TestLimitLiftedBy checks which access limit each change of the members of
// a group can lift, for each text of an access limit.
func TestLimitLiftedBy(t *testing.T) {
	t.Parallel()

	installation := gate.InstallationScope()
	acme := gate.NamespaceScope("acme")

	cases := []struct {
		name    string
		group   string
		newName string
		limits  []gate.AccessLimit
		change  principal.GroupChange
		want    access.LimitRef
		lifted  bool
	}{
		{
			name:  "an unless clause makes an add lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "an unless clause makes no remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "a when clause makes a remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "a when clause makes no add lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "the principal scope makes a remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal in OpenDustpan::Group::"sre", action, resource);`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "a set in a clause makes a remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in [`+
					`OpenDustpan::Group::"platform", OpenDustpan::Group::"sre"] };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "the delete of a group counts as a remove",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeDelete,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "an unknown change refuses a limit that names the group",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChange(99),
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "the limit of another group does not lift",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "the limit of the group lifts",
			group: "grp-2",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:    "an installation with no limit lifts nothing",
			group:   "grp-1",
			limits:  nil,
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "a limit that names no group lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { context.principal.kind == "service" };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "a name of a principal is not a group",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal == OpenDustpan::Principal::"sre", action, resource);`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "the limits of two scopes are both read",
			group: "grp-1",
			limits: []gate.AccessLimit{
				limit(installation, "l1",
					`forbid (principal, action, resource) when { context.principal.kind == "service" };`),
				limit(acme, "l2",
					`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`),
			},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    ref("l2", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "the first limit that lifts decides",
			group: "grp-1",
			limits: []gate.AccessLimit{
				limit(acme, "l1",
					`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`),
				limit(acme, "l2",
					`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`),
			},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "a limit that cannot lift is skipped",
			group: "grp-1",
			limits: []gate.AccessLimit{
				limit(acme, "l1",
					`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`),
				limit(acme, "l2",
					`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`),
			},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    ref("l2", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "a rename from the name of a group of a when clause lifts the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRename,
			newName: "sre-two",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "a rename to the name of a group of an unless clause lifts the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeRename,
			newName: "platform",
			want:    ref("l1", access.LimitKindAccessLimit),
			lifted:  true,
		},
		{
			name:  "a rename from the name of a group of an unless clause lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRename,
			newName: "sre-two",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "a rename to the name of a group of a when clause lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeRename,
			newName: "platform",
			want:    noRef(),
			lifted:  false,
		},
		{
			name:  "a rename that no limit names lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeRename,
			newName: "sre-two",
			want:    noRef(),
			lifted:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			groups := fakeGroups{names: map[string]string{"grp-1": "sre", "grp-2": "platform"}, err: nil}
			reader := limitgroups.New(storeOf(tc.limits, nil, nil), groups)

			got, lifted, err := reader.LimitLiftedBy(t.Context(), tc.group, tc.newName, tc.change)
			if err != nil {
				t.Fatalf("LimitLiftedBy(%s, %q) = %v, want nil", tc.group, tc.newName, err)
			}

			if lifted != tc.lifted || got != tc.want {
				t.Fatalf("LimitLiftedBy(%s, %q) = %v, %t, want %v, %t",
					tc.group, tc.newName, got, lifted, tc.want, tc.lifted)
			}
		})
	}
}

// gateAt returns one gate of the namespace acme with the clauses condition
// and unless.
func gateAt(condition, unless string) gate.Gate {
	return gate.Gate{
		Scope: gate.NamespaceScope("acme"), ID: "g1", Version: 1, DecisionPoint: gate.PointChangePlanned,
		Condition: condition, Unless: unless, Outcome: gate.OutcomeDeny, Message: "",
		Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{
			Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0},
		},
		State: "", Reads: nil,
	}
}

// ruleAt returns one approval rule of the namespace acme with the approver
// group IDs groupIDs.
func ruleAt(groupIDs []string) gate.ApprovalRule {
	return gate.ApprovalRule{
		Scope: gate.NamespaceScope("acme"), ID: "r1", Version: 1,
		Approvers: gate.Approvers{Groups: nil, GroupIDs: groupIDs, Users: nil, UnitOwners: false},
		Count:     1, AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
	}
}

// ruleNamed returns one approval rule of the namespace acme with the approver
// group names groups and no group IDs.
func ruleNamed(groups []string) gate.ApprovalRule {
	return gate.ApprovalRule{
		Scope: gate.NamespaceScope("acme"), ID: "r1", Version: 1,
		Approvers: gate.Approvers{Groups: groups, GroupIDs: nil, Users: nil, UnitOwners: false},
		Count:     1, AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
	}
}

// noRef is the ref of no object.
func noRef() access.LimitRef {
	var none access.LimitRef

	return none
}

// ref returns the ref of the object id of acme with the kind kind.
func ref(id string, kind access.LimitKind) access.LimitRef {
	return access.LimitRef{ID: id, Scope: "namespace/acme", Kind: kind}
}

// storeOf returns a fakeStore of the given objects.
func storeOf(limits []gate.AccessLimit, gates []gate.Gate, rules []gate.ApprovalRule) fakeStore {
	return fakeStore{
		Store: nil, limits: limits, gates: gates, rules: rules,
		err: nil, gatesErr: nil, rulesErr: nil,
	}
}

// storeFailing returns a fakeStore whose reads fail with the given errors.
func storeFailing(limitsErr, gatesErr, rulesErr error) fakeStore {
	return fakeStore{
		Store: nil, limits: nil, gates: nil, rules: nil,
		err: limitsErr, gatesErr: gatesErr, rulesErr: rulesErr,
	}
}

// TestGateAndRuleLiftedBy checks which gate and which approval rule each
// change of the members of a group can widen access through.
func TestGateAndRuleLiftedBy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		group   string
		newName string
		store   fakeStore
		change  principal.GroupChange
		want    access.LimitRef
		lifted  bool
	}{
		{
			name:    "an unless clause of a gate makes an add lift the gate",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt("", `principal in OpenDustpan::Group::"ops"`)}, nil),
			change:  principal.GroupChangeAddMember,
			want:    ref("g1", access.LimitKindGate),
			lifted:  true,
		},
		{
			name:    "an unless clause of a gate makes no remove lift the gate",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt("", `principal in OpenDustpan::Group::"ops"`)}, nil),
			change:  principal.GroupChangeRemoveMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "a when clause of a gate makes a remove lift the gate",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt(`principal in OpenDustpan::Group::"ops"`, "")}, nil),
			change:  principal.GroupChangeRemoveMember,
			want:    ref("g1", access.LimitKindGate),
			lifted:  true,
		},
		{
			name:    "a gate that names another group does not lift",
			group:   "grp-1",
			newName: "",
			store: storeOf(nil, []gate.Gate{
				gateAt("", `principal in OpenDustpan::Group::"platform"`),
			}, nil),
			change: principal.GroupChangeAddMember,
			want:   noRef(),
			lifted: false,
		},
		{
			name:    "a gate whose condition is the principal variable names no group",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt(`principal in principal`, "")}, nil),
			change:  principal.GroupChangeAddMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "a gate whose condition is the resource variable names no group",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt(`principal in resource`, "")}, nil),
			change:  principal.GroupChangeAddMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "a gate whose condition is a set of the principal variable names no group",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt(`principal in [principal]`, "")}, nil),
			change:  principal.GroupChangeAddMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "a gate whose condition is a principal entity names no group",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt(`principal in OpenDustpan::Principal::"olga"`, "")}, nil),
			change:  principal.GroupChangeAddMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "a gate whose unless is the principal variable names no group",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, []gate.Gate{gateAt("", `principal in principal`)}, nil),
			change:  principal.GroupChangeAddMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "a rename to the new name of an unless clause of a gate lifts",
			group:   "grp-1",
			newName: "auditors",
			store: storeOf(nil, []gate.Gate{
				gateAt("", `principal in OpenDustpan::Group::"auditors"`),
			}, nil),
			change: principal.GroupChangeRename,
			want:   ref("g1", access.LimitKindGate),
			lifted: true,
		},
		{
			name:    "an approval rule with the ID of the group makes an add lift it",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, nil, []gate.ApprovalRule{ruleAt([]string{"grp-1"})}),
			change:  principal.GroupChangeAddMember,
			want:    ref("r1", access.LimitKindApprovalRule),
			lifted:  true,
		},
		{
			name:    "an approval rule makes no remove lift it",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, nil, []gate.ApprovalRule{ruleAt([]string{"grp-1"})}),
			change:  principal.GroupChangeRemoveMember,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "an approval rule makes no rename lift it",
			group:   "grp-1",
			newName: "ops-two",
			store:   storeOf(nil, nil, []gate.ApprovalRule{ruleAt([]string{"grp-1"})}),
			change:  principal.GroupChangeRename,
			want:    noRef(),
			lifted:  false,
		},
		{
			name:    "an approval rule that names no group ID counts no one",
			group:   "grp-1",
			newName: "",
			store:   storeOf(nil, nil, []gate.ApprovalRule{ruleNamed([]string{"ops"})}),
			change:  principal.GroupChangeAddMember,
			want:    noRef(),
			lifted:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			groups := fakeGroups{names: map[string]string{"grp-1": "ops", "grp-2": "platform"}, err: nil}
			reader := limitgroups.New(tc.store, groups)

			got, lifted, err := reader.LimitLiftedBy(t.Context(), tc.group, tc.newName, tc.change)
			if err != nil {
				t.Fatalf("LimitLiftedBy(%s, %q) = %v, want nil", tc.group, tc.newName, err)
			}

			if lifted != tc.lifted || got != tc.want {
				t.Fatalf("LimitLiftedBy(%s, %q) = %v, %t, want %v, %t",
					tc.group, tc.newName, got, lifted, tc.want, tc.lifted)
			}
		})
	}
}

// TestGateAndRuleLiftedByErrors checks that the reader fails closed for a
// gate and an approval rule that it cannot read or parse.
func TestGateAndRuleLiftedByErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		store fakeStore
	}{
		{
			name:  "a gate clause that does not parse",
			store: storeOf(nil, []gate.Gate{gateAt(`principal in`, "")}, nil),
		},
		{
			name: "a gate whose membership test holds a group in another shape",
			store: storeOf(nil, []gate.Gate{gateAt(
				`principal in (if true then OpenDustpan::Group::"sre" else OpenDustpan::Group::"sre")`, "",
			)}, nil),
		},
		{
			name: "a gate whose membership test mixes a group and a principal",
			store: storeOf(nil, []gate.Gate{gateAt(
				`principal in [OpenDustpan::Group::"sre", OpenDustpan::Principal::"olga"]`, "",
			)}, nil),
		},
		{
			name:  "a read of the gates that fails",
			store: storeFailing(nil, errRead, nil),
		},
		{
			name:  "a read of the approval rules that fails on an add",
			store: storeFailing(nil, nil, errRead),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			groups := fakeGroups{names: map[string]string{"grp-1": "ops"}, err: nil}
			reader := limitgroups.New(tc.store, groups)

			got, lifted, err := reader.LimitLiftedBy(
				t.Context(), "grp-1", "", principal.GroupChangeAddMember,
			)
			if err == nil {
				t.Fatalf("LimitLiftedBy(grp-1) = %v, %t, nil, want an error", got, lifted)
			}

			if lifted || got != noRef() {
				t.Fatalf("LimitLiftedBy(grp-1) = %v, %t with the error %v, want no object", got, lifted, err)
			}
		})
	}
}

// TestLimitLiftedByErrors checks that the reader fails closed: a read that
// fails, a limit that does not parse, and a limit that holds a second
// policy are errors.
func TestLimitLiftedByErrors(t *testing.T) {
	t.Parallel()

	groups := fakeGroups{names: map[string]string{"grp-1": "sre"}, err: nil}

	cases := []struct {
		name   string
		store  fakeStore
		groups fakeGroups
	}{
		{
			name:   "the group read fails",
			store:  storeOf(nil, nil, nil),
			groups: fakeGroups{names: nil, err: errRead},
		},
		{
			name:   "the limits read fails",
			store:  storeFailing(errRead, nil, nil),
			groups: groups,
		},
		{
			name: "a limit does not parse",
			store: storeOf([]gate.AccessLimit{
				limit(gate.NamespaceScope("acme"), "l1", `forbid (principal, action, resource) unless { principal in`),
			}, nil, nil),
			groups: groups,
		},
		{
			name: "a limit holds a second policy",
			store: storeOf([]gate.AccessLimit{
				limit(gate.NamespaceScope("acme"), "l1",
					`forbid (principal, action, resource); forbid (principal, action, resource);`),
			}, nil, nil),
			groups: groups,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := limitgroups.New(tc.store, tc.groups)

			got, lifted, err := reader.LimitLiftedBy(t.Context(), "grp-1", "", principal.GroupChangeAddMember)
			if err == nil {
				t.Fatalf("LimitLiftedBy(grp-1) = %v, %t, nil, want an error", got, lifted)
			}

			if lifted || got != noRef() {
				t.Fatalf("LimitLiftedBy(grp-1) = %v, %t with the error %v, want no limit", got, lifted, err)
			}
		})
	}
}

// TestLimitLiftedByWithoutTheGroup checks that a group that does not exist
// lifts no limit: the change of its members fails with the error of the
// group, so the guard has nothing to refuse.
func TestLimitLiftedByWithoutTheGroup(t *testing.T) {
	t.Parallel()

	reader := limitgroups.New(
		storeOf(nil, nil, nil),
		fakeGroups{names: map[string]string{}, err: nil},
	)

	got, lifted, err := reader.LimitLiftedBy(t.Context(), "grp-gone", "", principal.GroupChangeAddMember)
	if err != nil {
		t.Fatalf("LimitLiftedBy(grp-gone) = %v, want nil", err)
	}

	if lifted || got != noRef() {
		t.Fatal("LimitLiftedBy(grp-gone) = a limit, want none")
	}
}
