package jobtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// NoGates is the job.GateGuard of a test harness whose units no enforced gate
// covers: it returns an empty list for every unit. The cases of the gates at
// the claim build their own claimer with a guard that they control. A server
// uses the guard over its gate store.
type NoGates struct{}

// Enforced implements job.GateGuard. It finds no gate.
func (NoGates) Enforced(context.Context, identity.Object) ([]job.EnforcedGate, error) {
	return []job.EnforcedGate{}, nil
}

// claimGateCases returns the cases of the gates at the claim: each claim meets
// the enforced gates of its unit again, so a gate saved after the start stops
// the phase.
func claimGateCases() []suiteCase {
	return []suiteCase{
		{"claim gates: the answer of the guard decides each claim", testClaimGates},
		{"claim gates: a gate saved after a claim stops the takeover", testClaimGateTakeover},
	}
}

// switchGates is a job.GateGuard whose answer a case sets between its calls.
type switchGates struct {
	mu       sync.Mutex
	enforced []job.EnforcedGate
	err      error
	asked    []string
}

// Enforced implements job.GateGuard.
func (g *switchGates) Enforced(_ context.Context, unit identity.Object) ([]job.EnforcedGate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.asked = append(g.asked, unit.String())

	return g.enforced, g.err
}

// set makes enforced and err the next answer.
func (g *switchGates) set(enforced []job.EnforcedGate, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.enforced, g.err = enforced, err
}

// errGateStore is the error of a guard that cannot read the gates. Its text
// holds a detail that the refusal must not show.
var errGateStore = errors.New("gate store down: dsn=postgres://ada:hunter2@db")

// gateAt returns an enforced gate id at the decision point point.
func gateAt(id, point string) job.EnforcedGate {
	return job.EnforcedGate{Scope: "unit:acme/opentofu/network", ID: id, Point: point}
}

// claimGateCase is one answer of the guard and what the claim must do.
type claimGateCase struct {
	name     string
	enforced []job.EnforcedGate
	err      error
	// nilGuard builds the claimer with no guard.
	nilGuard bool
	// refused is true when the claim must fail the phase.
	refused bool
	// reason is a text that the reason of the refused phase holds.
	reason string
}

func claimGateTable() []claimGateCase {
	return []claimGateCase{
		{"no gate", nil, nil, false, false, ""},
		{"a gate in evaluate mode is not in the list", []job.EnforcedGate{}, nil, false, false, ""},
		{
			"an enforced gate at state.write saved after the start",
			[]job.EnforcedGate{gateAt("no-plans", "state.write")},
			nil, false, true,
			"no-plans at state.write",
		},
		{
			"an enforced gate at change.propose saved after the start",
			[]job.EnforcedGate{gateAt("review-first", "change.propose")},
			nil, false, true,
			"review-first at change.propose",
		},
		{
			"a gate at a decision point that the claimer does not know",
			[]job.EnforcedGate{gateAt("odd", "state.read")},
			nil, false, true, "odd at state.read",
		},
		{
			"a list with a gate and an error",
			[]job.EnforcedGate{gateAt("no-plans", "state.write")},
			errGateStore, false, true,
			"the gates of the unit cannot be read",
		},
		{"a guard that cannot read the gates", nil, errGateStore, false, true, "the gates of the unit cannot be read"},
		{"a claimer with no guard", nil, nil, true, true, "the server has no gate guard"},
	}
}

// testClaimGates starts a job for each answer of the guard, sets the answer
// after the start, and claims the phase. An empty list lets the claim run. Any
// other answer fails the phase with a reason that names the gates and writes
// a denied runner:claim entry, and the runner gets no phase.
func testClaimGates(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	for i, tc := range claimGateTable() {
		guard := &switchGates{mu: sync.Mutex{}, enforced: nil, err: nil, asked: nil}

		var c *job.DurableClaimer
		if tc.nilGuard {
			c = job.NewClaimer(h.Jobs, h.Durable, h.Namespaces, nil)
		} else {
			c = job.NewClaimer(h.Jobs, h.Durable, h.Namespaces, guard)
		}

		// The start met no gate. The gate comes after it, before the claim.
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationRefresh, steps: changeSteps()})
		guard.set(tc.enforced, tc.err)

		a, err := c.Claim(t.Context(), request(runnerOne, longRenew, runnerKind(&j)))

		if !tc.refused {
			if err != nil || a.Job.ID != j.ID {
				t.Fatalf("%s: Claim = %s, %v; want the phase of job %s", tc.name, a.Job.ID, err, j.ID)
			}

			assertState(t, h, j.ID, job.StateRunning)

			continue
		}

		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("%s: Claim = %s, %v; want durable.ErrNoWork", tc.name, a.Job.ID, err)
		}

		checkGateRefusal(t, h, f, &j, &tc)
	}
}

