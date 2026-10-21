package jobtest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// endClaimCases returns the cases of the end of a claim: the one transaction
// of the end of the operation and the event of the job, the audit entry of a
// claim, a claim that cannot spin, and the claim check of the step records.
func endClaimCases() []suiteCase {
	return []suiteCase{
		{
			"end: a takeover between the read and the end of a holder is not changed by the holder",
			testEndAfterTakeover,
		},
		{
			"end: a fence change between the read and the end changes neither the operation nor the job",
			testEndFenceChanges,
		},
		{
			"end: a cancel between the read and the end still ends the operation and the job together",
			testEndCancelBetween,
		},
		{"end: an end of the store is bound to the claim and the phase", testApplyEndBound},
		{"end: a refused change ends no operation, and a refused end changes no job", testApplyEndAtomic},
		{"end: release and finish survive the end of the request", testEndSurvivesRequest},
		{"end: the claim of a phase that changes resources writes the audit entry of runner:claim", testClaimAudit},
		{"end: a claim of an operation whose job cannot run it ends the operation", testClaimEndsUnrunnable},
		{"end: a step record needs the live claim of the phase", testStepRecordClaim},
	}
}

// Values that the cases of the end of a claim share.
const (
	// farPhase is a phase index that no job has.
	farPhase = 99
	// foreignFailover is a failover version that no namespace has reached.
	foreignFailover = 7
)

// hookJobs is a job.Store that runs hook once, after the first Get. A claimer
// over it reads the job, and then the hook runs before the claimer writes: it
// stands for what happens between the read and the write of the claimer.
type hookJobs struct {
	job.Store

	once sync.Once
	hook func()
}

// Get implements job.Store.
func (s *hookJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := s.Store.Get(ctx, id)

	s.once.Do(s.hook)

	return j, err
}

// hooked returns a claimer over the stores of h whose job store runs hook
// after its first Get.
func hooked(h Harness, hook func()) *job.DurableClaimer {
	return job.NewClaimer(&hookJobs{Store: h.Jobs, once: sync.Once{}, hook: hook}, h.Durable, h.Namespaces)
}

// endCall is a call that ends a claim, with the states that it leaves when it
// succeeds.
type endCall struct {
	name string
	call func(ctx context.Context, c job.Claimer, pc job.PhaseClaim) error
	job  job.State
	op   durable.State
}

// endCalls returns the calls that end the claim of a plan phase. The release
// comes last: it leaves a pending operation, which a later claim of the same
// kind in a case would take before the operation of the next job.
func endCalls() []endCall {
	return []endCall{
		{
			"finish", func(ctx context.Context, c job.Claimer, pc job.PhaseClaim) error {
				return c.Finish(ctx, pc, job.Outcome{Succeeded: true, Message: ""})
			},
			job.StateSucceeded, durable.StateSucceeded,
		},
		{
			"finish with a failure", func(ctx context.Context, c job.Claimer, pc job.PhaseClaim) error {
				return c.Finish(ctx, pc, job.Outcome{Succeeded: false, Message: "exit code 1"})
			},
			job.StateFailed, durable.StateFailed,
		},
		{
			"release", func(ctx context.Context, c job.Claimer, pc job.PhaseClaim) error { return c.Release(ctx, pc) },
			job.StateDispatched, durable.StatePending,
		},
	}
}

func testEndAfterTakeover(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	calls := endCalls()

	for i := range calls {
		endAfterTakeover(t, h, f, &calls[i], unitName(i))
	}
}

