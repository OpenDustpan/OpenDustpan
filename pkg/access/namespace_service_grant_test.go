package access_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestGrantRefusesTheNamespaceServiceRecord proves that the Grant API gives
// nothing to a planted record with the subject of the namespace service
// principal: no role, not even one with unit:run-plan, on any object, and also
// for an administrator.
func TestGrantRefusesTheNamespaceServiceRecord(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	subject := identity.NamespaceServiceSubject("acme")
	f.person(t, subject)

	own := f.unit(t, unitObject(t, "acme", "proj/dev"))

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "run-plan-only", Description: "", Actions: []identity.Action{identity.ActionUnitRunPlan},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	for _, roleID := range []string{access.RoleAdmin, access.RoleViewer, role.ID} {
		for _, obj := range []identity.Object{own, identity.NamespaceObject("acme"), identity.ServerObject()} {
			_, grantErr := f.svc.Grant(t.Context(), testEntry(), admin(), access.GrantInput{
				Object: obj, Role: roleID, Subject: prn(subject),
			})
			if !errors.Is(grantErr, access.ErrInvalid) || !errors.Is(grantErr, principal.ErrReservedSubject) {
				t.Errorf(
					"Grant of %q on %q = %v, want ErrInvalid and ErrReservedSubject",
					roleID,
					obj.String(),
					grantErr,
				)
			}
		}
	}
}

// TestUnitRunPlanRoleForAStoredPerson proves that a stored person can hold
// unit:run-plan through a role on a unit, and nothing else follows from it.
func TestUnitRunPlanRoleForAStoredPerson(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	person := f.person(t, "ada")
	own := unitObject(t, "acme", "proj/dev")

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "run-plan-only", Description: "", Actions: []identity.Action{identity.ActionUnitRunPlan},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	_, err = f.svc.Grant(t.Context(), testEntry(), admin(), access.GrantInput{
		Object: f.unit(t, own), Role: role.ID, Subject: prn("ada"),
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	assertChecks(t, f.authz, person, []check{
		{own, identity.ActionUnitRunPlan, true},
		{own, identity.ActionUnitRead, false},
		{unitObject(t, "acme", "proj/other"), identity.ActionUnitRunPlan, false},
	})
}
