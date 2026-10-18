package catalogtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/quota"
)

// testExpireRunning: a revision past its deadline with a job that has not
// ended keeps its unit and its open reservation. The expire schedule moves
// the deadline and the end time of the reservation, and changes no state.
func testExpireRunning(t *testing.T, h Harness, f fixture) {
	t.Helper()

	rev := runningCreate(t, h, f)

	var j job.Job

	j.ID, j.State = "job-1", job.StateRunning
	now := rev.Deadline.Add(-time.Hour)

	exp, err := catalog.ExpiryOf(&rev, &j, now)
	if err != nil || exp.End != "" || !exp.Deadline.Equal(rev.Deadline.Add(quota.ReservationGrace)) {
		t.Fatalf("ExpiryOf(running job) = %+v, %v, want a new deadline and no end", exp, err)
	}

	req := get(t, h)
	d := catalog.DeadlineChange{Ref: req.Ref, Deadline: exp.Deadline, Number: 1, Version: req.Version}

	got, err := h.Requests.Extend(t.Context(), d, f.fence)
	if err != nil || got.State != catalog.StateRunning || !got.Deadline.Equal(exp.Deadline) {
		t.Fatalf("Extend() = %+v, %v, want running with the new deadline", got, err)
	}

	wantKept(t, h, &rev, now)
	wantKept(t, h, &rev, rev.Deadline.Add(time.Minute))
	wantExtendErrors(t, h, f, d)
}

// runningCreate stores a create with a unit and a reservation, whose job
// runs.
func runningCreate(t *testing.T, h Harness, f fixture) catalog.Revision {
	t.Helper()

	s := withQuota(submission(f, none(), catalog.OperationCreate, "small"), "expire", 5, 5)
	s.Request.Unit, s.Request.UnitName = identity.Object{}, "databases/expire"
	s.Provision = &catalog.Provision{
		Request: "", Tool: "opentofu", Unit: "databases/expire", Ref: ref(),
		Blueprint: catalog.BlueprintRef{Namespace: ns, Name: "postgres"}, Version: 1,
	}
	req, _ := submit(t, h, s, f)

	c := change(&req, catalog.StateRunning)
	c.Job, c.JobVersion = "job-1", 1

	return mustChange(t, h, f, c)
}

// wantKept checks that the unit of rev exists, that its reservation counts
// at now, and that no quota operation started.
func wantKept(t *testing.T, h Harness, rev *catalog.Revision, now time.Time) {
	t.Helper()

	if _, ok, err := h.Domain.Unit(t.Context(), ns, "opentofu", "databases/expire"); err != nil || !ok {
		t.Fatalf("Unit() after Extend = %v, %v, want the unit", ok, err)
	}

	usage, err := h.Domain.QuotaUsage(t.Context(), []quota.Check{{Key: unitsKey(), Window: 0, Max: 0}}, now)
	if err != nil || len(usage) != 1 || usage[0].Reserved != 1 {
		t.Fatalf("QuotaUsage(at %v) = %+v, %v, want 1 reserved", now, usage, err)
	}

	if _, ok, err := h.Domain.Operation(t.Context(), catalog.QuotaKey(rev.Key())); err != nil || ok {
		t.Fatalf("Operation(quota) after Extend = %v, %v, want none", ok, err)
	}
}

func wantExtendErrors(t *testing.T, h Harness, f fixture, d catalog.DeadlineChange) {
	t.Helper()

	if _, err := h.Requests.Extend(t.Context(), d, f.fence); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("Extend(stale) = %v, want ErrConflict", err)
	}

	req := get(t, h)
	d.Version = req.Version

	if _, err := h.Requests.Extend(t.Context(), d, f.fence); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("Extend(the same deadline) = %v, want ErrInvalid", err)
	}

	mustChange(t, h, f, change(&req, catalog.StateCanceled))

	req = get(t, h)
	d.Version, d.Deadline = req.Version, d.Deadline.Add(time.Hour)

	if _, err := h.Requests.Extend(t.Context(), d, f.fence); !errors.Is(err, catalog.ErrFinished) {
		t.Fatalf("Extend(canceled) = %v, want ErrFinished", err)
	}
}
