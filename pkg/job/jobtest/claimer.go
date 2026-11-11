package jobtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// claimerCases returns the cases of job.Claimer.
func claimerCases() []suiteCase {
	return []suiteCase{
		{"claimer: a claim takes a dispatched phase and moves its job to running", testClaimerClaim},
		{"claimer: a claim with no phase to run is ErrNoWork", testClaimerNoWork},
		{"claimer: a request with no owner, no kind or no lease is ErrInvalid", testClaimerInvalid},
		{"claimer: a claim takes only the kinds that it asks for", testClaimerKinds},
		{"claimer: a phase above MaxVersions stays unclaimed", testClaimerMaxVersions},
		{"claimer: the claim returns the fence that the namespace has now", testClaimerFence},
		{"claimer: parallel claims of one phase give one winner", testClaimerOneWinner},
		{"claimer: parallel claims of several phases give each phase once", testClaimerEachOnce},
		{"claimer: renew moves the expiry and keeps the token", testRenew},
		{"claimer: renew by a claim that is not live is ErrClaimLost", testRenewLost},
		{"claimer: renew during a cancel is ErrCanceled", testRenewCanceled},
		{"claimer: a stale fence stops renew, release and finish", testClaimerStaleFence},
		{"claimer: release gives the phase back for the next claim", testRelease},
		{"claimer: release by a claim that is not live is ErrClaimLost", testReleaseLost},
		{"claimer: release after a begin of a change fails the phase", testReleaseAfterChange},
		{"claimer: release during a cancel ends canceled, or failed after a change", testReleaseCancel},
		{"claimer: finish moves the job by the state machine", testFinish},
		{"claimer: finish by a claim that is not live is ErrClaimLost", testFinishLost},
		{"claimer: finish during a cancel never hides a failure", testFinishCancel},
		{"claimer: finish of a deliver phase needs a delivery", testFinishDeliver},
		{"claimer: a takeover is a new claim and the phase runs again", testClaimerTakeover},
		{"claimer: a takeover after a begin of a change fails the phase", testTakeoverAfterChange},
		{"claimer: a takeover after a step that changes nothing runs again", testTakeoverNoChange},
		{"claimer: a takeover of a deliver phase runs the commit lookup", testTakeoverDeliver},
		{"claimer: the steps that a claim recorded do not stop its finish", testFinishAfterSteps},
	}
}

// Values that the cases of the claimer share.
const (
	// claimWait is the longest wait for a claim that an expiry frees.
	claimWait = expiryWait
	// runnerOne and runnerTwo are the owners of the first and the second
	// claim of a phase.
	runnerOne = "runner-1"
	runnerTwo = "runner-2"
	// toolTofu is the command of the steps.
	toolTofu = "tofu"
	// editedPath and editedKey name the edit of a write-back job.
	editedPath = "network.tfvars.json"
	editedKey  = "size"
	// newVersion is a version of a durable kind above the first one.
	newVersion = 2
	// longRenew is a lease that outlasts the case.
	longRenew = time.Hour
	// lapses is how many short leases pass before a case reads an expiry.
	lapses = 3
	// firstLease is the lease of a claim that a case renews. It is long
	// enough that a slow store cannot expire it before the renew, and the
	// case waits for its end after the renew.
	firstLease = expiryWait
	// renewFirstLease is the lease of the first claim of the renew case. The
	// case waits for the end of the first lease, so half of firstLease keeps
	// the case short, and it is long enough that a slow store cannot expire
	// the claim before the renew.
	renewFirstLease = firstLease / 2
)

// request returns a claim request of owner for the kinds ks.
func request(owner string, lease time.Duration, ks ...durable.Kind) job.ClaimRequest {
	return job.ClaimRequest{Owner: owner, Kinds: ks, MaxVersions: nil, Lease: lease}
}

// runnerKind returns the durable kind of the runner phases of j.
func runnerKind(j *job.Job) durable.Kind { return job.RunnerKind(j.Spec.Pool) }

// plannedJob says which job dispatched stores: the operation, the unit and the
// steps. A job with no unit name runs on the unit of the fixture. Steps
// replaces the steps of the spec when it is not nil.
type plannedJob struct {
	unit  string
	op    job.Operation
	steps []job.Step
}

// dispatched stores the job of p, and dispatches its first phase.
func dispatched(t *testing.T, h Harness, f fixture, p plannedJob) job.Job {
	t.Helper()

	unit := f.unit

	if p.unit != "" {
		var err error

		if unit, err = h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", p.unit); err != nil {
			t.Fatalf("CreateUnit(%s) = %v", p.unit, err)
		}
	}

	j := newJob(unit, p.op, "")
	if p.steps != nil {
		j.Spec.Steps = p.steps
	}

	return create(t, h, fixture{fence: f.fence, unit: unit}, j, dispatchOf(j))
}

// unitName returns the name of the unit with the number i.
func unitName(i int) string { return fmt.Sprintf("unit-%d", i) }

