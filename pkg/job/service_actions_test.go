package job_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// cancelRig returns a rig whose authorizer lets every subject read and cancel
// on the unit. Tests that need a denial remove the entry or make the
// authorizer fail.
func cancelRig(t *testing.T) *rig {
	t.Helper()

	return newRig(t, func(_ *job.ServiceConfig, r *rig) {
		for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobCancel} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	})
}

func TestServiceActionsCancelRecordsTheEvent(t *testing.T) {
	t.Parallel()

	for _, state := range []job.State{
		job.StateQueued, job.StateAwaitingApproval, job.StateDispatched, job.StateRunning, job.StateAwaitingState,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			r := cancelRig(t)
			seeded := r.seed(func(j *job.Job) { j.State = state })

			_, err := r.svc.Cancel(t.Context(), actorOf("carol"), approveID, "wrong unit")
			if err != nil {
				t.Fatalf("Cancel = %v", err)
			}

			checkCancelChange(t, r, seeded.Version)
			checkCancelEntry(t, r)
		})
	}
}

func checkCancelChange(t *testing.T, r *rig, version int64) {
	t.Helper()

	if len(r.store.applies) != 1 || len(r.store.applyEntries) != 1 || len(r.store.applyEntries[0]) != 1 {
		t.Fatalf("applies = %d, entries = %v, want one change with one entry", len(r.store.applies),
			r.store.applyEntries)
	}

	c := r.store.applies[0]
	wrong := c.Event != job.EventCancel || c.Job != approveID || c.Version != version
	if wrong || c.Reason != "wrong unit" {
		t.Errorf("change = %s of %s at %d with reason %q, want %s of %s at %d with the reason", c.Event, c.Job,
			c.Version, c.Reason, job.EventCancel, approveID, version)
	}
}

func checkCancelEntry(t *testing.T, r *rig) {
	t.Helper()

	e := r.store.applyEntries[0][0]
	if e.Action != string(job.ActionJobCancel) || e.Subject != "carol" || e.Reason != "wrong unit" ||
		e.Outcome != audit.OutcomeSuccess || e.Object != r.unit.String() {
		t.Errorf("entry = %+v, want %s by carol on the unit with the reason", e, job.ActionJobCancel)
	}
}

func TestServiceActionsCancelOfCancelingChangesNothing(t *testing.T) {
	t.Parallel()

	r := cancelRig(t)
	r.seed(func(j *job.Job) { j.State = job.StateCanceling })

	got, err := r.svc.Cancel(t.Context(), actorOf("carol"), approveID, "")
	if err != nil {
		t.Fatalf("Cancel = %v", err)
	}

	if got.State != job.StateCanceling || len(r.store.applies) != 0 {
		t.Errorf("state = %s with %d changes, want canceling and no change", got.State, len(r.store.applies))
	}
}

// TestServiceActionsCancelRefuses holds the hostile cases: each guard of
// Cancel refuses and writes nothing.
func TestServiceActionsCancelRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mod    func(*job.Job)
		prep   func(*rig)
		id     job.ID
		actor  job.Actor
		reason string
		want   error
	}{
		{"unknown job", nil, nil, "job-missing", actorOf("carol"), "", job.ErrNotFound},
		{"id with no job prefix", nil, nil, "x-1", actorOf("carol"), "", job.ErrNotFound},
		{
			"no read action", nil,
			func(r *rig) { delete(r.authz.allow, string(job.ActionJobRead)+" "+r.unit.String()) },
			approveID, actorOf("carol"), "", job.ErrNotFound,
		},
		{
			"read but no cancel action", nil,
			func(r *rig) { delete(r.authz.allow, string(job.ActionJobCancel)+" "+r.unit.String()) },
			approveID, actorOf("carol"), "", identity.ErrDenied,
		},
		{
			"the authorizer fails on cancel", nil,
			func(r *rig) { r.authz.fail[string(job.ActionJobCancel)+" "+r.unit.String()] = errNoRights },
			approveID, actorOf("carol"), "", identity.ErrDenied,
		},
		{
			"the authorizer fails on read", nil,
			func(r *rig) { r.authz.fail[string(job.ActionJobRead)+" "+r.unit.String()] = errNoRights },
			approveID, actorOf("carol"), "", errNoRights,
		},
		{
			"job that ended", func(j *job.Job) { j.State = job.StateSucceeded }, nil, approveID, actorOf("carol"), "",
			job.ErrFinished,
		},
		{
			"job already canceled", func(j *job.Job) { j.State = job.StateCanceled }, nil, approveID,
			actorOf("carol"), "", job.ErrFinished,
		},
		{
			"change already in git", func(j *job.Job) {
				j.State = job.StateAwaitingState
				j.Delivery = delivery(job.DeliveryDirectPush, time.Time{})
			}, nil, approveID, actorOf("carol"), "", job.ErrDelivered,
		},
		{"hidden character in the reason", nil, nil, approveID, actorOf("carol"), bidiOverride(), job.ErrInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := cancelRig(t)
			r.seed(tc.mod)

			if tc.prep != nil {
				tc.prep(r)
			}

			_, err := r.svc.Cancel(t.Context(), tc.actor, tc.id, tc.reason)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Cancel = %v, want %v", err, tc.want)
			}

			if len(r.store.applies) != 0 {
				t.Errorf("a refused cancel wrote %d changes", len(r.store.applies))
			}
		})
	}
}

func TestServiceActionsCancelFailsClosedOnAStoreFault(t *testing.T) {
	t.Parallel()

	r := cancelRig(t)
	r.seed(nil)
	r.store.applyErr = errDriver

	_, err := r.svc.Cancel(t.Context(), actorOf("carol"), approveID, "")
	if !errors.Is(err, errDriver) {
		t.Fatalf("Cancel = %v, want the store fault", err)
	}
}

func TestServiceActionsCancelNeedsTheFence(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(cfg *job.ServiceConfig, r *rig) {
		cfg.Fences = svcFences{err: errDriver}

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobCancel} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	})
	r.seed(nil)

	_, err := r.svc.Cancel(t.Context(), actorOf("carol"), approveID, "")
	if !errors.Is(err, errDriver) || len(r.store.applies) != 0 {
		t.Fatalf("Cancel = %v with %d changes, want the fence error and no change", err, len(r.store.applies))
	}
}