// endAfterTakeover runs the call tc by a holder whose claim a takeover replaces
// between its read and its end, and checks that the holder changed nothing and
// that the new holder can run the phase to its end.
func endAfterTakeover(t *testing.T, h Harness, f fixture, tc *endCall, unit string) {
	t.Helper()

	j := dispatched(t, h, f, plannedJob{unit: unit, op: job.OperationRefresh, steps: changeSteps()})
	a := claim(t, h, runnerOne, shortLease, runnerKind(&j))

	var b job.Assignment

	// The runner B takes the phase over after the claim of A expired, between the
	// read of A and its end. A takeover finds no record of a step that changes
	// resources, so it runs the phase.
	holder := hooked(h, func() {
		expired(t, a.Claim)

		var err error

		if b, err = claimAgain(t, h, runnerTwo, runnerKind(&j)); err != nil {
			t.Errorf("takeover = %v", err)
		}
	})

	if err := tc.call(t.Context(), holder, a.Claim); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("%s by the replaced holder = %v, want ErrClaimLost", tc.name, err)
	}

	got := jobOf(t, h, j.ID)
	if got.State != job.StateRunning || got.Phases[0].Executor != runnerTwo || got.Phases[0].Claims != 2 {
		t.Fatalf(
			"job after %s by the replaced holder = %s, executor %q, claims %d; want running under runner-2, 2 claims",
			tc.name,
			got.State,
			got.Phases[0].Executor,
			got.Phases[0].Claims,
		)
	}

	if op := opState(t, h, b.Claim); op.State != durable.StateRunning || op.Claim.Owner != runnerTwo ||
		op.Claim.Token != b.Claim.Claim.Token {
		t.Fatalf("operation after %s by the replaced holder = %s, %q, token %d; want running for runner-2",
			tc.name, op.State, op.Claim.Owner, op.Claim.Token)
	}

	// B renews, begins its change and finishes. Nobody runs the phase again.
	if _, err := h.Claimer.Renew(t.Context(), b.Claim, time.Minute); err != nil {
		t.Fatalf("Renew by the new holder after %s = %v", tc.name, err)
	}

	record(t, h, &b, beginApply(&b))

	if err := h.Claimer.Finish(t.Context(), b.Claim, job.Outcome{Succeeded: true, Message: ""}); err != nil {
		t.Fatalf("Finish by the new holder after %s = %v", tc.name, err)
	}

	if got = jobOf(t, h, j.ID); got.State != job.StateSucceeded {
		t.Fatalf("job after the Finish of the new holder = %s, want succeeded", got.State)
	}

	noWork(t, h, "runner-3", runnerKind(&j), "of a phase that finished after a change began")
}

func testEndFenceChanges(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	calls := endCalls()

	for i := range calls {
		tc := &calls[i]
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationPlan, steps: nil})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
		before := jobOf(t, h, j.ID)
		fence := f.fence

		// A failover between the read and the write: the end is refused, and
		// neither the operation nor the job changes.
		holder := hooked(h, func() {
			var err error

			if fence, err = h.Domain.BumpFence(t.Context(), fence); err != nil {
				t.Errorf("BumpFence = %v", err)
			}
		})

		if err := tc.call(t.Context(), holder, a.Claim); !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("%s across a failover = %v, want namespace.ErrFenced", tc.name, err)
		}

		checkRunning(t, h, tc.name+" across a failover", &a, before.Version)

		f.fence = fence
	}
}

// checkRunning fails t unless the job of a is running at version, and its
// operation is running under the claim of a.
func checkRunning(t *testing.T, h Harness, what string, a *job.Assignment, version int64) {
	t.Helper()

	got := jobOf(t, h, a.Claim.Job)
	op := opState(t, h, a.Claim)

	if got.State != job.StateRunning || got.Version != version || op.State != durable.StateRunning ||
		op.Claim.Token != a.Claim.Claim.Token {
		t.Fatalf(
			"after %s: job %s at version %d, operation %s under token %d; want both running at version %d, token %d",
			what,
			got.State,
			got.Version,
			op.State,
			op.Claim.Token,
			version,
			a.Claim.Claim.Token,
		)
	}
}

func testEndCancelBetween(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	calls := endCalls()

	for i := range calls {
		tc := &calls[i]
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationPlan, steps: nil})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

		// A cancel changes the job between the read and the end: the claimer reads
		// the job again, and ends the operation and the job in one write.
		holder := hooked(h, func() { cancelJob(t, h, f, j.ID) })

		if err := tc.call(t.Context(), holder, a.Claim); err != nil {
			t.Fatalf("%s across a cancel = %v", tc.name, err)
		}

		got := jobOf(t, h, j.ID)
		op := opState(t, h, a.Claim)

		if !got.State.Terminal() || op.State == durable.StateRunning || op.State == durable.StatePending {
			t.Fatalf("after %s across a cancel: job %s, operation %s; want both ended", tc.name, got.State, op.State)
		}
	}
}