// changeSteps are the steps of a refresh: the step "apply" changes resources.
func changeSteps() []job.Step {
	return []job.Step{
		{
			Name: stepPrepare, Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{toolTofu, "init"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
		},
		{
			Name: "apply", Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{toolTofu, "apply"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: true, Outputs: nil,
		},
	}
}

// claim claims a phase for owner and fails t on an error.
func claim(t *testing.T, h Harness, owner string, lease time.Duration, k durable.Kind) job.Assignment {
	t.Helper()

	a, err := h.Claimer.Claim(t.Context(), request(owner, lease, k))
	if err != nil {
		t.Fatalf("Claim(%s) = %v", owner, err)
	}

	return a
}

// claimAgain claims a phase of kind k for owner as soon as the claim before
// it expires.
func claimAgain(t *testing.T, h Harness, owner string, k durable.Kind) (job.Assignment, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), claimWait)
	defer cancel()

	tick := time.NewTicker(pollEvery)
	defer tick.Stop()

	for {
		a, err := h.Claimer.Claim(ctx, request(owner, time.Minute, k))
		if !errors.Is(err, durable.ErrNoWork) {
			return a, err
		}

		select {
		case <-ctx.Done():
			return job.Assignment{}, fmt.Errorf("no claim within %v: %w", claimWait, err)
		case <-tick.C:
		}
	}
}

// expired waits on the local clock until the expiry that the store answered
// for the claim c has passed, plus lapses polls, without a claim by another
// owner. On one host the local clock and the clock of the store agree.
func expired(t *testing.T, c job.PhaseClaim) {
	t.Helper()

	pause(t, time.Until(c.Claim.ExpiresAt)+lapses*pollEvery)
}

// lapse makes the claim of a expire: it shortens the lease of the claim to
// shortLease and waits until the expiry that the store answered has passed,
// plus lapses polls. A case claims with longRenew and calls lapse after its last write under the
// claim. So no write of the case needs a short lease to be still live, and
// a slow store cannot make a claim expire before the case wants it to: a
// slow store makes the wait longer, not shorter.
func lapse(t *testing.T, h Harness, a *job.Assignment) {
	t.Helper()

	shorten(t, h, a)
	expired(t, a.Claim)
}

// shorten moves the expiry of the claim of a to shortLease from now, with
// Renew, and keeps the new claim in a. A case that must request a cancel
// before the claim expires calls it before the cancel, because Renew
// refuses a claim with a requested cancel.
func shorten(t *testing.T, h Harness, a *job.Assignment) {
	t.Helper()

	pc, err := h.Claimer.Renew(t.Context(), a.Claim, shortLease)
	if err != nil {
		t.Fatalf("Renew to a short lease = %v", err)
	}

	// An adapter that keeps the later expiry would make expired wait for the
	// first lease, up to the timeout of the test, with no message.
	if !pc.Claim.ExpiresAt.Before(a.Claim.Claim.ExpiresAt) {
		t.Fatalf("Renew to %v did not move the expiry earlier: %v after %v", shortLease, pc.Claim.ExpiresAt,
			a.Claim.Claim.ExpiresAt)
	}

	a.Claim = pc
}

// pause waits for d, or until the case ends.
func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
	}
}

// opState returns the state of the durable operation of c.
func opState(t *testing.T, h Harness, c job.PhaseClaim) durable.Operation {
	t.Helper()

	op, err := h.Durable.Get(t.Context(), c.Claim.Operation)
	if err != nil {
		t.Fatalf("durable Get = %v", err)
	}

	return op
}

// noWork fails t unless a claim of owner for the kind k finds no phase. what
// says why the case expects it.
func noWork(t *testing.T, h Harness, owner string, k durable.Kind, what string) {
	t.Helper()

	_, err := h.Claimer.Claim(t.Context(), request(owner, time.Minute, k))
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim %s = %v, want durable.ErrNoWork", what, err)
	}
}

// jobOf returns the job id and fails t on an error.
func jobOf(t *testing.T, h Harness, id job.ID) job.Job {
	t.Helper()

	got, err := h.Jobs.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%s) = %v", id, err)
	}

	return got
}

// cancelJob asks to cancel the job id, as Service.Cancel does.
func cancelJob(t *testing.T, h Harness, f fixture, id job.ID) job.Job {
	t.Helper()

	cur := jobOf(t, h, id)

	return apply(t, h, f, change(&cur, job.EventCancel))
}

// begun returns the record of a step that began under the claim of a.
func begun(a *job.Assignment, name string) job.StepRecord {
	return job.StepRecord{
		Name: name, Token: a.Claim.Claim.Token, ExitCode: 0, StartedAt: time.Now().UTC(), FinishedAt: time.Time{},
		Log: nil,
	}
}

// ended returns the record of a step that began and ended under the claim of
// a.
func ended(a *job.Assignment, name string) job.StepRecord {
	r := begun(a, name)
	r.FinishedAt = r.StartedAt.Add(time.Second)

	return r
}

// record stores r in the step records of the phase of a.
func record(t *testing.T, h Harness, a *job.Assignment, r job.StepRecord) {
	t.Helper()

	if err := h.Domain.RecordStepBegin(t.Context(), a.Claim, r); err != nil {
		t.Fatalf("RecordStepBegin(%s) = %v", r.Name, err)
	}
}

