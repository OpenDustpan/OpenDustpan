package access_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// jobDataActionList returns every job action that the identity package
// defines. Each holds on the unit, so each belongs to the data family.
func jobDataActionList() []identity.Action {
	return []identity.Action{
		identity.ActionJobRead,
		identity.ActionJobReadLogs,
		identity.ActionJobReadPlans,
		identity.ActionJobCancel,
		identity.ActionJobApprove,
	}
}

// TestJobActionsHaveDataFamily pins every job action to the data family, so
// a grant of a role that holds one works on a namespace, a collection and a
// unit.
func TestJobActionsHaveDataFamily(t *testing.T) {
	t.Parallel()

	for _, a := range jobDataActionList() {
		t.Run(string(a), func(t *testing.T) {
			t.Parallel()

			family, ok := access.FamilyOf(a)
			if !ok {
				t.Fatalf("action %q has no family, want %q", a, access.FamilyData)
			}

			if family != access.FamilyData {
				t.Fatalf("action %q has family %q, want %q", a, family, access.FamilyData)
			}
		})
	}
}

// TestRoleOutsideDataFamilyRefusedOnDataObjects is a hostile test: a role
// that holds an action outside the data family is refused on a namespace, a
// collection and a unit, with ErrInvalid. The check fails closed: an action
// the table does not classify has no family and is refused too.
func TestRoleOutsideDataFamilyRefusedOnDataObjects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	f.person(t, "bob")
	subject := prn("bob")

	// An operator action stands in for any action outside the data family:
	// the grant-scope check must refuse it on every data object.
	// Defense in depth: the store checks the scope again in the
	// transaction of the grant; this service check stays so a new caller
	// cannot skip it.
	role := f.customRole(t, identity.ActionServerRestore)
	// unit:run-apply stands in for an action of an unknown family: the
	// identity package defines it, but the family table does not classify
	// it, so making a role that holds it fails closed with ErrInvalid.
	// Defense in depth: role creation refuses it here even though
	// FamilyOf also reports it as unclassified; this check stays so
	// a new caller that skips the lookup cannot store it.
	unknownActions := []identity.Action{identity.ActionUnitRunApply}
	unknownInput := access.RoleInput{Name: "custom-unknown", Description: "", Actions: unknownActions}
	_, err := f.svc.CreateRole(ctx, testEntry(), admin(), unknownInput)
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("create role with an unknown-family action answers %v, want %v", err, access.ErrInvalid)
	}

	colObj := identity.CollectionObject("acme", "team-a")
	f.collection(colObj)

	for _, obj := range []identity.Object{
		identity.NamespaceObject("acme"),
		colObj,
		f.unit(t, unitObject(t, "acme", "web/prod")),
	} {
		kind, _ := access.KindOf(obj)

		_, err := f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
			Object: obj, Role: role.ID, Subject: subject,
		})
		if err == nil {
			t.Fatalf("grant of a role with an operator action on %s %q was allowed, want refusal",
				kind, obj.String())
		}

		if !errors.Is(err, access.ErrInvalid) {
			t.Fatalf("grant of a role with an operator action on %s %q answers %v, want %v",
				kind, obj.String(), err, access.ErrInvalid)
		}
	}
}
