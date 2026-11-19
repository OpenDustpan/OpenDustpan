package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const damagedID job.ID = "job-damaged"

var (
	errBoom      = errors.New("the authorizer failed")
	errFenceDown = errors.New("the fence source failed")
)

// damagedRig returns a rig with one damaged row on the unit of the rig, and an
// authorizer that lets every subject read and cancel on the unit.
func damagedRig(t *testing.T, mod func(*rig)) *rig {
	t.Helper()

	r := cancelRig(t)
	r.store.damaged = map[job.ID]damagedRow{
		damagedID: {
			row: job.DamagedRow{ID: damagedID, UnitID: r.unit.UnitID(), Reason: "the body is not JSON"},
			ns:  r.unit.Namespace(),
		},
	}

	if mod != nil {
		mod(r)
	}

	return r
}

func TestServiceCancelsADamagedJob(t *testing.T) {
	t.Parallel()

	r := damagedRig(t, nil)

	got, err := r.svc.Cancel(t.Context(), actorOf("carol"), damagedID, "the row is damaged")
	if err != nil {
		t.Fatalf("Cancel of a damaged job = %v", err)
	}

	if got.ID != damagedID || got.State != job.StateCanceled || got.Spec.Unit.String() != r.unit.String() {
		t.Errorf("Cancel = %s in state %s on %s, want %s canceled on the unit", got.ID, got.State, got.Spec.Unit,
			damagedID)
	}

	if len(r.store.damagedCancels) != 1 {
		t.Fatalf("CancelDamaged was called %d times, want 1", len(r.store.damagedCancels))
	}

	checkDamagedCancel(t, r)

	if len(r.store.applies) != 0 {
		t.Errorf("Apply was called %d times, want 0: a damaged row has no body to change", len(r.store.applies))
	}
}

func checkDamagedCancel(t *testing.T, r *rig) {
	t.Helper()

	c := r.store.damagedCancels[0]
	if c.id != damagedID || c.fence.Namespace != r.unit.Namespace() || len(c.entries) != 1 {
		t.Fatalf("CancelDamaged got %s, fence %v, %d entries; want the job, the fence of its namespace, one entry",
			c.id, c.fence, len(c.entries))
	}

	e := c.entries[0]
	if e.Action != string(job.ActionJobCancel) || e.Subject != "carol" || e.Reason != "the row is damaged" ||
		e.Outcome != audit.OutcomeSuccess || e.Object != r.unit.String() {
		t.Errorf("entry = %+v, want %s by carol on the unit with the reason", e, job.ActionJobCancel)
	}
}

// TestServiceCancelsADamagedJobRefuses is the hostile table of the service
// layer: each case must stop before the store cancels the row.
func TestServiceCancelsADamagedJobRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mod   func(*rig)
		actor job.Actor
		id    job.ID
		check func(*testing.T, error)
	}{
		{
			"no read action", func(r *rig) { delete(r.authz.allow, string(job.ActionJobRead)+" "+r.unit.String()) },
			actorOf("carol"), damagedID, isErr(job.ErrNotFound),
		},
		{
			"no cancel action", func(r *rig) { delete(r.authz.allow, string(job.ActionJobCancel)+" "+r.unit.String()) },
			actorOf("carol"), damagedID, isDenied,
		},
		{
			"a subject that holds nothing", nil, actorOf("bob"), damagedID, isErr(job.ErrNotFound),
		},
		{
			"the authorizer fails on cancel",
			func(r *rig) { r.authz.fail[string(job.ActionJobCancel)+" "+r.unit.String()] = errBoom },
			actorOf("carol"), damagedID, isDenied,
		},
		{
			"the authorizer fails on read",
			func(r *rig) { r.authz.fail[string(job.ActionJobRead)+" "+r.unit.String()] = errBoom },
			actorOf("carol"), damagedID, isAnyErr,
		},
		{
			"the row names a unit of another namespace",
			func(r *rig) {
				d := r.store.damaged[damagedID]
				d.ns = namespace.Name("other")
				r.store.damaged[damagedID] = d
			},
			actorOf("carol"), damagedID, isErr(job.ErrNotFound),
		},
		{
			"the row names an unknown unit",
			func(r *rig) {
				d := r.store.damaged[damagedID]
				d.row.UnitID = "unt-gone"
				r.store.damaged[damagedID] = d
			},
			actorOf("carol"), damagedID, isErr(job.ErrNotFound),
		},
		{
			"the row names no unit",
			func(r *rig) {
				d := r.store.damaged[damagedID]
				d.row.UnitID = ""
				r.store.damaged[damagedID] = d
			},
			actorOf("carol"), damagedID, isErr(job.ErrNotFound),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := damagedRig(t, tc.mod)

			_, err := r.svc.Cancel(t.Context(), tc.actor, tc.id, "the row is damaged")
			tc.check(t, err)

			if n := len(r.store.damagedCancels); n != 0 {
				t.Errorf("CancelDamaged was called %d times after a refusal, want 0", n)
			}
		})
	}
}

func TestServiceCancelsADamagedJobFailsOnTheFence(t *testing.T) {
	t.Parallel()

	r := damagedRig(t, nil)
	r2 := newRig(t, func(cfg *job.ServiceConfig, rr *rig) {
		for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobCancel} {
			rr.authz.allow[string(action)+" "+rr.unit.String()] = true
		}

		rr.store.damaged = r.store.damaged
		cfg.Fences = svcFences{err: errFenceDown}
	})

	_, err := r2.svc.Cancel(t.Context(), actorOf("carol"), damagedID, "the row is damaged")
	if !errors.Is(err, errFenceDown) {
		t.Fatalf("Cancel = %v, want the error of the fence source", err)
	}

	if len(r2.store.damagedCancels) != 0 {
		t.Error("CancelDamaged was called after a failed fence")
	}
}

func isErr(want error) func(*testing.T, error) {
	return func(t *testing.T, err error) {
		t.Helper()

		if !errors.Is(err, want) {
			t.Errorf("error = %v, want %v", err, want)
		}
	}
}

func isAnyErr(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Error("error = nil, want a refusal")
	}
}

func isDenied(t *testing.T, err error) {
	t.Helper()

	var d *job.DeniedError
	if !errors.As(err, &d) || !errors.Is(err, identity.ErrDenied) {
		t.Errorf("error = %v, want a *DeniedError", err)
	}
}
