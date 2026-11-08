package jobstore_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// The write paths of the tests share one clock time, one namespace and one
// claim.
const (
	testClockMs    = int64(1_700_000_000_000)
	testNamespace  = namespace.Name("acme")
	testUnitID     = "unt-1"
	testClaimToken = int64(7)
)

// testUnit returns the unit of the jobs of the tests.
func testUnit(t *testing.T) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", testNamespace, "net")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return unit.WithUnitID(testUnitID)
}

// testFence returns the fence of the namespace of the tests.
func testFence() namespace.Fence {
	return namespace.Fence{Namespace: testNamespace, FailoverVersion: 0}
}

// testClaim returns the claim of the first phase of the jobs of the tests.
func testClaim() durable.Claim {
	return durable.Claim{Operation: "op-1", Owner: "runner-1", Token: testClaimToken, ExpiresAt: time.Time{}}
}

// testPhase returns a phase of kind k with the operation op.
func testPhase(kind job.PhaseKind, op string) job.Phase {
	return job.Phase{
		Kind: kind, Operation: op, Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil,
		Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}
}

// testJob returns the job of the tests with every field set. A test row
// changes the fields of its path only.
func testJob(unit identity.Object, updatedAt time.Time) job.Job {
	return job.Job{
		ID: "job-1",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationApply, Purpose: job.PurposeNone, Executor: job.ExecutorRunner,
			Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
			Steps: []job.Step{{
				Name: "apply", Role: job.RoleTool, Phase: job.PhaseApply, Args: nil, Env: nil, Dir: "",
				Timeout: 0, AllowFailure: false, ChangesResources: true, Outputs: nil,
			}},
			Pool: "", Profile: "", Edits: nil, Delivery: "", Defaulted: nil,
		},
		State: job.StateQueued, Wait: job.WaitNone, Reason: "",
		Phases:  []job.Phase{testPhase(job.PhaseApply, "")},
		Current: 0, StartedBy: "ada", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
		Trigger: nil, Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "",
		RequestHash: "", Version: 1, CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt,
		StateChangedAt: time.Time{},
	}
}

// awaitJob returns the job of the closeAwaiting path: a write-back job that
// waits for the state version of its unit.
func awaitJob(id job.ID, unit identity.Object, updatedAt time.Time) job.Job {
	j := testJob(unit, updatedAt)
	j.ID = id
	j.State = job.StateAwaitingState
	j.Spec.Executor = job.ExecutorWriteBack
	j.Phases = []job.Phase{testPhase(job.PhaseDeliver, "")}
	j.Delivery = &job.DeliveryResult{
		Delivery: job.DeliveryDirectPush, Commit: "c0ffee", Branch: "main", ReviewRequest: "",
		MergedAt: time.Time{}, DeliveredAt: updatedAt.Add(-time.Hour),
	}

	return j
}

// closedJob returns the job of the setVerificationIn path: a write-back job
// that a state version closed.
func closedJob(unit identity.Object, updatedAt time.Time) job.Job {
	j := testJob(unit, updatedAt)
	j.ID = "job-verify"
	j.State = job.StateSucceeded
	j.Spec.Executor = job.ExecutorWriteBack
	j.Phases = []job.Phase{testPhase(job.PhaseDeliver, "")}
	j.Closed = &job.StateVersion{
		Unit: unit, VersionID: "sv-1", Commit: "c0ffee", Job: "job-verify",
		At: updatedAt.Add(-time.Hour), Final: true,
	}
	j.Verification = job.VerificationPending

	return j
}

// runningJob returns the job of the record and save paths: a runner job that
// runs the first phase of the operation op-1.
func runningJob(id job.ID, unit identity.Object, updatedAt time.Time) job.Job {
	j := testJob(unit, updatedAt)
	j.ID = id
	j.State = job.StateRunning
	j.Phases = []job.Phase{testPhase(job.PhaseApply, "op-1")}

	return j
}

// changeOf returns a change of the job id from the version v by the event ev.
func changeOf(id job.ID, v int64, ev job.Event) job.Change {
	return job.Change{
		Job: id, Version: v, Event: ev, Wait: job.WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{},
		Approval: nil, End: nil,
	}
}

// stateVersionOf returns the final state version of the unit at the time at.
func stateVersionOf(unit identity.Object, at time.Time) job.StateVersion {
	return job.StateVersion{
		Unit: unit, VersionID: "sv-1", Commit: "c0ffee", Job: "", At: at, Final: true,
	}
}

// phaseClaimOf returns the claim of the first phase of the job id.
func phaseClaimOf(id job.ID) job.PhaseClaim {
	return job.PhaseClaim{Job: id, Phase: 0, Claim: testClaim(), Fence: testFence()}
}