// takenOver dispatches a plan job on the unit called name, claims its phase for
// runnerOne, lets that claim expire and takes the phase over for runnerTwo. It
// returns the job and both assignments.
func takenOver(t *testing.T, h Harness, f fixture, name string) takeover {
	t.Helper()

	j := dispatched(t, h, f, plannedJob{unit: name, op: job.OperationPlan, steps: nil})
	first := claim(t, h, runnerOne, shortLease, runnerKind(&j))

	expired(t, first.Claim)

	second, err := claimAgain(t, h, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	return takeover{cur: jobOf(t, h, j.ID), first: first, second: second}
}

// takeover is a job, the assignment that a takeover replaced and the one
// that replaced it.
type takeover struct {
	cur           job.Job
	first, second job.Assignment
}

// endOf returns the end of the claim c by kind.
func endOf(c job.PhaseClaim, kind job.EndKind) *job.OperationEnd {
	return &job.OperationEnd{
		Claim: c.Claim, Kind: kind, Failure: durable.Failure{Message: "late", Final: true, RetryAfter: 0},
	}
}

func testApplyEndBound(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	tk := takenOver(t, h, f, unitName(0))
	cur, first, second := tk.cur, tk.first, tk.second

	// The event of the replaced claim is refused, whatever its kind of end.
	for _, kind := range []job.EndKind{job.EndRelease, job.EndComplete, job.EndFail} {
		c := change(&cur, job.EventRelease)
		c.End = endOf(first.Claim, kind)

		_, err := h.Jobs.Apply(t.Context(), c, f.fence, nil)
		if !errors.Is(err, job.ErrClaimLost) || !errors.Is(err, durable.ErrClaimLost) {
			t.Fatalf("Apply with the %s of a replaced claim = %v, want ErrClaimLost", kind, err)
		}
	}

	checkRunning(t, h, "the refused ends of a replaced claim", &second, cur.Version)

	// A claim of another phase cannot end this phase.
	other := dispatched(t, h, f, plannedJob{unit: unitName(1), op: job.OperationPlan, steps: nil})
	otherClaim := claim(t, h, runnerOne, time.Minute, runnerKind(&other))

	c := change(&cur, job.EventRelease)
	c.End = endOf(otherClaim.Claim, job.EndRelease)

	if _, err := h.Jobs.Apply(t.Context(), c, f.fence, nil); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("Apply with the claim of another phase = %v, want ErrClaimLost", err)
	}

	checkRunning(t, h, "the end with the claim of another phase", &otherClaim, other.Version+1)

	// The live claim ends the phase and the job together.
	c = change(&cur, job.EventLastPhaseDone)
	c.End = endOf(second.Claim, job.EndComplete)

	done, err := h.Jobs.Apply(t.Context(), c, f.fence, nil)
	if err != nil {
		t.Fatalf("Apply with the live claim = %v", err)
	}

	if op := opState(t, h, second.Claim); done.State != job.StateSucceeded || op.State != durable.StateSucceeded {
		t.Fatalf("after the live end: job %s, operation %s; want both succeeded", done.State, op.State)
	}
}

func testApplyEndAtomic(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))
	cur := jobOf(t, h, j.ID)
	version := cur.Version

	good := change(&cur, job.EventLastPhaseDone)
	good.End = endOf(a.Claim, job.EndComplete)

	// A refused change ends no operation: nobody sees the end of the operation
	// without the event of the job.
	for what, c := range refusedEnds(&cur, &a) {
		if _, err := h.Jobs.Apply(t.Context(), c.change, f.fence, nil); !errors.Is(err, c.want) {
			t.Fatalf("Apply with %s = %v, want %v", what, err, c.want)
		}

		checkRunning(t, h, "Apply with "+what, &a, version)
	}

	// A refused audit entry rolls the end back too.
	bad := entry(f.unit, job.ActionJobRead)
	bad.Namespace = "other"

	if _, err := h.Jobs.Apply(t.Context(), good, f.fence, []audit.Entry{bad}); err == nil {
		t.Fatal("Apply with an entry of another namespace = nil, want an error")
	}

	checkRunning(t, h, "Apply with a refused entry", &a, version)

	// A stale fence refuses both.
	fence, err := h.Domain.BumpFence(t.Context(), f.fence)
	if err != nil {
		t.Fatalf("BumpFence = %v", err)
	}

	if _, err = h.Jobs.Apply(t.Context(), good, f.fence, nil); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Apply with a stale fence = %v, want namespace.ErrFenced", err)
	}

	checkRunning(t, h, "Apply with a stale fence", &a, version)

	done, err := h.Jobs.Apply(t.Context(), good, fence, nil)
	if err != nil {
		t.Fatalf("Apply with the current fence = %v", err)
	}

	if op := opState(t, h, a.Claim); done.State != job.StateSucceeded || op.State != durable.StateSucceeded {
		t.Fatalf("after the end: job %s, operation %s; want both succeeded", done.State, op.State)
	}
}

// refusedChange is a change that the store must refuse, and the error that it
// must return.
type refusedChange struct {
	change job.Change
	want   error
}