// beginApply and endApply return the record of the step "apply", which
// changes resources, as begun and as ended under the claim of a.
func beginApply(a *job.Assignment) job.StepRecord { return begun(a, "apply") }
func endApply(a *job.Assignment) job.StepRecord   { return ended(a, "apply") }

func testClaimerClaim(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})

	a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
	c := a.Claim

	if c.Job != j.ID || c.Phase != 0 || c.Fence != f.fence || string(c.Claim.Operation) != j.Phases[0].Operation ||
		c.Claim.Token != 1 || c.Claim.Owner != runnerOne || !c.Claim.ExpiresAt.After(time.Now()) {
		t.Fatalf("claim = %+v, fence %+v; want job %s phase 0 token 1 of runner-1", c, c.Fence, j.ID)
	}

	checkAssigned(t, h, &a)
}

// checkAssigned checks the job of the assignment a, and the job and the
// operation that the store holds after the claim.
func checkAssigned(t *testing.T, h Harness, a *job.Assignment) {
	t.Helper()

	if a.Job.State != job.StateRunning || a.Job.Phases[0].Claims != 1 || a.Job.Phases[0].Executor != runnerOne ||
		a.Job.Phases[0].StartedAt.IsZero() || a.Plan != nil {
		t.Fatalf("assignment job = %s, claims %d, executor %q, plan %v; want running, 1 claim, runner-1, no plan",
			a.Job.State, a.Job.Phases[0].Claims, a.Job.Phases[0].Executor, a.Plan)
	}

	if a.Profile == "" || a.Profile != a.Job.Spec.Profile {
		t.Fatalf("assignment profile = %q, want the profile %q of the job", a.Profile, a.Job.Spec.Profile)
	}

	if got := jobOf(t, h, a.Claim.Job); got.State != job.StateRunning || got.Version != a.Job.Version {
		t.Fatalf("stored job = %s, version %d; want running, version %d", got.State, got.Version, a.Job.Version)
	}

	if op := opState(t, h, a.Claim); op.State != durable.StateRunning || op.Claim.Token != 1 {
		t.Fatalf("operation = %s, token %d; want running, token 1", op.State, op.Claim.Token)
	}
}

func testClaimerNoWork(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	queued := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	_, err := h.Claimer.Claim(t.Context(), request(runnerOne, time.Minute, runnerKind(&queued)))
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim with no dispatched phase = %v, want durable.ErrNoWork", err)
	}
}

func testClaimerInvalid(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	k := runnerKind(&j)

	tests := []struct {
		name string
		req  job.ClaimRequest
	}{
		{"no owner", request("", time.Minute, k)},
		{"no kind", request(runnerOne, time.Minute)},
		{"an empty kind", request(runnerOne, time.Minute, "")},
		{"no lease", request(runnerOne, 0, k)},
		{"a negative lease", request(runnerOne, -time.Second, k)},
		{"a negative version limit", job.ClaimRequest{
			Owner: runnerOne, Kinds: []durable.Kind{k}, MaxVersions: map[durable.Kind]int{k: -1}, Lease: time.Minute,
		}},
	}

	for _, tc := range tests {
		if _, err := h.Claimer.Claim(t.Context(), tc.req); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("Claim with %s = %v, want ErrInvalid", tc.name, err)
		}
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateDispatched {
		t.Fatalf("job after refused claims = %s, want dispatched", got.State)
	}
}

func testClaimerKinds(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})

	for _, k := range []durable.Kind{job.KindWriteBack, job.RunnerKind("rpl-other")} {
		noWork(t, h, runnerOne, k, "of kind "+string(k))
	}

	a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
	if a.Claim.Job != j.ID {
		t.Fatalf("claim of the pool kind = job %s, want %s", a.Claim.Job, j.ID)
	}
}

func testClaimerMaxVersions(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationPlan, "")
	d := dispatchOf(j)
	d.Version = newVersion

	j = new(create(t, h, f, j, d))
	k := runnerKind(j)

	limits := []struct {
		name  string
		limit map[durable.Kind]int
		won   bool
	}{
		{"a limit below the version", map[durable.Kind]int{k: 1}, false},
		{"a limit of another kind", map[durable.Kind]int{job.KindWriteBack: 1}, true},
		{"a limit at the version", map[durable.Kind]int{k: newVersion}, true},
	}

	for _, tc := range limits {
		req := job.ClaimRequest{Owner: runnerOne, Kinds: []durable.Kind{k}, MaxVersions: tc.limit, Lease: time.Minute}

		a, err := h.Claimer.Claim(t.Context(), req)
		if tc.won {
			if err != nil || a.Claim.Job != j.ID {
				t.Fatalf("Claim with %s = job %s, %v; want %s", tc.name, a.Claim.Job, err, j.ID)
			}

			return
		}

		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("Claim with %s = %v, want durable.ErrNoWork", tc.name, err)
		}

		if got := jobOf(t, h, j.ID); got.State != job.StateDispatched || got.Phases[0].Claims != 0 {
			t.Fatalf(
				"job after a refused claim = %s, %d claims; want dispatched, none",
				got.State,
				got.Phases[0].Claims,
			)
		}
	}
}