// readJob reads the job id back through the store.
func readJob(t *testing.T, s *jobstore.Store, id job.ID) job.Job {
	t.Helper()

	j, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get job %s: %v", id, err)
	}

	return j
}

// writeApply performs one write of the job id through the applyIn path: the
// event need-approval on a queued job, and the event cancel on the job that
// waits for approval.
func writeApply(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)

	ev := job.EventNeedApproval
	if cur.State != job.StateQueued {
		ev = job.EventCancel
	}

	_, err := s.Apply(t.Context(), changeOf(id, cur.Version, ev), testFence(), nil)
	if err != nil {
		t.Fatalf("apply %s: %v", ev, err)
	}
}

// writeAwait closes the awaiting job id through the closeAwaiting path, with
// a final state version of the unit at the clock time.
func writeAwait(t *testing.T, s *jobstore.Store, id job.ID, unit identity.Object, clock time.Time) {
	t.Helper()

	closed, err := s.ObserveStateVersion(t.Context(), stateVersionOf(unit, clock), testFence())
	if err != nil {
		t.Fatalf("observe state version: %v", err)
	}

	if len(closed) != 1 || closed[0].ID != id {
		t.Fatalf("closed %d jobs, want the job %s", len(closed), id)
	}
}

// writeVerification performs one write of the job id through the
// setVerificationIn path, with the other verdict on the second write.
func writeVerification(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)

	v := job.VerificationVerified
	if cur.Verification == v {
		v = job.VerificationUnverified
	}

	if err := s.SetVerification(t.Context(), id, v, testFence(), nil); err != nil {
		t.Fatalf("set verification %s: %v", v, err)
	}
}

// writeStepRecord appends one step record to the job id through the
// AppendStepRecord path.
func writeStepRecord(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)
	name := fmt.Sprintf("step-%d", len(cur.Phases[0].Steps)+1)

	r := job.StepRecord{
		Name: name, Token: testClaimToken, ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{}, Log: nil,
	}

	if err := s.AppendStepRecord(t.Context(), phaseClaimOf(id), r); err != nil {
		t.Fatalf("append step record %s: %v", name, err)
	}
}

// writeSave performs one write of the job id through the save path: it begins
// the step apply on the first write, and ends it on the second.
func writeSave(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)
	pc := phaseClaimOf(id)

	if len(cur.Phases[0].Steps) == 0 {
		if err := s.BeginStep(t.Context(), pc, "apply"); err != nil {
			t.Fatalf("begin step: %v", err)
		}

		return
	}

	if err := s.EndStep(t.Context(), pc, "apply", 0, nil); err != nil {
		t.Fatalf("end step: %v", err)
	}
}

// writeNeedApproval performs one write of the job id through the applyIn
// path, with the event need-approval.
func writeNeedApproval(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)

	_, err := s.Apply(t.Context(), changeOf(id, cur.Version, job.EventNeedApproval), testFence(), nil)
	if err != nil {
		t.Fatalf("apply need-approval: %v", err)
	}
}

// writeClaim performs one write of the job id through the applyIn path, with
// the event claim of the first phase.
func writeClaim(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)
	claim := testClaim()
	c := changeOf(id, cur.Version, job.EventClaim)
	c.Executor, c.Claim = "runner-1", &claim

	_, err := s.Apply(t.Context(), c, testFence(), nil)
	if err != nil {
		t.Fatalf("apply claim: %v", err)
	}
}

// writePhaseDone performs one write of the job id through the applyIn path,
// with the event phase-done.
func writePhaseDone(t *testing.T, s *jobstore.Store, id job.ID) {
	t.Helper()

	cur := readJob(t, s, id)

	_, err := s.Apply(t.Context(), changeOf(id, cur.Version, job.EventPhaseDone), testFence(), nil)
	if err != nil {
		t.Fatalf("apply phase-done: %v", err)
	}
}

// checkStateChangedAt fails unless the job changed its state at want.
func checkStateChangedAt(t *testing.T, got *job.Job, want time.Time) {
	t.Helper()

	if !got.StateChangedAt.Equal(want) {
		t.Fatalf("state changed at = %v, want the clock time %v", got.StateChangedAt, want)
	}
}

// checkPhaseStartedAt fails unless the first phase of the job started at want.
func checkPhaseStartedAt(t *testing.T, got *job.Job, want time.Time) {
	t.Helper()

	if !got.Phases[0].StartedAt.Equal(want) {
		t.Fatalf("phase started at = %v, want the clock time %v", got.Phases[0].StartedAt, want)
	}
}