// refusedEnds returns changes of cur with the end of the claim of a that the
// store refuses before it ends anything.
func refusedEnds(cur *job.Job, a *job.Assignment) map[string]*refusedChange {
	stale := change(cur, job.EventLastPhaseDone)
	stale.Version = cur.Version - 1
	stale.End = endOf(a.Claim, job.EndComplete)

	badEvent := change(cur, job.EventCancel)
	badEvent.End = endOf(a.Claim, job.EndComplete)

	noKind := change(cur, job.EventLastPhaseDone)
	noKind.End = endOf(a.Claim, "")

	noToken := change(cur, job.EventLastPhaseDone)
	noToken.End = endOf(a.Claim, job.EndComplete)
	noToken.End.Claim.Token = 0

	return map[string]*refusedChange{
		"a stale version":                  {change: stale, want: job.ErrConflict},
		"an event that cannot end a claim": {change: badEvent, want: job.ErrInvalid},
		"an unknown kind of end":           {change: noKind, want: job.ErrInvalid},
		"a claim with no token":            {change: noToken, want: job.ErrInvalid},
	}
}

func testEndSurvivesRequest(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	calls := endCalls()

	for i := range calls {
		tc := &calls[i]
		j := dispatched(t, h, f, plannedJob{unit: unitName(i), op: job.OperationPlan, steps: nil})
		a := claim(t, h, runnerOne, time.Minute, runnerKind(&j))

		// The runner dropped its connection: the context of the request ended.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if err := tc.call(ctx, h.Claimer, a.Claim); err != nil {
			t.Fatalf("%s after the request ended = %v", tc.name, err)
		}

		// Both writes happened, or neither: here both.
		got := jobOf(t, h, j.ID)
		if op := opState(t, h, a.Claim); got.State != tc.job || op.State != tc.op {
			t.Fatalf("after %s: job %s, operation %s; want %s and %s", tc.name, got.State, op.State, tc.job, tc.op)
		}
	}
}

// claimEntriesOf returns the audit entries of the action runner:claim in the
// namespace of f.
func claimEntriesOf(t *testing.T, h Harness, f fixture) []audit.Entry {
	t.Helper()

	return slices.DeleteFunc(allEntries(t, h, f), func(e audit.Entry) bool {
		return e.Action != string(job.ActionRunnerClaim)
	})
}

// allEntries returns the audit entries of the namespace of f.
func allEntries(t *testing.T, h Harness, f fixture) []audit.Entry {
	t.Helper()

	all, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	return all
}

func testClaimAudit(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	// A phase that only plans changes nothing: its claim writes no entry.
	plan := dispatched(t, h, f, plannedJob{unit: unitName(0), op: job.OperationPlan, steps: nil})
	p := claim(t, h, runnerOne, time.Minute, runnerKind(&plan))

	if got := claimEntriesOf(t, h, f); len(got) != 0 {
		t.Fatalf("entries after the claim of a plan phase = %d, want 0", len(got))
	}

	if err := h.Claimer.Finish(t.Context(), p.Claim, job.Outcome{Succeeded: true, Message: ""}); err != nil {
		t.Fatalf("Finish of the plan phase = %v", err)
	}

	// A phase that changes resources: the claim writes the entry in its own
	// transaction, and a takeover is a claim too.
	apply := dispatched(t, h, f, plannedJob{unit: unitName(1), op: job.OperationRefresh, steps: changeSteps()})
	a := claim(t, h, runnerOne, shortLease, runnerKind(&apply))

	got := claimEntriesOf(t, h, f)
	if len(got) != 1 {
		t.Fatalf("entries after the claim of an apply phase = %d, want 1", len(got))
	}

	checkClaimEntry(t, &got[0], &apply)

	// Renew, release and finish write no entry.
	before := len(allEntries(t, h, f))
	wroteNone := func(call string) {
		t.Helper()

		if after := len(allEntries(t, h, f)); after != before {
			t.Fatalf("%s wrote %d entries, want 0", call, after-before)
		}
	}

	if _, err := h.Claimer.Renew(t.Context(), a.Claim, time.Minute); err != nil {
		t.Fatalf("Renew = %v", err)
	}

	wroteNone("Renew")

	if err := h.Claimer.Release(t.Context(), a.Claim); err != nil {
		t.Fatalf("Release = %v", err)
	}

	wroteNone("Release")

	b := claim(t, h, runnerTwo, time.Minute, runnerKind(&apply))

	if got = claimEntriesOf(t, h, f); len(got) != 2 || got[1].Subject != runnerTwo {
		t.Fatalf("entries after the second claim = %d; want 2, the second for runner-2", len(got))
	}

	before = len(allEntries(t, h, f))

	if err := h.Claimer.Finish(t.Context(), b.Claim, job.Outcome{Succeeded: true, Message: ""}); err != nil {
		t.Fatalf("Finish = %v", err)
	}

	wroteNone("Finish")
}

