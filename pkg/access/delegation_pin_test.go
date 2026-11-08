package access_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// customRole makes a custom role with the actions, for the bootstrap admin.
func (f *fixture) customRole(tb testing.TB, actions ...identity.Action) access.Role {
	tb.Helper()

	r, err := f.svc.CreateRole(tb.Context(), testEntry(), admin(), access.RoleInput{
		Name: "custom", Description: "", Actions: actions,
	})
	if err != nil {
		tb.Fatalf("create role: %v", err)
	}

	return r
}

// grantedRole makes a custom role with the token action and gives it on the
// service principal prn-svc, as a grant that lets its holder act as that
// principal looks.
func (f *fixture) grantedRole(tb testing.TB) access.Role {
	tb.Helper()

	f.person(tb, "bob")
	f.installationService(tb, "prn-svc", "ci-svc")

	r := f.customRole(tb, identity.ActionPrincipalManageTokens)

	g, err := f.svc.Grant(tb.Context(), testEntry(), admin(), access.GrantInput{
		Object: identity.PrincipalObject("prn-svc"), Role: r.ID, Subject: prn("bob"),
	})
	if err != nil {
		tb.Fatalf("grant the role on the principal: %v", err)
	}

	if g.ID == "" {
		tb.Fatal("the grant of the token role on a principal stored no grant")
	}

	return r
}

// TestGrantOnAPrincipalTakesDelegationActionsOnly pins the rule of a grant
// on a principal object: the role gives principal actions only, and at
// least one. A role with a data action, a role with a server action and a
// role with no action are refused; a role with principal:manage-tokens
// alone is accepted.
func TestGrantOnAPrincipalTakesDelegationActionsOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		actions []identity.Action
		// stored writes the role with the store: CreateRole refuses a role
		// with no action.
		stored bool
		want   error
	}{
		{
			name: "a role with a data action", actions: []identity.Action{identity.ActionNamespaceRead},
			stored: false, want: access.ErrInvalid,
		},
		{
			name: "a role with a server action", actions: []identity.Action{identity.ActionServerBackup},
			stored: false, want: access.ErrInvalid,
		},
		{name: "a role with no action", actions: nil, stored: true, want: access.ErrInvalid},
		{
			name:    "a role with principal:manage-tokens only",
			actions: []identity.Action{identity.ActionPrincipalManageTokens}, stored: false, want: nil,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			ctx := t.Context()
			f.person(t, "bob")
			f.installationService(t, "prn-svc", "ci-svc")

			// A role with no action cannot come from CreateRole; a store of
			// another version can keep one.
			at := time.UnixMilli(1_700_000_000_000).UTC()
			role := access.Role{
				CreatedAt: at, UpdatedAt: at, ID: "rol-empty", Name: "empty", Description: "", Actions: tc.actions,
				BuiltIn: false,
			}

			if !tc.stored {
				role = f.customRole(t, tc.actions...)
			} else if err := f.store.CreateRole(ctx, role, storetest.ServerChange(at, access.RoleRef(role.ID))); err != nil {
				t.Fatalf("store the empty role: %v", err)
			}

			g, err := f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
				Object: identity.PrincipalObject("prn-svc"), Role: role.ID, Subject: prn("bob"),
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("grant of role %s on a principal = %v, want %v", role.ID, err, tc.want)
			}

			if tc.want == nil && g.ID == "" {
				t.Fatal("the grant of the token role on a principal stored no grant")
			}
		})
	}
}

// TestUpdateOfARoleOnAPrincipalKeepsTheDelegationFamily pins the rule of an
// update of a role that a grant gives on a principal: the role keeps
// principal actions only, and at least one. An update that adds a data
// action, one that adds a server action and one that removes every action
// are refused, and each refusal keeps the role as it was.
func TestUpdateOfARoleOnAPrincipalKeepsTheDelegationFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		actions []identity.Action
		want    error
	}{
		{
			name:    "an update that adds a data action",
			actions: []identity.Action{identity.ActionPrincipalManageTokens, identity.ActionNamespaceRead},
			want:    access.ErrInUse,
		},
		{
			name:    "an update that adds a server action",
			actions: []identity.Action{identity.ActionPrincipalManageTokens, identity.ActionServerBackup},
			want:    access.ErrInUse,
		},
		{
			// The service refuses a role with no action before the
			// placement check runs, because a role needs at least one
			// action. The row stays so that the refusal cannot go away.
			name:    "an update that removes every action",
			actions: nil,
			want:    access.ErrInvalid,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			ctx := t.Context()
			r := f.grantedRole(t)

			_, err := f.svc.UpdateRole(ctx, testEntry(), admin(), r.ID, access.RoleInput{
				Name: r.Name, Description: "", Actions: tc.actions,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("update of a role granted on a principal = %v, want %v", err, tc.want)
			}

			got, err := f.svc.GetRole(ctx, r.ID)
			if err != nil {
				t.Fatalf("get role: %v", err)
			}

			if !slices.Equal(got.Actions, []identity.Action{identity.ActionPrincipalManageTokens}) {
				t.Fatalf("role actions after the refusal = %v, want the token action alone", got.Actions)
			}
		})
	}
}

// TestDelegationFamilyIsPinned pins the actions of the delegation family.
// The family decides who may act as a service principal, so a second action
// needs a review of each rule that compares a reach.
func TestDelegationFamilyIsPinned(t *testing.T) {
	t.Parallel()

	want := []identity.Action{identity.ActionPrincipalManageTokens}
	if got := access.Actions(access.FamilyDelegation); !slices.Equal(got, want) {
		t.Fatalf("the delegation family = %v, want %v", got, want)
	}
}