// checkPhaseFinishedAt fails unless the first phase of the job finished at want.
func checkPhaseFinishedAt(t *testing.T, got *job.Job, want time.Time) {
	t.Helper()

	if !got.Phases[0].FinishedAt.Equal(want) {
		t.Fatalf("phase finished at = %v, want the clock time %v", got.Phases[0].FinishedAt, want)
	}
}

// fakeTx is a jobstore.Tx over rows in memory. It carries the methods that
// the write paths of these tests reach. The embedded interface stays nil.
type fakeTx struct {
	jobstore.Tx

	now   time.Time
	rows  map[job.ID]jobstore.Row
	order []job.ID
}

// newFakeTx returns a transaction over no rows, on the clock time now.
func newFakeTx(now time.Time) *fakeTx {
	return &fakeTx{Tx: nil, now: now, rows: map[job.ID]jobstore.Row{}, order: nil}
}

// Now implements jobstore.Tx.
func (f *fakeTx) Now(context.Context) (time.Time, error) { return f.now, nil }

// Job implements jobstore.Tx.
func (f *fakeTx) Job(_ context.Context, id job.ID) (jobstore.Row, bool, error) {
	r, ok := f.rows[id]

	return r, ok, nil
}

// UpdateJob implements jobstore.Tx.
func (f *fakeTx) UpdateJob(_ context.Context, r jobstore.Row, prev int64) (bool, error) {
	cur, ok := f.rows[r.ID]
	if !ok || cur.Version != prev {
		return false, nil
	}

	f.rows[r.ID] = r

	return true, nil
}

// Awaiting implements jobstore.Tx.
func (f *fakeTx) Awaiting(_ context.Context, ns namespace.Name, unitID string) ([]jobstore.Row, error) {
	var out []jobstore.Row

	for _, id := range f.order {
		r := f.rows[id]
		if r.Namespace == ns && r.UnitID == unitID && r.State == job.StateAwaitingState {
			out = append(out, r)
		}
	}

	return out, nil
}

// StartOperation implements jobstore.Tx.
func (f *fakeTx) StartOperation(_ context.Context, req *durable.StartRequest) (string, error) {
	return "op-" + string(req.Kind), nil
}

// PutFinalVersion implements jobstore.Tx.
func (f *fakeTx) PutFinalVersion(context.Context, namespace.Name, jobstore.FinalVersion) error {
	return nil
}

// CheckClaim implements jobstore.Tx.
func (f *fakeTx) CheckClaim(context.Context, durable.Claim) error { return nil }

// AppendAudit implements jobstore.Tx.
func (f *fakeTx) AppendAudit(context.Context, []audit.Entry) error { return nil }

// put stores j as the row of its ID, as a write of the store would.
func (f *fakeTx) put(t *testing.T, j *job.Job) {
	t.Helper()

	body, err := jobstore.EncodeJobBody(j)
	if err != nil {
		t.Fatalf("encode body of job %s: %v", j.ID, err)
	}

	r := jobstore.Row{
		ID: j.ID, Namespace: j.Spec.Unit.Namespace(), UnitID: j.Spec.Unit.UnitID(), Seq: 0,
		State: j.State, Wait: j.Wait, Phase: j.Current, Changes: false, Operation: "",
		Key: j.IdempotencyKey, Version: j.Version, Body: body, CreatedMs: j.CreatedAt.UnixMilli(),
		UpdatedMs: j.UpdatedAt.UnixMilli(), StateChangedMs: j.StateChangedAt.UnixMilli(),
	}
	if j.Current < len(j.Phases) {
		r.Changes = j.Phases[j.Current].Kind.Changes()
		r.Operation = j.Phases[j.Current].Operation
	}

	if _, ok := f.rows[j.ID]; !ok {
		f.order = append(f.order, j.ID)
	}

	f.rows[j.ID] = r
}

// fakeRunner runs every read and write of a store on one fake transaction.
type fakeRunner struct {
	tx *fakeTx
}

// Write implements jobstore.Runner.
func (r fakeRunner) Write(_ context.Context, _ namespace.Fence, fn func(jobstore.Tx) error) error {
	return fn(r.tx)
}

// Read implements jobstore.Runner.
func (r fakeRunner) Read(_ context.Context, fn func(jobstore.Tx) error) error {
	return fn(r.tx)
}

// Logger implements jobstore.Runner: nil, so the store logs nothing.
func (fakeRunner) Logger() *slog.Logger { return nil }

// writePath is one path that writes a job row through a public store method.
type writePath struct {
	// first returns the job to store before the first write.
	first func() job.Job
	// write performs one write through the public store method of the path.
	write func(t *testing.T, s *jobstore.Store, id job.ID)
	// second returns the job to store before the second write, given the job
	// after the first write, or nil to write the first job again.
	second func(first job.Job) job.Job
}

