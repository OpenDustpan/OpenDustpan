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

// fakeStore is the access limits of an installation, and the error of their
// read.
type fakeStore struct {
	gate.Store

	limits []gate.AccessLimit
	err    error
}

func (s fakeStore) AccessLimits() gate.Objects[gate.AccessLimit] {
	return fakeLimits{Objects: nil, store: s}
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
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "an unless clause makes no remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "a when clause makes a remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "a when clause makes no add lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "the principal scope makes a remove lift the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal in OpenDustpan::Group::"sre", action, resource);`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
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
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "the delete of a group counts as a remove",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeDelete,
			newName: "",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "an unknown change refuses a limit that names the group",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChange(99),
			newName: "",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "the limit of another group does not lift",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "the limit of the group lifts",
			group: "grp-2",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:    "an installation with no limit lifts nothing",
			group:   "grp-1",
			limits:  nil,
			change:  principal.GroupChangeAddMember,
			newName: "",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "a limit that names no group lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { context.principal.kind == "service" };`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "a name of a principal is not a group",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal == OpenDustpan::Principal::"sre", action, resource);`)},
			change:  principal.GroupChangeRemoveMember,
			newName: "",
			want:    access.LimitRef{ID: "", Scope: ""},
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
			want:    access.LimitRef{ID: "l2", Scope: "namespace/acme"},
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
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
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
			want:    access.LimitRef{ID: "l2", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "a rename from the name of a group of a when clause lifts the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRename,
			newName: "sre-two",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "a rename to the name of a group of an unless clause lifts the forbid",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeRename,
			newName: "platform",
			want:    access.LimitRef{ID: "l1", Scope: "namespace/acme"},
			lifted:  true,
		},
		{
			name:  "a rename from the name of a group of an unless clause lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"sre" };`)},
			change:  principal.GroupChangeRename,
			newName: "sre-two",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "a rename to the name of a group of a when clause lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeRename,
			newName: "platform",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
		{
			name:  "a rename that no limit names lifts nothing",
			group: "grp-1",
			limits: []gate.AccessLimit{limit(acme, "l1",
				`forbid (principal, action, resource) when { principal in OpenDustpan::Group::"platform" };`)},
			change:  principal.GroupChangeRename,
			newName: "sre-two",
			want:    access.LimitRef{ID: "", Scope: ""},
			lifted:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			groups := fakeGroups{names: map[string]string{"grp-1": "sre", "grp-2": "platform"}, err: nil}
			reader := limitgroups.New(fakeStore{Store: nil, limits: tc.limits, err: nil}, groups)

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
			store:  fakeStore{Store: nil, limits: nil, err: nil},
			groups: fakeGroups{names: nil, err: errRead},
		},
		{
			name:   "the limits read fails",
			store:  fakeStore{Store: nil, limits: nil, err: errRead},
			groups: groups,
		},
		{
			name: "a limit does not parse",
			store: fakeStore{Store: nil, err: nil, limits: []gate.AccessLimit{
				limit(gate.NamespaceScope("acme"), "l1", `forbid (principal, action, resource) unless { principal in`),
			}},
			groups: groups,
		},
		{
			name: "a limit holds a second policy",
			store: fakeStore{Store: nil, err: nil, limits: []gate.AccessLimit{
				limit(gate.NamespaceScope("acme"), "l1",
					`forbid (principal, action, resource); forbid (principal, action, resource);`),
			}},
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

			if lifted || got != (access.LimitRef{ID: "", Scope: ""}) {
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
		fakeStore{Store: nil, limits: nil, err: nil},
		fakeGroups{names: map[string]string{}, err: nil},
	)

	got, lifted, err := reader.LimitLiftedBy(t.Context(), "grp-gone", "", principal.GroupChangeAddMember)
	if err != nil {
		t.Fatalf("LimitLiftedBy(grp-gone) = %v, want nil", err)
	}

	if lifted || got != (access.LimitRef{ID: "", Scope: ""}) {
		t.Fatal("LimitLiftedBy(grp-gone) = a limit, want none")
	}
}