// checkGateRefusal checks the job, the operation and the audit entry of a
// claim that the gates refused.
func checkGateRefusal(t *testing.T, h Harness, f fixture, j *job.Job, tc *claimGateCase) {
	t.Helper()

	got := jobOf(t, h, j.ID)
	if got.State != job.StateFailed || !strings.Contains(got.Reason, tc.reason) ||
		!strings.HasPrefix(got.Reason, "the claim is refused") || got.Phases[0].Claims != 0 {
		t.Fatalf("%s: job = %s, reason %q, %d claims; want failed with no claim and a reason with %q",
			tc.name, got.State, got.Reason, got.Phases[0].Claims, tc.reason)
	}

	// The reason is a fixed text: no detail of the cause of a guard error.
	if strings.Contains(got.Reason, "hunter2") {
		t.Fatalf("%s: the reason %q shows the detail of the guard error", tc.name, got.Reason)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(got.Phases[0].Operation))
	if err != nil || op.State != durable.StateFailed {
		t.Fatalf("%s: operation = %s, %v; want failed", tc.name, op.State, err)
	}

	checkDeniedEntry(t, h, f, &got, tc.name)
}

// checkDeniedEntry checks that the refused claim of j wrote one denied
// runner:claim entry of runner-1 with the reason of the job.
func checkDeniedEntry(t *testing.T, h Harness, f fixture, j *job.Job, name string) {
	t.Helper()

	var denied []audit.Entry

	entries := claimEntriesOf(t, h, f)
	for i := range entries {
		if entries[i].JobID == string(j.ID) {
			denied = append(denied, entries[i])
		}
	}

	if len(denied) != 1 || denied[0].Outcome != audit.OutcomeDenied || denied[0].Reason != j.Reason ||
		denied[0].Subject != runnerOne || denied[0].Object != j.Spec.Unit.String() {
		t.Fatalf("%s: runner:claim entries of the job = %+v, want one denied entry of runner-1 with the reason",
			name, denied)
	}
}

// testClaimGateTakeover proves that a takeover meets the gates too: the first
// claim runs, a gate is saved, and the claim after the lapse of the first
// lease fails the phase. A plan phase changes nothing, so the takeover would
// run the phase again without the gate.
func testClaimGateTakeover(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	guard := &switchGates{mu: sync.Mutex{}, enforced: nil, err: nil, asked: nil}
	c := job.NewClaimer(h.Jobs, h.Durable, h.Namespaces, guard)

	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})

	a, err := c.Claim(t.Context(), request(runnerOne, longRenew, runnerKind(&j)))
	if err != nil {
		t.Fatalf("first Claim = %v", err)
	}

	lapse(t, h, &a)
	guard.set([]job.EnforcedGate{gateAt("no-plans", "state.write")}, nil)

	ctx, cancel := context.WithTimeout(t.Context(), claimWait)
	defer cancel()

	// The lapsed claim frees the operation at the clock of the store, so the
	// takeover asks until the claimer takes the operation and refuses it.
	for got := jobOf(t, h, j.ID); got.State != job.StateFailed; got = jobOf(t, h, j.ID) {
		if _, err = c.Claim(ctx, request(runnerTwo, time.Minute, runnerKind(&j))); err != nil &&
			!errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("takeover Claim = %v, want durable.ErrNoWork", err)
		}

		if ctx.Err() != nil {
			t.Fatalf("job after the takeover = %s, want failed by the gate", got.State)
		}

		pause(t, pollEvery)
	}

	got := jobOf(t, h, j.ID)
	if !strings.Contains(got.Reason, "no-plans at state.write") || got.Phases[0].Claims != 1 {
		t.Fatalf("job after the takeover = reason %q, %d claims; want the gate and the first claim only",
			got.Reason, got.Phases[0].Claims)
	}
}
