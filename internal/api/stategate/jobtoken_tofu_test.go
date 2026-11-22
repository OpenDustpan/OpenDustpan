package stategate_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestOpenTofuJobTokenStateWrites sends the OpenTofu writes of a plan-only job
// token through the state.write gate. The HTTP backend state write and the TFE
// state-version create, both without a lock, are refused. A token of another
// unit and a route unit other than the job's unit are refused, also for the
// plan lock. The plan lock of its own unit and a job past change.apply stay
// allowed, so that a refusal is not a broken fixture.
//
// The HTTP backend writes the operation of an unlocked write (postState, with no
// named lock held), and the TFE state-version create has no gate call on main.
// The gate gets the same question for both, because the gate sees no route.
func TestOpenTofuJobTokenStateWrites(t *testing.T) {
	t.Parallel()

	web := jobUnit(t, "opentofu", "web")
	api := jobUnit(t, "opentofu", "api")

	plan := gate.JobClaim{Job: jobID, Phase: "plan", PassedApply: false, Units: []identity.Object{web}}
	planOfAPI := gate.JobClaim{Job: jobID, Phase: "plan", PassedApply: false, Units: []identity.Object{api}}
	applied := gate.JobClaim{Job: jobID, Phase: "apply", PassedApply: true, Units: []identity.Object{web}}

	runJobCases(t, []jobCase{
		{
			name: "HTTP backend state write without a lock", job: plan, unit: web,
			op: gate.OperationUnlockedWrite, refused: true,
		},
		{
			name: "TFE state-version create without a lock", job: plan, unit: web,
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
			name: "control: state write after change.apply", job: applied, unit: web,
			op: gate.OperationUnlockedWrite, refused: false,
		},
	})
}