// checkClaimEntry fails t unless e is the runner:claim entry of the first claim
// of j.
func checkClaimEntry(t *testing.T, e *audit.Entry, j *job.Job) {
	t.Helper()

	unit := j.Spec.Unit
	if e.Subject != runnerOne || e.Object != unit.String() || e.ObjectID != unit.UnitID() || e.JobID != string(j.ID) ||
		e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("claim entry = %+v; want runner-1 on the unit %s for job %s, kind change", *e, unit, j.ID)
	}
}

func testClaimEndsUnrunnable(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	// The job expired while it was dispatched. Its operation stays pending: the
	// store cancels the operation only for a cancel.
	stuck := dispatched(t, h, f, plannedJob{unit: unitName(0), op: job.OperationPlan, steps: nil})
	apply(t, h, f, change(&stuck, job.EventExpire))

	ok := dispatched(t, h, f, plannedJob{unit: unitName(1), op: job.OperationPlan, steps: nil})

	// A bounded claim: it must not spin on the operation of the expired job.
	ctx, cancel := context.WithTimeout(t.Context(), expiryWait)
	defer cancel()

	a, err := h.Claimer.Claim(ctx, request(runnerOne, time.Minute, runnerKind(&ok)))
	if err != nil {
		t.Fatalf("Claim past an operation of an expired job = %v", err)
	}

	if a.Claim.Job != ok.ID {
		t.Fatalf("Claim took job %s, want %s", a.Claim.Job, ok.ID)
	}

	if got := jobOf(t, h, stuck.ID); got.State != job.StateExpired {
		t.Fatalf("expired job after the claim = %s, want expired", got.State)
	}

	failed := durable.Filter{Kind: runnerKind(&ok), States: []durable.State{durable.StateFailed}, Limit: 0}

	ops, err := h.Durable.List(t.Context(), failed)
	if err != nil || len(ops) != 1 {
		t.Fatalf("failed operations = %d, %v; want 1, the operation of the expired job", len(ops), err)
	}

	noWork(t, h, runnerTwo, runnerKind(&ok), "after the operation of the expired job ended")
}

func testStepRecordClaim(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	tk := takenOver(t, h, f, unitName(0))
	cur, first, second := tk.cur, tk.first, tk.second

	for what, tc := range refusedRecords(f, &first, &second) {
		err := h.Domain.RecordStepBegin(t.Context(), tc.pc, tc.rec)
		if !errors.Is(err, tc.want) {
			t.Fatalf("RecordStepBegin with %s = %v, want %v", what, err, tc.want)
		}

		if got := jobOf(t, h, cur.ID); len(got.Phases[0].Steps) != 0 || got.Version != cur.Version {
			t.Fatalf("after %s: %d step records at version %d; want none at version %d", what,
				len(got.Phases[0].Steps), got.Version, cur.Version)
		}
	}

	if err := h.Domain.RecordStepBegin(t.Context(), second.Claim, begun(&second, "apply")); err != nil {
		t.Fatalf("RecordStepBegin with the live claim = %v", err)
	}

	if got := jobOf(t, h, cur.ID); len(got.Phases[0].Steps) != 1 {
		t.Fatalf("step records after the live write = %d, want 1", len(got.Phases[0].Steps))
	}
}

// refusedRecord is a step record that the store must refuse, with the claim
// that writes it and the error that it must return.
type refusedRecord struct {
	pc   job.PhaseClaim
	rec  job.StepRecord
	want error
}

// refusedRecords returns the writes of a step record that need the refusal of
// the claim check: a replaced claim, a phase that does not exist, a phase that
// the job is not at, a record with another token and a stale fence.
func refusedRecords(f fixture, first, second *job.Assignment) map[string]*refusedRecord {
	missing := second.Claim
	missing.Phase = farPhase

	notCurrent := second.Claim
	notCurrent.Phase = second.Claim.Phase + 1

	wrongToken := begun(second, "apply")
	wrongToken.Token++

	staleFence := second.Claim
	staleFence.Fence = namespace.Fence{
		Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion + foreignFailover,
	}

	return map[string]*refusedRecord{
		"a replaced claim":                 {first.Claim, begun(first, "apply"), job.ErrClaimLost},
		"a phase that does not exist":      {missing, begun(second, "apply"), job.ErrClaimLost},
		"a phase that the job is not at":   {notCurrent, begun(second, "apply"), job.ErrClaimLost},
		"a record with another token":      {second.Claim, wrongToken, job.ErrInvalid},
		"a stale fence on the claim of it": {staleFence, begun(second, "apply"), namespace.ErrFenced},
	}
}