func testClaimerPlanVersion(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := writeState(t, h, f, false)

	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationApply, steps: nil})

	a := claim(t, h, runnerOne, longRenew, runnerKind(&j))
	if a.Job.Phases[0].StateVersion != first {
		t.Fatalf("plan phase state version = %q, want %s", a.Job.Phases[0].StateVersion, first)
	}
}

func testClaimerFence(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})

	bumped, err := h.Domain.BumpFence(t.Context(), f.fence)
	if err != nil {
		t.Fatalf("BumpFence = %v", err)
	}

	a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
	if a.Claim.Fence != bumped || bumped == f.fence {
		t.Fatalf("claim fence = %+v, want the new fence %+v", a.Claim.Fence, bumped)
	}
}

func testClaimerOneWinner(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	k := runnerKind(&j)

	results := make([]job.Assignment, racers)

	errs := race(func(i int) error {
		a, err := h.Claimer.Claim(t.Context(), request(owner(i), time.Minute, k))
		results[i] = a

		return err
	})

	wins := 0

	for i, err := range errs {
		switch {
		case err == nil:
			wins++

			if results[i].Claim.Job != j.ID || results[i].Claim.Claim.Owner != owner(i) {
				t.Fatalf("winner %d = %+v, want job %s owned by %s", i, results[i].Claim, j.ID, owner(i))
			}
		case !errors.Is(err, durable.ErrNoWork):
			t.Fatalf("claimer %d = %v, want a claim or durable.ErrNoWork", i, err)
		default:
			// The claimer found no phase: another claimer won it.
		}
	}

	if wins != 1 {
		t.Fatalf("%d of %d claimers won one phase, want exactly 1", wins, racers)
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateRunning || got.Phases[0].Claims != 1 {
		t.Fatalf("job = %s, %d claims; want running with 1 claim", got.State, got.Phases[0].Claims)
	}
}

func testClaimerEachOnce(t *testing.T, h Harness) {
	t.Helper()

	const phases = racers / 2

	f := setUp(t, h)

	want := map[job.ID]bool{}

	var k durable.Kind

	for i := range phases {
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationPlan, steps: nil})
		want[j.ID], k = true, runnerKind(&j)
	}

	var (
		mu  sync.Mutex
		got = map[job.ID]int{}
	)

	errs := race(func(i int) error {
		a, err := h.Claimer.Claim(t.Context(), request(owner(i), time.Minute, k))
		if err == nil {
			mu.Lock()
			got[a.Claim.Job]++
			mu.Unlock()
		}

		return err
	})

	// Twice as many claimers as phases: the others find no work.
	won := 0

	for _, err := range errs {
		if err == nil {
			won++
		} else if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("claimer = %v, want a claim or durable.ErrNoWork", err)
		}
	}

	if won != phases {
		t.Fatalf("%d claims, want %d: %v", won, phases, got)
	}

	for id, n := range got {
		if !want[id] || n != 1 {
			t.Fatalf("job %s claimed %d times (known %v), want once", id, n, want[id])
		}
	}
}

func testRenew(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	a := claim(t, h, runnerOne, renewFirstLease, runnerKind(&j))

	got, err := h.Claimer.Renew(t.Context(), a.Claim, longRenew)
	if err != nil {
		t.Fatalf("Renew = %v", err)
	}

	if got.Job != a.Claim.Job || got.Phase != a.Claim.Phase || got.Fence != a.Claim.Fence ||
		got.Claim.Token != a.Claim.Claim.Token || got.Claim.Owner != runnerOne ||
		!got.Claim.ExpiresAt.After(a.Claim.Claim.ExpiresAt.Add(time.Minute)) {
		t.Fatalf("renewed claim = %+v, want the same claim with a later expiry than %v", got,
			a.Claim.Claim.ExpiresAt)
	}

	// The old lease is over, and the renewed claim still holds the phase.
	expired(t, a.Claim)

	noWork(t, h, runnerTwo, runnerKind(&j), "of a renewed phase")

	for _, d := range []time.Duration{0, -time.Second} {
		if _, err = h.Claimer.Renew(t.Context(), got, d); !errors.Is(err, job.ErrInvalid) {
			t.Fatalf("Renew with lease %v = %v, want ErrInvalid", d, err)
		}
	}
}

func testRenewLost(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claim(t, h, runnerOne, longRenew, runnerKind(&j))

	lapse(t, h, &first)

	// The claim ran out and no other claim took the phase.
	if _, err := h.Claimer.Renew(t.Context(), first.Claim, time.Minute); !errors.Is(err, job.ErrClaimLost) ||
		!errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("Renew of an expired claim = %v, want ErrClaimLost wrapping durable.ErrClaimLost", err)
	}

	second, err := claimAgain(t, h, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	if _, err = h.Claimer.Renew(t.Context(), first.Claim, time.Minute); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("Renew by the replaced claim = %v, want ErrClaimLost", err)
	}

	if _, err = h.Claimer.Renew(t.Context(), second.Claim, time.Minute); err != nil {
		t.Fatalf("Renew by the new claim = %v", err)
	}
}

