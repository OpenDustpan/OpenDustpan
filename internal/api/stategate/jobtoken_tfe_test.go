package stategate_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestOpenTofuJobTokenStateWrites sends the operations of the OpenTofu routes
// that call the state.write guard through the gate and the admission, with the
// claim of a plan-only job token. It calls the guard directly: the route-level
// cases are in the server tests. The TFE force unlock, rollback and delete and
// the unlocked write of the HTTP backend are refused. A token of another unit
// and a route unit other than the job's unit are refused, also for the plan
// lock. The plan lock of its own unit and a job past change.apply stay
// allowed, so that a refusal is not a broken fixture.
func TestOpenTofuJobTokenStateWrites(t *testing.T) {
	t.Parallel()

	web := jobUnit(t, "opentofu", "web")
	api := jobUnit(t, "opentofu", "api")

	plan := gate.JobClaim{Job: jobID, Phase: "plan", PassedApply: false, Units: []identity.Object{web}}
	planOfAPI := gate.JobClaim{Job: jobID, Phase: "plan", PassedApply: false, Units: []identity.Object{api}}
	applied := gate.JobClaim{Job: jobID, Phase: "apply", PassedApply: true, Units: []identity.Object{web}}

	runJobCases(t, []jobCase{
		{name: "force unlock in the plan phase", job: plan, unit: web, op: gate.OperationUnlock, refused: true},
		{name: "rollback in the plan phase", job: plan, unit: web, op: gate.OperationRollback, refused: true},
		{name: "delete in the plan phase", job: plan, unit: web, op: gate.OperationDelete, refused: true},
		{
			name: "unlocked write operation in the plan phase", job: plan, unit: web,
			op: gate.OperationUnlockedWrite, refused: true,
		},
		{
			name: "token of another unit on this unit", job: planOfAPI, unit: web,
			op: gate.OperationUnlockedWrite, refused: true,
		},
		{
			name: "route unit other than the job unit, plan lock", job: plan, unit: api,
			op: gate.OperationLock, refused: true,
		},
		{
			name: "control: plan lock of the job unit", job: plan, unit: web,
			op: gate.OperationLock, refused: false,
		},
		{
			name: "control: unlocked write operation after change.apply", job: applied, unit: web,
			op: gate.OperationUnlockedWrite, refused: false,
		},
	})
}
