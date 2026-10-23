package jobtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// claimBoundCases returns the cases of the claim as a durable token: the
// event of a claim that is bound to its claim, and the end of a phase whose
// claim expired during a cancel.
func claimBoundCases() []suiteCase {
	return []suiteCase{
		{"claim: the event of a claim is bound to the live claim of its operation", testClaimEventBound},
		{"claim: a claim whose lease expired before its event leaves no trace in the job", testClaimTwoTakeovers},
		{"claim: a claim that expires during a cancel ends the job and frees the unit", testExpiryDuringCancel},
		{"claim: the audit entry of a claim has the time of the injected clock", testClaimClock},
	}
}

// runnerThree is the owner of the third claim of a phase.
const runnerThree = "runner-3"

func testClaimEventBound(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationRefresh, steps: changeSteps()})
	a := claim(t, h, runnerOne, longRenew, runnerKind(&j))

	lapse(t, h, &a)

	b, err := claimAgain(t, h, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	before := jobOf(t, h, j.ID)

	// The claim of A is not the live claim of the operation any more, so an
	// event that carries it changes nothing.
	stale := change(&before, job.EventClaim)
	stale.Executor = runnerOne
	stale.Claim = &a.Claim.Claim

	_, err = h.Jobs.Apply(t.Context(), stale, f.fence, []audit.Entry{entry(f.unit, job.ActionRunnerClaim)})
	if !errors.Is(err, job.ErrClaimLost) || !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("EventClaim with the claim of a replaced holder = %v, want ErrClaimLost", err)
	}

	// A claim of another operation does not bind the job either.
	other := dispatched(t, h, f, plannedJob{unit: unitName(1), op: job.OperationRefresh, steps: changeSteps()})
	foreign := claim(t, h, runnerOne, time.Minute, runnerKind(&other))

	entries := len(allEntries(t, h, f))
	wrong := change(&before, job.EventClaim)
	wrong.Executor = runnerOne
	wrong.Claim = &foreign.Claim.Claim

	_, err = h.Jobs.Apply(t.Context(), wrong, f.fence, []audit.Entry{entry(f.unit, job.ActionRunnerClaim)})
	if !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("EventClaim with the claim of another operation = %v, want ErrClaimLost", err)
	}

	// A claim on another event than a claim is not valid.
	bad := change(&before, job.EventRelease)
	bad.Claim = &b.Claim.Claim

	if _, err = h.Jobs.Apply(t.Context(), bad, f.fence, nil); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("EventRelease with a claim = %v, want ErrInvalid", err)
	}

	got := jobOf(t, h, j.ID)
	if got.Version != before.Version || got.Phases[0].Executor != runnerTwo || got.Phases[0].Claims != 2 {
		t.Fatalf(
			"job after the refused events = version %d, executor %q, %d claims; want version %d, runner-2, 2 claims",
			got.Version,
			got.Phases[0].Executor,
			got.Phases[0].Claims,
			before.Version,
		)
	}

	if n := len(allEntries(t, h, f)); n != entries {
		t.Fatalf("entries after the refused events = %d, want %d", n, entries)
	}

	// The live claim binds the event.
	live := change(&before, job.EventClaim)
	live.Executor = runnerTwo
	live.Claim = &b.Claim.Claim

	if _, err = h.Jobs.Apply(t.Context(), live, f.fence, nil); err != nil {
		t.Fatalf("EventClaim with the live claim = %v", err)
	}
}

// testClaimTwoTakeovers runs the sequence of two takeovers in a row. The
// runner B claims the operation with a short lease and reads the job. Its
// lease expires before its event, and the runner C takes the operation over
// and applies its event. The event of B then fails with ErrClaimLost, B
// gives no assignment, and the job, its claim count and its audit entries
// name only the runners that held the phase.
func testClaimTwoTakeovers(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationRefresh, steps: changeSteps()})
	kind := runnerKind(&j)
	a := claim(t, h, runnerOne, longRenew, kind)

	lapse(t, h, &a)

	var (
		c      job.Assignment
		bClaim durable.Claim
	)

	holder := hooked(h, func() {
		op, err := h.Durable.Get(t.Context(), a.Claim.Claim.Operation)
		if err != nil {
			t.Errorf("durable Get = %v", err)

			return
		}

		bClaim = op.Claim

		pause(t, time.Until(bClaim.ExpiresAt)+lapses*pollEvery)

		if c, err = claimAgain(t, h, runnerThree, kind); err != nil {
			t.Errorf("takeover by runner-3 = %v", err)
		}
	})

	_, err := holder.Claim(t.Context(), request(runnerTwo, shortLease, kind))
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim of the runner whose lease expired before its event = %v, want durable.ErrNoWork", err)
	}

	if bClaim.Owner != runnerTwo || bClaim.Token != a.Claim.Claim.Token+1 {
		t.Fatalf("claim of runner-2 = %+v, want token %d", bClaim, a.Claim.Claim.Token+1)
	}

	checkTwoTakeovers(t, h, f, &j, &c)
}

