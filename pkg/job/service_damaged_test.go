package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// allowEverything lets the authorizer of the rig allow every action on every
// object, the zero object too, as an authorizer that trusts its caller does.
func allowEverything(r *rig) {
	for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobCancel} {
		r.authz.allow[string(action)+" "+(identity.Object{}).String()] = true
	}
}

// resolvesTo makes UnitByID answer o, whatever namespace and unit ID it is asked for.
func resolvesTo(o identity.Object) func(*job.ServiceConfig, *rig) {
	return func(cfg *job.ServiceConfig, r *rig) {
		cfg.Units = svcUnits{unit: r.unit, err: nil, other: nil, byID: &o}
	}
}

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
			row: job.DamagedRow{
				ID: damagedID, UnitID: r.unit.UnitID(), Reason: "the body is not JSON",
				State: job.StateRunning, Version: 7,
			},
			ns: r.unit.Namespace(),
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

	// The answer names the version that the store wrote, not version 0.
	if got.Version != 8 {
		t.Errorf("Cancel answers version %d, want 8: the version after the stored row", got.Version)
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

	checkEventOld(t, e.Changes, job.StateRunning)
}

// checkEventOld checks that the event change of a cancel entry has the stored
// state as its old value, and not an empty text.
func checkEventOld(t *testing.T, changes []audit.Change, want job.State) {
	t.Helper()

	for _, c := range changes {
		if c.Key == "event" && (c.Old == nil || *c.Old != string(want)) {
			t.Errorf("the event change has old value %v, want %q: the stored state", c.Old, want)
		}
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

// TestServiceCancelsADamagedJobTrustsNoLayer sends hostile answers straight to
// the service, as if the resolver, the store and the authorizer had failed to
// check them. Each case must stop before CancelDamaged, except the one that
// models a row repaired during the cancel.
func TestServiceCancelsADamagedJobTrustsNoLayer(t *testing.T) {
	t.Parallel()

	bare := func(ns, id string) identity.Object {
		u, err := identity.UnitObject("opentofu", namespace.Name(ns), "web")
		if err != nil {
			t.Fatalf("UnitObject = %v", err)
		}

		return u.WithUnitID(id)
	}

	tests := []struct {
		name        string
		opt         func(*job.ServiceConfig, *rig)
		store       func(*svcStore)
		check       func(*testing.T, error)
		wantCancels int
	}{
		{
			"the resolver answers a unit of another namespace", resolvesTo(bare("other", "unt-web")), nil,
			isErr(job.ErrNotFound), 0,
		},
		{
			"the resolver answers another unit ID", resolvesTo(bare("acme", "unt-other")), nil,
			isErr(job.ErrNotFound), 0,
		},
		{"the resolver answers a unit with no ID", resolvesTo(bare("acme", "")), nil, isErr(job.ErrNotFound), 0},
		{
			"the row and the resolver both hold no unit ID", resolvesTo(bare("acme", "")),
			func(s *svcStore) {
				d := s.damaged[damagedID]
				d.row.UnitID = ""
				s.damaged[damagedID] = d
			},
			isErr(job.ErrNotFound), 0,
		},
		{
			"the resolver fails",
			func(cfg *job.ServiceConfig, r *rig) {
				cfg.Units = svcUnits{unit: r.unit, err: errStoreDown, other: nil, byID: nil}
			},
			nil, isNotNotFound(errStoreDown), 0,
		},
		{
			"the store has no such damaged row", nil, func(s *svcStore) { s.getDamagedErr = job.ErrNotFound },
			isErr(job.ErrNotFound), 0,
		},
		{
			"the store fails on the damaged row", nil, func(s *svcStore) { s.getDamagedErr = errStoreDown },
			isNotNotFound(errStoreDown), 0,
		},
		{
			"the row was repaired before the read of the row", nil,
			func(s *svcStore) { s.getDamagedErr = job.ErrInvalid }, isErr(job.ErrConflict), 0,
		},
		{
			"the row was repaired before the cancel", nil,
			func(s *svcStore) { s.cancelDamagedErr = job.ErrInvalid }, isErr(job.ErrConflict), 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := hostileRig(t, tc.opt, tc.store)

			_, err := r.svc.Cancel(t.Context(), actorOf("carol"), damagedID, "the row is damaged")
			tc.check(t, err)

			if n := len(r.store.damagedCancels); n != tc.wantCancels {
				t.Errorf("CancelDamaged was called %d times, want %d", n, tc.wantCancels)
			}
		})
	}
}

// isNotNotFound checks that err wraps want and is not the answer for a missing job.
func isNotNotFound(want error) func(*testing.T, error) {
	return func(t *testing.T, err error) {
		t.Helper()

		if !errors.Is(err, want) || errors.Is(err, job.ErrNotFound) {
			t.Errorf("error = %v, want the failure %v and not ErrNotFound", err, want)
		}
	}
}

// hostileRig is a rig whose authorizer allows every object, the zero object
// and the units of other namespaces too: only the checks of the service can
// stop a call. It holds one damaged row on the unit of the rig.
func hostileRig(t *testing.T, opt func(*job.ServiceConfig, *rig), store func(*svcStore)) *rig {
	t.Helper()

	return newRig(t, func(cfg *job.ServiceConfig, rr *rig) {
		allowEverything(rr)

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionJobCancel} {
			units := []identity.Object{
				rr.unit,
				hostileUnit(t, "other", "unt-web"),
				hostileUnit(t, "acme", "unt-other"),
				hostileUnit(t, "acme", ""),
			}
			for i := range units {
				rr.authz.allow[string(action)+" "+units[i].String()] = true
			}
		}

		rr.store.damaged = map[job.ID]damagedRow{
			damagedID: {
				row: job.DamagedRow{
					ID:      damagedID,
					UnitID:  rr.unit.UnitID(),
					Reason:  "the body is not JSON",
					State:   "",
					Version: 0,
				},
				ns: rr.unit.Namespace(),
			},
		}

		if store != nil {
			store(rr.store)
		}

		if opt != nil {
			opt(cfg, rr)
		}
	})
}

func hostileUnit(t *testing.T, ns, id string) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", namespace.Name(ns), "web")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	return u.WithUnitID(id)
}
