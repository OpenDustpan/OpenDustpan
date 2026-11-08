package access_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// readOnlyServer are the read-only server actions: each server:read-*
// action. A read-only action cannot raise a privilege, so it is not
// sensitive.
func readOnlyServer() []identity.Action {
	return []identity.Action{
		identity.ActionServerReadPrincipals,
		identity.ActionServerReadAccess,
		identity.ActionServerReadTokens,
		identity.ActionServerReadAudit,
		identity.ActionServerReadSettings,
		identity.ActionServerReadInstances,
		identity.ActionServerReadGitHost,
	}
}

// TestSensitiveActionsHoldsTheThreeFamilies proves that each action of the
// operator family, the administration family and the delegation family is
// sensitive, except the read-only server actions, and that no action of the
// data family and the self family is.
func TestSensitiveActionsHoldsTheThreeFamilies(t *testing.T) {
	t.Parallel()

	sensitive := access.SensitiveActions()
	readOnly := readOnlyServer()

	cases := []struct {
		family access.Family
		want   bool
	}{
		{family: access.FamilyOperator, want: true},
		{family: access.FamilyAdministration, want: true},
		{family: access.FamilyDelegation, want: true},
		{family: access.FamilyData, want: false},
		{family: access.FamilySelf, want: false},
	}

	for _, tc := range cases {
		for _, action := range access.Actions(tc.family) {
			if slices.Contains(readOnly, action) {
				continue
			}

			if got := slices.Contains(sensitive, action); got != tc.want {
				t.Errorf("action %q of family %q is in SensitiveActions() = %t, want %t",
					action, tc.family, got, tc.want)
			}

			if got := access.IsSensitive(action); got != tc.want {
				t.Errorf("IsSensitive(%q) = %t, want %t", action, got, tc.want)
			}
		}
	}
}

// TestSensitiveActionsExcludesReadOnlyServer proves that the read-only
// server actions are not sensitive, and that the actions that write stay
// sensitive.
func TestSensitiveActionsExcludesReadOnlyServer(t *testing.T) {
	t.Parallel()

	sensitive := access.SensitiveActions()

	for _, action := range readOnlyServer() {
		if access.IsSensitive(action) {
			t.Errorf("IsSensitive(%q) = true, want false: a read-only action cannot raise a privilege", action)
		}

		if slices.Contains(sensitive, action) {
			t.Errorf("SensitiveActions() holds %q, want no read-only server action", action)
		}
	}

	for _, action := range []identity.Action{
		identity.ActionServerDisableAuditChain,
		identity.ActionServerManageTokenPolicy,
		identity.ActionServerCreateNamespace,
	} {
		if !access.IsSensitive(action) {
			t.Errorf("IsSensitive(%q) = false, want true", action)
		}

		if !slices.Contains(sensitive, action) {
			t.Errorf("SensitiveActions() misses %q", action)
		}
	}
}

// TestSensitiveActionsIsSortedAndUnknownIsNotSensitive proves that the list
// holds each sensitive action once, in order, and that IsSensitive answers
// false for an action that no family classifies. The protected check of a
// group reads the family itself, so that an unknown action fails closed.
func TestSensitiveActionsIsSortedAndUnknownIsNotSensitive(t *testing.T) {
	t.Parallel()

	sensitive := access.SensitiveActions()

	if len(sensitive) == 0 {
		t.Fatal("SensitiveActions() is empty, want the actions of the three sensitive families")
	}

	if !slices.IsSorted(sensitive) {
		t.Errorf("SensitiveActions() = %v, want the actions in order", sensitive)
	}

	if len(slices.Compact(slices.Clone(sensitive))) != len(sensitive) {
		t.Errorf("SensitiveActions() = %v, want each action once", sensitive)
	}

	unknown := identity.Action("future:read-installation")

	if _, ok := access.FamilyOf(unknown); ok {
		t.Fatalf("FamilyOf(%q) is classified, want an action that no family classifies", unknown)
	}

	if access.IsSensitive(unknown) {
		t.Errorf("IsSensitive(%q) = true, want false for an action that no family classifies", unknown)
	}
}