// checkTwoTakeovers checks that the job, the operation and the audit entries
// of j name only the runners 1 and 3, which held the phase, and not the
// runner 2, whose lease expired before its event.
func checkTwoTakeovers(t *testing.T, h Harness, f fixture, j *job.Job, c *job.Assignment) {
	t.Helper()

	got := jobOf(t, h, j.ID)
	if got.State != job.StateRunning || got.Phases[0].Executor != runnerThree || got.Phases[0].Claims != 2 {
		t.Fatalf("job after two takeovers = %s, executor %q, %d claims; want running under runner-3, 2 claims",
			got.State, got.Phases[0].Executor, got.Phases[0].Claims)
	}

	if op := opState(t, h, c.Claim); op.Claim.Owner != runnerThree || op.Claim.Token != c.Claim.Claim.Token {
		t.Fatalf("operation after two takeovers = owner %q, token %d; want runner-3, token %d", op.Claim.Owner,
			op.Claim.Token, c.Claim.Claim.Token)
	}

	entries := claimEntriesOf(t, h, f)
	if len(entries) != 2 || entries[0].Subject != runnerOne || entries[1].Subject != runnerThree {
		t.Fatalf("runner:claim entries = %d, want 2, for runner-1 and runner-3 (none for runner-2)", len(entries))
	}
}

func testExpiryDuringCancel(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	tests := []struct {
		name    string
		changed bool
		state   job.State
	}{
		{"no change began", false, job.StateCanceled},
		{"a change began", true, job.StateFailed},
	}

	for i, tc := range tests {
		unit, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", unitName(i))
		if err != nil {
			t.Fatalf("CreateUnit = %v", err)
		}

		onUnit := fixture{fence: f.fence, unit: unit}
		spec := newJobWithSteps(unit, job.OperationRefresh, changeSteps())
		first := create(t, h, onUnit, spec, dispatchOf(spec))

		a := claim(t, h, runnerOne, longRenew, runnerKind(&first))

		if tc.changed {
			record(t, h, &a, beginApply(&a))
		}

		// Renew refuses a claim with a requested cancel, so the lease gets
		// short before the cancel.
		shorten(t, h, &a)

		canceling := cancelJob(t, h, f, first.ID)
		if canceling.State != job.StateCanceling {
			t.Fatalf("job after the cancel, %s = %s, want canceling", tc.name, canceling.State)
		}

		// The runner stops before it ends the claim. The next claim of any runner
		// finds the phase and ends it.
		expired(t, a.Claim)
		noWork(t, h, runnerTwo, runnerKind(&first), "after the claim expired during a cancel, "+tc.name)

		if got := jobOf(t, h, first.ID); got.State != tc.state {
			t.Fatalf("job after the claim expired during a cancel, %s = %s, want %s", tc.name, got.State, tc.state)
		}

		if op := opState(t, h, a.Claim); op.State != durable.StateCanceled {
			t.Fatalf("operation after the claim expired during a cancel, %s = %s, want canceled", tc.name, op.State)
		}

		// The unit is free: a new change on it starts at once.
		second := create(t, h, onUnit, newJobWithSteps(unit, job.OperationRefresh, changeSteps()), nil)

		started, err := h.Jobs.Apply(t.Context(), dispatch(&second), f.fence,
			[]audit.Entry{entry(unit, job.ActionJobRead)})
		if err != nil {
			t.Fatalf("dispatch on the unit after the canceled job, %s = %v", tc.name, err)
		}

		// The operation of the new job must not stay for the next case.
		cancelJob(t, h, f, started.ID)
	}
}

// newJobWithSteps returns a queued job on unit with the steps of its spec.
func newJobWithSteps(unit identity.Object, op job.Operation, steps []job.Step) *job.Job {
	j := newJob(unit, op, "")
	j.Spec.Steps = steps

	return j
}

func testClaimClock(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationRefresh, steps: changeSteps()})
	at := time.Date(2031, time.March, 4, 5, 6, 7, 0, time.UTC)

	c := job.NewClaimer(h.Jobs, h.Durable, h.Namespaces, job.WithClaimerClock(func() time.Time { return at }))

	if _, err := c.Claim(t.Context(), request(runnerOne, time.Minute, runnerKind(&j))); err != nil {
		t.Fatalf("Claim = %v", err)
	}

	entries := claimEntriesOf(t, h, f)
	if len(entries) != 1 || !entries[0].At.Equal(at) {
		t.Fatalf("runner:claim entries = %d, want 1 with the time %s of the injected clock", len(entries), at)
	}
}