func testRenewCanceled(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

	if got := cancelJob(t, h, f, j.ID); got.State != job.StateCanceling {
		t.Fatalf("cancel of a running job = %s, want canceling", got.State)
	}

	if _, err := h.Claimer.Renew(t.Context(), a.Claim, time.Minute); !errors.Is(err, job.ErrCanceled) ||
		!errors.Is(err, durable.ErrCanceled) {
		t.Fatalf("Renew during a cancel = %v, want ErrCanceled wrapping durable.ErrCanceled", err)
	}
}

func testClaimerStaleFence(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	var claims [3]job.PhaseClaim

	for i := range claims {
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationPlan, steps: nil})
		claims[i] = claim(t, h, owner(i), time.Minute, runnerKind(&j)).Claim
	}

	if _, err := h.Domain.BumpFence(t.Context(), f.fence); err != nil {
		t.Fatalf("BumpFence = %v", err)
	}

	calls := []struct {
		name string
		call func(c job.PhaseClaim) error
	}{
		{"Renew", func(c job.PhaseClaim) error { _, err := h.Claimer.Renew(t.Context(), c, time.Minute); return err }},
		{"Release", func(c job.PhaseClaim) error { return h.Claimer.Release(t.Context(), c) }},
		{"Finish", func(c job.PhaseClaim) error {
			return h.Claimer.Finish(t.Context(), c, job.Outcome{Succeeded: true, Message: ""})
		}},
	}

	for i, tc := range calls {
		if err := tc.call(claims[i]); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("%s with a stale fence = %v, want namespace.ErrFenced", tc.name, err)
		}

		// A refused call changes nothing: the claim and the job stay as they were.
		got := jobOf(t, h, claims[i].Job)
		op := opState(t, h, claims[i])

		if op.State != durable.StateRunning || op.Claim.Token != claims[i].Claim.Token ||
			got.State != job.StateRunning {
			t.Errorf("after %s with a stale fence: operation %s, job %s; want both running", tc.name, op.State,
				got.State)
		}
	}
}

func testRelease(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationApply, steps: changeSteps()})
	first := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

	// A begin of a step that changes nothing does not stop the next claim.
	record(t, h, &first, ended(&first, stepPrepare))

	if err := h.Claimer.Release(t.Context(), first.Claim); err != nil {
		t.Fatalf("Release = %v", err)
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateDispatched {
		t.Fatalf("job after a release = %s, want dispatched", got.State)
	}

	if op := opState(t, h, first.Claim); op.State != durable.StatePending || op.Attempt != 1 {
		t.Fatalf("operation after a release = %s, attempt %d; want pending, attempt 1", op.State, op.Attempt)
	}

	second := claim(t, h, runnerTwo, time.Minute, runnerKind(&j))
	if second.Claim.Job != j.ID || second.Claim.Claim.Token != 2 || second.Job.Phases[0].Claims != 2 ||
		second.Job.State != job.StateRunning {
		t.Fatalf("claim after a release = job %s, token %d, %d claims, %s; want %s, token 2, 2 claims, running",
			second.Claim.Job, second.Claim.Claim.Token, second.Job.Phases[0].Claims, second.Job.State, j.ID)
	}
}

func testReleaseLost(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claim(t, h, runnerOne, longRenew, runnerKind(&j))

	lapse(t, h, &first)

	second, err := claimAgain(t, h, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	if err = h.Claimer.Release(t.Context(), first.Claim); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("Release by the replaced claim = %v, want ErrClaimLost", err)
	}

	// The replaced holder changed nothing: the new claim still holds the phase.
	if got := jobOf(t, h, j.ID); got.State != job.StateRunning || got.Phases[0].Executor != runnerTwo {
		t.Fatalf("job after a lost release = %s, executor %q; want running under runner-2", got.State,
			got.Phases[0].Executor)
	}

	if err = h.Claimer.Release(t.Context(), second.Claim); err != nil {
		t.Fatalf("Release by the new claim = %v", err)
	}

	if err = h.Claimer.Release(t.Context(), second.Claim); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("second Release = %v, want ErrClaimLost", err)
	}
}

func testReleaseAfterChange(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	tests := []struct {
		name   string
		record func(a *job.Assignment) job.StepRecord
	}{
		{"a begin with no end", beginApply},
		{"a begin and an end", endApply},
	}

	for i, tc := range tests {
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationRefresh, steps: changeSteps()})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
		record(t, h, &a, tc.record(&a))

		if err := h.Claimer.Release(t.Context(), a.Claim); err != nil {
			t.Fatalf("Release after %s = %v", tc.name, err)
		}

		got := jobOf(t, h, j.ID)
		if got.State != job.StateFailed || !strings.Contains(got.Reason, `"apply"`) {
			t.Fatalf("job after a release past %s = %s, %q; want failed with the step", tc.name, got.State, got.Reason)
		}

		if op := opState(t, h, a.Claim); op.State != durable.StateFailed {
			t.Fatalf("operation after a release past %s = %s, want failed", tc.name, op.State)
		}

		noWork(t, h, runnerTwo, runnerKind(&j), "of a failed phase")
	}
}

