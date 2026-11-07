package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// allowRunners lets every principal that is not denied start apply and destroy
// jobs on the unit of the rig. The rig allows a plan only.
func allowRunners(_ *job.ServiceConfig, r *rig) {
	for _, a := range []identity.Action{job.ActionUnitRunApply, job.ActionUnitRunDestroy} {
		r.authz.allow[string(a)+" "+r.unit.String()] = true
	}
}

func TestStartApplyAndDestroyHaveTwoPhases(t *testing.T) {
	t.Parallel()

	for _, op := range []job.Operation{job.OperationApply, job.OperationDestroy} {
		t.Run(string(op), func(t *testing.T) {
			t.Parallel()

			r := newRig(t, allowRunners)
			req := r.plan("key-" + string(op))
			req.Operation = op

			j, created, err := r.svc.StartOnce(t.Context(), actorOf("ada"), req)
			if err != nil || !created {
				t.Fatalf("StartOnce(%s) = created %v, error %v; want a new job", op, created, err)
			}

			if len(j.Phases) != 2 || j.Phases[0].Kind != job.PhasePlan || j.Phases[1].Kind != job.PhaseApply ||
				j.Current != 0 || j.Spec.Operation != op {
				t.Fatalf("job = phases %+v, current %d, operation %s; want the phases plan and apply, at the plan",
					j.Phases, j.Current, j.Spec.Operation)
			}
		})
	}
}

// TestStartApplyByWriteBackHolderIsRefusedAfterThePlanner proves that a
// principal who holds unit:write-back but not unit:run-apply passes the first
// check of a start with no executor (the write-back action), and that the
// check of the spec, which the planner sets to a runner, still refuses the
// start with the action it lacks. The second check does not rely on the first.
func TestStartApplyByWriteBackHolderIsRefusedAfterThePlanner(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(_ *job.ServiceConfig, r *rig) {
		r.authz.allow[string(job.ActionUnitWriteBack)+" "+r.unit.String()] = true
	})

	req := r.plan("key-write-back")
	req.Operation = job.OperationApply

	_, err := r.svc.Start(t.Context(), actorOf("pam"), req)

	var denied *job.DeniedError
	if !errors.Is(err, identity.ErrDenied) || !errors.As(err, &denied) || denied.Action != job.ActionUnitRunApply {
		t.Fatalf("Start = %v, want a refusal for the action %s", err, job.ActionUnitRunApply)
	}

	if r.planner.calls != 1 || len(r.store.creates) != 0 {
		t.Fatalf("planner calls = %d, stored jobs = %d; want 1 and 0", r.planner.calls, len(r.store.creates))
	}
}

// TestStartApplyAndDestroyNeedTheirActions proves that a principal who can
// plan only gets a refusal for an apply and for a destroy, with the action
// that it lacks, before the planner reads the unit or a job is stored.
func TestStartApplyAndDestroyNeedTheirActions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op     job.Operation
		action identity.Action
	}{
		{job.OperationApply, job.ActionUnitRunApply},
		{job.OperationDestroy, job.ActionUnitRunDestroy},
	}

	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			req := r.plan("key-" + string(tt.op))
			req.Operation = tt.op
			// Without an executor the service reports the last action it tried,
			// which for an apply is the write-back action.
			req.Executor = job.ExecutorRunner

			_, err := r.svc.Start(t.Context(), actorOf("pam"), req)

			var denied *job.DeniedError
			if !errors.Is(err, identity.ErrDenied) || !errors.As(err, &denied) || denied.Action != tt.action {
				t.Fatalf("Start(%s) = %v, want a refusal for the action %s", tt.op, err, tt.action)
			}

			if len(r.store.creates) != 0 || r.planner.calls != 0 {
				t.Fatalf("Start(%s) stored %d jobs and called the planner %d times; want none", tt.op,
					len(r.store.creates), r.planner.calls)
			}
		})
	}
}
