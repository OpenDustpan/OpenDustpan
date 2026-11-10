package jobtest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
)

// testForeignNamespace gives a caller the fence of another namespace and
// proves that it reaches no job of the first one: each write refuses with a
// not-found or invalid error, never with a change, and the job stays as it
// was. A job record that names another namespace than the fence, and an audit
// entry of another namespace, are refused too.
func testForeignNamespace(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	other, err := h.Domain.CreateNamespace(t.Context(), "beta")
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	otherUnit, err := h.Domain.CreateUnit(t.Context(), other, "opentofu", "network")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	_, err = h.Jobs.Apply(t.Context(), change(&stored, job.EventCancel), other, nil)
	wantError(t, "Apply with the fence of another namespace", err, job.ErrNotFound)

	err = h.Jobs.SetVerification(t.Context(), stored.ID, job.VerificationVerified, other, nil)
	wantError(t, "SetVerification with the fence of another namespace", err, job.ErrNotFound)

	_, _, err = h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, ""), nil, other, nil)
	wantError(t, "Create of a job of another namespace", err, job.ErrInvalid)

	foreign := []audit.Entry{entry(otherUnit, job.ActionJobRead)}
	_, err = h.Jobs.Apply(t.Context(), change(&stored, job.EventCancel), f.fence, foreign)
	wantError(t, "Apply with an audit entry of another namespace", err, audit.ErrInvalidEntry)

	assertState(t, h, stored.ID, job.StateQueued)
}

// wantError fails t unless err wraps want.
func wantError(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", what, err, want)
	}
}

// testParallelCreatesOneKey races several creates with one idempotency key
// and one spec. Exactly one stores a job, and every create answers that job.
func testParallelCreatesOneKey(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	template := newJob(f.unit, job.OperationPlan, "start-race")
	results := make([]job.Job, racers)
	created := make([]bool, racers)

	errs := race(func(i int) error {
		j := *template
		j.ID = job.ID(fmt.Sprintf("%s-%d", template.ID, i))

		var err error

		results[i], created[i], err = h.Jobs.Create(t.Context(), j, nil, f.fence,
			[]audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})

		return err
	})

	if wins, other := count(errs, job.ErrKeyConflict); wins != racers || other != nil {
		t.Fatalf("creates by %d racers = %d without error, other error %v; want all to answer the one job", racers,
			wins, other)
	}

	news := 0

	for i := range racers {
		if created[i] {
			news++
		}

		if results[i].ID != results[0].ID {
			t.Fatalf("create %d answered %s, create 0 answered %s; want one job", i, results[i].ID, results[0].ID)
		}
	}

	if news != 1 {
		t.Fatalf("%d creates stored a job, want 1", news)
	}

	all, err := h.Jobs.List(t.Context(), job.Filter{
		Namespace: f.fence.Namespace, UnitID: "", States: nil, Limit: 0, After: "",
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	})
	if err != nil || len(all.Jobs) != 1 {
		t.Fatalf("List = %d jobs, %v; want 1", len(all.Jobs), err)
	}
}
