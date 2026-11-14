package access_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestPlannerReadsMaskedState pins what a planner can read. The Pulumi
// export and update routes need unit:read, which planner holds, and the
// checkpoint keeps ciphertext, so secrets stay unreadable. The OpenTofu
// state download answers plain secret values and needs unit:read and
// unit:read-secrets (internal/api/tfe/routes.go:285), which planner lacks,
// so `tofu state pull` as planner answers 403. A planner reads OpenTofu
// state metadata with unit:read and outputs with unit:read-outputs, with
// secret values masked. The 403 itself is proved at the handler by
// TestRevealDeniesWithoutReadSecrets
// (internal/api/pulumi/reveal_test.go:512).
func TestPlannerReadsMaskedState(t *testing.T) {
	t.Parallel()

	role, ok := access.BuiltInRole(access.RolePlanner)
	if !ok {
		t.Fatalf("no built-in role %q", access.RolePlanner)
	}

	for _, action := range []identity.Action{
		identity.ActionUnitRead,
		identity.ActionUnitReadOutputs,
	} {
		if !role.Has(action) {
			t.Errorf("planner holds %s = false, want true", action)
		}
	}

	if role.Has(identity.ActionUnitReadSecrets) {
		t.Error("planner holds unit:read-secrets = true, want false: reads stay masked")
	}
}

// TestPlannerWriteRefused pins that the planner role holds no write action:
// an update, apply, destroy or approval answers 403 and changes nothing.
// The 403 itself is proved at the handler by the route action tables, for
// example the download route in internal/api/tfe/states_test.go:230.
func TestPlannerWriteRefused(t *testing.T) {
	t.Parallel()

	role, ok := access.BuiltInRole(access.RolePlanner)
	if !ok {
		t.Fatalf("no built-in role %q", access.RolePlanner)
	}

	for _, action := range []identity.Action{
		identity.ActionUnitWrite,
		identity.ActionUnitRunApply,
		identity.ActionUnitRunDestroy,
		identity.ActionUnitWriteBack,
		identity.ActionUnitDefineSteps,
		identity.ActionUnitEncrypt,
		identity.ActionUnitDecrypt,
		identity.ActionUnitSetContext,
		identity.ActionUnitManage,
		identity.ActionUnitLock,
		identity.ActionUnitReadSecrets,
		identity.ActionJobApprove,
		identity.ActionJobCancel,
	} {
		if role.Has(action) {
			t.Errorf("planner holds %s = true, want false", action)
		}
	}
}

// TestPlannerAccessDecision pins the access decision for a planner at the
// role table: viewer reads and plan runs are allowed, every write and a
// token for another principal are refused because planner holds no write
// or token action. There is no CheckAccess function in pkg/access; the
// decision lives in the Authorizer behind the action declarations of each
// route, and the refusal path is proved at the handler by
// TestRevealDeniesWithoutReadSecrets
// (internal/api/pulumi/reveal_test.go:512).
func TestPlannerAccessDecision(t *testing.T) {
	t.Parallel()

	role, ok := access.BuiltInRole(access.RolePlanner)
	if !ok {
		t.Fatalf("no built-in role %q", access.RolePlanner)
	}

	allowed := []identity.Action{
		identity.ActionNamespaceRead,
		identity.ActionCollectionRead,
		identity.ActionUnitRead,
		identity.ActionUnitReadOutputs,
		identity.ActionUnitRunPlan,
		identity.ActionJobReadPlans,
		identity.ActionJobReadLogs,
	}

	for _, action := range allowed {
		if !role.Has(action) {
			t.Errorf("planner holds %s = false, want true", action)
		}
	}

	refused := []identity.Action{
		identity.ActionUnitWrite,
		identity.ActionUnitRunApply,
		identity.ActionUnitRunDestroy,
		identity.ActionUnitReadSecrets,
		identity.ActionJobApprove,
		identity.ActionPrincipalManageTokens,
		identity.ActionServerManageTokens,
	}

	for _, action := range refused {
		if role.Has(action) {
			t.Errorf("planner holds %s = true, want false", action)
		}
	}
}

// TestPlannerUnknownActionDenied pins that the role check fails closed: an
// empty or unknown action is never allowed, and an unknown role resolves to
// nothing. The handler-level guards of task 1.4 (body size limit, strict
// decoding, containment on the refused routes with a planner token) are not
// covered here; that task stays open.
func TestPlannerUnknownActionDenied(t *testing.T) {
	t.Parallel()

	role, ok := access.BuiltInRole(access.RolePlanner)
	if !ok {
		t.Fatalf("no built-in role %q", access.RolePlanner)
	}

	for _, action := range []identity.Action{"", "unit:write ", "unit:read-secrets\n", "admin"} {
		if role.Has(action) {
			t.Errorf("planner holds %q = true, want false", action)
		}
	}

	if _, ok := access.BuiltInRole("planner "); ok {
		t.Error(`BuiltInRole("planner ") = true, want false`)
	}
}