func testReleaseCancel(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	tests := []struct {
		name    string
		changed bool
		state   job.State
		op      durable.State
	}{
		{"no change began", false, job.StateCanceled, durable.StateCanceled},
		{"a change began", true, job.StateFailed, durable.StateCanceled},
	}

	for i, tc := range tests {
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationRefresh, steps: changeSteps()})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

		if tc.changed {
			record(t, h, &a, beginApply(&a))
		}

		cancelJob(t, h, f, j.ID)

		if err := h.Claimer.Release(t.Context(), a.Claim); err != nil {
			t.Fatalf("Release during a cancel, %s = %v", tc.name, err)
		}

		if got := jobOf(t, h, j.ID); got.State != tc.state {
			t.Fatalf("job after a release during a cancel, %s = %s, want %s", tc.name, got.State, tc.state)
		}

		if op := opState(t, h, a.Claim); op.State != tc.op {
			t.Fatalf("operation after a release during a cancel, %s = %s, want %s", tc.name, op.State, tc.op)
		}
	}
}

// finishCase is one case of Finish: the operation of the job, the outcome and
// what the job and the operation are after it.
type finishCase struct {
	name  string
	op    job.Operation
	out   job.Outcome
	state job.State
	cur   int
	opEnd durable.State
}

func testFinish(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	tests := []finishCase{
		{
			"a phase that is not the last", job.OperationApply,
			job.Outcome{Succeeded: true, Message: ""},
			job.StateQueued, 1, durable.StateSucceeded,
		},
		{
			"the last phase", job.OperationPlan,
			job.Outcome{Succeeded: true, Message: ""},
			job.StateSucceeded, 1, durable.StateSucceeded,
		},
		{
			"a phase that failed", job.OperationPlan,
			job.Outcome{Succeeded: false, Message: "exit code 1"},
			job.StateFailed, 0, durable.StateFailed,
		},
		{
			"a phase that failed with no message", job.OperationPlan,
			job.Outcome{Succeeded: false, Message: ""},
			job.StateFailed, 0, durable.StateFailed,
		},
	}

	for i := range tests {
		tc := &tests[i]
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: tc.op, steps: nil})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

		if err := h.Claimer.Finish(t.Context(), a.Claim, tc.out); err != nil {
			t.Fatalf("Finish, %s = %v", tc.name, err)
		}

		checkFinished(t, h, tc, &a)
	}
}

// checkFinished checks the job and the operation of a after the Finish of tc.
func checkFinished(t *testing.T, h Harness, tc *finishCase, a *job.Assignment) {
	t.Helper()

	got := jobOf(t, h, a.Claim.Job)
	if got.State != tc.state || got.Current != tc.cur {
		t.Fatalf("job after Finish, %s = %s at phase %d; want %s at phase %d", tc.name, got.State, got.Current,
			tc.state, tc.cur)
	}

	if tc.out.Succeeded && got.Phases[0].FinishedAt.IsZero() {
		t.Fatalf("phase after Finish, %s has no end time", tc.name)
	}

	checkReason(t, tc, &got)

	op := opState(t, h, a.Claim)
	if op.State != tc.opEnd || (tc.opEnd == durable.StateFailed && op.LastError == "") {
		t.Fatalf("operation after Finish, %s = %s, %q; want %s", tc.name, op.State, op.LastError, tc.opEnd)
	}
}

// checkReason checks that a job that failed has a reason, and that the reason
// holds the message of the executor.
func checkReason(t *testing.T, tc *finishCase, got *job.Job) {
	t.Helper()

	if !tc.out.Succeeded && got.Reason == "" {
		t.Fatalf("failed job after Finish, %s has no reason", tc.name)
	}

	if tc.out.Message != "" && tc.state == job.StateFailed && !strings.Contains(got.Reason, tc.out.Message) {
		t.Fatalf("reason after Finish, %s = %q; want the message %q", tc.name, got.Reason, tc.out.Message)
	}
}

func testFinishLost(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claim(t, h, runnerOne, longRenew, runnerKind(&j))

	lapse(t, h, &first)

	second, err := claimAgain(t, h, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	done := job.Outcome{Succeeded: true, Message: ""}
	if err = h.Claimer.Finish(t.Context(), first.Claim, done); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("Finish by the replaced claim = %v, want ErrClaimLost", err)
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateRunning {
		t.Fatalf("job after a lost Finish = %s, want running under the new claim", got.State)
	}

	if err = h.Claimer.Finish(t.Context(), second.Claim, done); err != nil {
		t.Fatalf("Finish by the new claim = %v", err)
	}

	for _, c := range []job.PhaseClaim{first.Claim, second.Claim} {
		if err = h.Claimer.Finish(t.Context(), c, done); !errors.Is(err, job.ErrClaimLost) {
			t.Fatalf("Finish of an ended phase = %v, want ErrClaimLost", err)
		}
	}

	if _, err = h.Claimer.Renew(t.Context(), second.Claim, time.Minute); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("Renew of an ended phase = %v, want ErrClaimLost", err)
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateSucceeded {
		t.Fatalf("job after the Finish = %s, want succeeded", got.State)
	}
}

func testFinishCancel(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	tests := []struct {
		name string
		op   job.Operation
		out  job.Outcome
		want job.State
	}{
		{"a failed phase", job.OperationPlan, job.Outcome{Succeeded: false, Message: "stopped"}, job.StateFailed},
		{
			"the last phase done before the cancel", job.OperationPlan,
			job.Outcome{Succeeded: true, Message: ""},
			job.StateSucceeded,
		},
		{
			"a phase that is not the last", job.OperationApply,
			job.Outcome{Succeeded: true, Message: ""},
			job.StateCanceled,
		},
	}

	for i, tc := range tests {
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: tc.op, steps: nil})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
		cancelJob(t, h, f, j.ID)

		if err := h.Claimer.Finish(t.Context(), a.Claim, tc.out); err != nil {
			t.Fatalf("Finish during a cancel, %s = %v", tc.name, err)
		}

		if got := jobOf(t, h, j.ID); got.State != tc.want {
			t.Fatalf("job after Finish during a cancel, %s = %s, want %s", tc.name, got.State, tc.want)
		}

		if op := opState(t, h, a.Claim); !op.State.Terminal() {
			t.Fatalf("operation after Finish during a cancel, %s = %s, want an end state", tc.name, op.State)
		}
	}
}