// TestUpdateTimeMovesOnEachWritePath proves that each write path moves the
// update time of its job row: the first write moves it past the clock, and
// the second write moves it at least one millisecond after the first, while
// the clock of the database stands still.
func TestUpdateTimeMovesOnEachWritePath(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)

	for name, tc := range map[string]writePath{
		"applyIn": {
			first: func() job.Job {
				j := testJob(unit, clock)
				j.ID = "job-apply"

				return j
			},
			write:  writeApply,
			second: nil,
		},
		"closeAwaiting": {
			// A final state version closes a job once, so the second write
			// closes a second job, which starts at the update time of the
			// first.
			first: func() job.Job {
				return awaitJob("job-await-1", unit, clock)
			},
			write: func(t *testing.T, s *jobstore.Store, id job.ID) {
				t.Helper()

				writeAwait(t, s, id, unit, clock)
			},
			second: func(first job.Job) job.Job {
				return awaitJob("job-await-2", unit, first.UpdatedAt)
			},
		},
		"setVerificationIn": {
			first: func() job.Job {
				return closedJob(unit, clock)
			},
			write:  writeVerification,
			second: nil,
		},
		"AppendStepRecord": {
			first: func() job.Job {
				return runningJob("job-record", unit, clock)
			},
			write:  writeStepRecord,
			second: nil,
		},
		"save": {
			first: func() job.Job {
				return runningJob("job-save", unit, clock)
			},
			write:  writeSave,
			second: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx := newFakeTx(clock)
			s := jobstore.New(fakeRunner{tx: tx})

			first := tc.first()
			tx.put(t, &first)

			tc.write(t, s, first.ID)
			gotFirst := readJob(t, s, first.ID)

			if !gotFirst.UpdatedAt.After(clock) {
				t.Fatalf("updated at of the first write = %v, want after the clock time %v", gotFirst.UpdatedAt, clock)
			}

			secondID := first.ID
			if tc.second != nil {
				next := tc.second(gotFirst)
				tx.put(t, &next)
				secondID = next.ID
			}

			tc.write(t, s, secondID)
			gotSecond := readJob(t, s, secondID)

			if gotSecond.UpdatedAt.Before(gotFirst.UpdatedAt.Add(time.Millisecond)) {
				t.Fatalf("updated at of the second write = %v, want at least 1 ms after %v", gotSecond.UpdatedAt,
					gotFirst.UpdatedAt)
			}
		})
	}
}

// clockCase is one write path and the field that must keep the clock time.
type clockCase struct {
	// job returns the job to store before the write.
	job func() job.Job
	// write performs one write through the public store method of the path.
	write func(t *testing.T, s *jobstore.Store, id job.ID)
	// check reads the field that must keep the clock time.
	check func(t *testing.T, got *job.Job, clock time.Time)
}

// TestWritePathsKeepTheClockTime proves that the shift of the update time
// moves only the update time: the state change, the start of a phase and the
// finish of a phase keep the clock time when the stored update time of the
// row is ahead of the clock.
func TestWritePathsKeepTheClockTime(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	ahead := clock.Add(5 * time.Millisecond)
	unit := testUnit(t)

	for name, tc := range map[string]clockCase{
		"applyIn keeps the clock in the state change time": {
			job: func() job.Job {
				j := testJob(unit, ahead)
				j.ID = "job-apply"

				return j
			},
			write: writeNeedApproval,
			check: checkStateChangedAt,
		},
		"applyIn keeps the clock in the start of a phase": {
			job: func() job.Job {
				return runningJob("job-start", unit, ahead)
			},
			write: writeClaim,
			check: checkPhaseStartedAt,
		},
		"applyIn keeps the clock in the finish of a phase": {
			job: func() job.Job {
				return runningJob("job-finish", unit, ahead)
			},
			write: writePhaseDone,
			check: checkPhaseFinishedAt,
		},
		"closeAwaiting keeps the clock in the state change time": {
			job: func() job.Job {
				return awaitJob("job-await", unit, ahead)
			},
			write: func(t *testing.T, s *jobstore.Store, id job.ID) {
				t.Helper()

				writeAwait(t, s, id, unit, clock)
			},
			check: checkStateChangedAt,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx := newFakeTx(clock)
			s := jobstore.New(fakeRunner{tx: tx})

			j := tc.job()
			tx.put(t, &j)

			tc.write(t, s, j.ID)
			got := readJob(t, s, j.ID)
			tc.check(t, &got, clock)
		})
	}
}