// deliverJob stores a write-back job on a new unit called name, with its
// deliver phase dispatched.
func deliverJob(t *testing.T, h Harness, f fixture, name string) job.Job {
	t.Helper()

	unit, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", name)
	if err != nil {
		t.Fatalf("CreateUnit(%s) = %v", name, err)
	}

	j := newJob(unit, job.OperationApply, "")
	j.Spec.Executor, j.Spec.Steps, j.Spec.Pool, j.Spec.Profile = job.ExecutorWriteBack, nil, "", ""
	j.Spec.Delivery = job.DeliveryDirectPush
	j.Spec.Edits = []job.FileEdit{{Path: editedPath, Keys: map[string][]byte{editedKey: []byte(`"large"`)}}}
	j.Phases = j.Phases[:1]
	j.Phases[0].Kind = job.PhaseDeliver

	return create(t, h, fixture{fence: f.fence, unit: unit}, j, dispatchOf(j))
}

func testFinishDeliver(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	j := deliverJob(t, h, f, "unit-0")
	a := claim(t, h, "instance-a", time.Minute, job.KindWriteBack)

	// Delivered did not record a commit, so the phase cannot end well.
	err := h.Claimer.Finish(t.Context(), a.Claim, job.Outcome{Succeeded: true, Message: ""})
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("Finish of a deliver phase with no delivery = %v, want ErrInvalid", err)
	}

	if _, err = h.Claimer.Renew(t.Context(), a.Claim, time.Minute); err != nil {
		t.Fatalf("the claim after a refused Finish = %v, want a live claim", err)
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateRunning {
		t.Fatalf("job after a refused Finish = %s, want running", got.State)
	}

	// A deliver phase that failed ends failed, with no delivery.
	refused := job.Outcome{Succeeded: false, Message: "push refused"}
	if err = h.Claimer.Finish(t.Context(), a.Claim, refused); err != nil {
		t.Fatalf("Finish of a failed deliver phase = %v", err)
	}

	if got := jobOf(t, h, j.ID); got.State != job.StateFailed || got.Delivery != nil {
		t.Fatalf("job after a failed deliver phase = %s, delivery %v; want failed, none", got.State, got.Delivery)
	}
}

func testClaimerTakeover(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claim(t, h, runnerOne, longRenew, runnerKind(&j))

	lapse(t, h, &first)

	second, err := claimAgain(t, h, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	got := second.Job
	if second.Claim.Claim.Operation != first.Claim.Claim.Operation ||
		second.Claim.Claim.Token != first.Claim.Claim.Token+1 ||
		got.State != job.StateRunning ||
		got.Phases[0].Claims != 2 ||
		got.Phases[0].Executor != runnerTwo {
		t.Fatalf("takeover = operation %s, token %d, %s, %d claims, executor %s; want the same operation, token %d, "+
			"running, 2 claims, runner-2", second.Claim.Claim.Operation, second.Claim.Claim.Token, got.State,
			got.Phases[0].Claims, got.Phases[0].Executor, first.Claim.Claim.Token+1)
	}

	if op := opState(t, h, second.Claim); op.Expiries != 1 {
		t.Fatalf("operation expiries after a takeover = %d, want 1", op.Expiries)
	}
}

func testTakeoverAfterChange(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	tests := []struct {
		name   string
		record func(a *job.Assignment) job.StepRecord
	}{
		{"a begin with no end", beginApply},
		{"a begin and an end", endApply},
	}

	for i, tc := range tests {
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationRefresh, steps: changeSteps()})
		a := claim(t, h, runnerOne, longRenew, runnerKind(&j))
		record(t, h, &a, tc.record(&a))

		lapse(t, h, &a)

		// The phase has no other claimer to take, so the claim fails the phase
		// and finds no work.
		_, err := h.Claimer.Claim(t.Context(), request(runnerTwo, time.Minute, runnerKind(&j)))
		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("takeover after %s = %v, want durable.ErrNoWork", tc.name, err)
		}

		got := jobOf(t, h, j.ID)
		if got.State != job.StateFailed || !strings.Contains(got.Reason, `"apply"`) ||
			!strings.Contains(got.Reason, "state of the unit") {
			t.Fatalf("job after a takeover after %s = %s, %q; want failed with the step and the advice", tc.name,
				got.State, got.Reason)
		}

		if got.Phases[0].Claims != 1 {
			t.Fatalf(
				"claims after a refused takeover = %d, want 1: the claim did not run the phase",
				got.Phases[0].Claims,
			)
		}

		if op := opState(t, h, a.Claim); op.State != durable.StateFailed {
			t.Fatalf("operation after a takeover after %s = %s, want failed", tc.name, op.State)
		}
	}
}

func testTakeoverNoChange(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	// The claim looks for another phase after it fails the first one.
	failed := dispatched(t, h, f, plannedJob{unit: "unit-0", op: job.OperationRefresh, steps: changeSteps()})
	a := claim(t, h, runnerOne, longRenew, runnerKind(&failed))
	record(t, h, &a, beginApply(&a))

	other := dispatched(t, h, f, plannedJob{unit: "unit-1", op: job.OperationRefresh, steps: changeSteps()})
	b := claim(t, h, runnerOne, longRenew, runnerKind(&other))
	record(t, h, &b, ended(&b, stepPrepare))

	lapse(t, h, &a)
	lapse(t, h, &b)

	got, err := claimAgain(t, h, runnerTwo, runnerKind(&other))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	if got.Claim.Job != other.ID || got.Job.State != job.StateRunning || got.Job.Phases[0].Claims != 2 {
		t.Fatalf(
			"takeover = job %s, %s, %d claims; want %s running with 2 claims (not %s)",
			got.Claim.Job,
			got.Job.State,
			got.Job.Phases[0].Claims,
			other.ID,
			failed.ID,
		)
	}

	if first := jobOf(t, h, failed.ID); first.State != job.StateFailed {
		t.Fatalf("job whose change began = %s, want failed by the same claim pass", first.State)
	}

	// The step records of the earlier claim stay in the record of the phase.
	if n := len(got.Job.Phases[0].Steps); n != 1 || got.Job.Phases[0].Steps[0].Token != b.Claim.Claim.Token {
		t.Fatalf("step records after a takeover = %+v; want the record of the first claim", got.Job.Phases[0].Steps)
	}
}

func testTakeoverDeliver(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	first := deliverJob(t, h, f, "unit-0")
	a := claim(t, h, "instance-a", time.Minute, job.KindWriteBack)

	if job.NeedsCommitLookup(&a.Job.Phases[0]) {
		t.Fatal("first claim of a deliver phase needs a commit lookup, want none")
	}

	if err := h.Claimer.Release(t.Context(), a.Claim); err != nil {
		t.Fatalf("Release = %v", err)
	}

	// A later claim after a release is a claim after the first.
	b := claim(t, h, "instance-b", longRenew, job.KindWriteBack)
	if b.Claim.Job != first.ID || !job.NeedsCommitLookup(&b.Job.Phases[0]) {
		t.Fatalf("second claim = job %s, %d claims; want %s with a commit lookup", b.Claim.Job, b.Job.Phases[0].Claims,
			first.ID)
	}

	// The commit began, and the instance stopped. The deliver phase has no
	// step with a spec, so the takeover runs again, with the lookup.
	record(t, h, &b, begun(&b, job.WriteBackStepCommit))
	lapse(t, h, &b)

	c, err := claimAgain(t, h, "instance-c", job.KindWriteBack)
	if err != nil {
		t.Fatalf("takeover of a deliver phase = %v", err)
	}

	if c.Job.State != job.StateRunning || c.Job.Phases[0].Claims != 3 || !job.NeedsCommitLookup(&c.Job.Phases[0]) {
		t.Fatalf("takeover of a deliver phase = %s, %d claims, lookup %v; want running, 3 claims, a commit lookup",
			c.Job.State, c.Job.Phases[0].Claims, job.NeedsCommitLookup(&c.Job.Phases[0]))
	}

	found := false

	for _, r := range c.Job.Phases[0].Steps {
		found = found || r.Name == job.WriteBackStepCommit
	}

	if !found {
		t.Fatalf("step records of the takeover = %+v; want the begin of %q", c.Job.Phases[0].Steps,
			job.WriteBackStepCommit)
	}
}

func testFinishAfterSteps(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

	// The executor records its steps in the durable operation. A second
	// record of one step is refused, and the claim stays live.
	fence := a.Claim.Claim.Step("fetch")
	if err := h.Durable.RecordStep(t.Context(), fence, nil); err != nil {
		t.Fatalf("durable RecordStep = %v", err)
	}

	if err := h.Durable.RecordStep(t.Context(), fence, nil); !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second durable RecordStep = %v, want durable.ErrStepDone", err)
	}

	if err := h.Claimer.Finish(t.Context(), a.Claim, job.Outcome{Succeeded: true, Message: ""}); err != nil {
		t.Fatalf("Finish after the steps = %v", err)
	}

	op := opState(t, h, a.Claim)
	if _, ok := op.Step("fetch"); !ok || op.State != durable.StateSucceeded {
		t.Fatalf("operation after Finish = %s, steps %+v; want succeeded with the step record", op.State, op.Steps)
	}
}
