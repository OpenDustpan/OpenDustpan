// Package jobtest is the conformance suite for job.Store and
// job.FreezeStore implementations. Every store adapter runs it:
//
//	func TestJobConformance(t *testing.T) {
//		t.Parallel()
//		jobtest.Run(t, func(t *testing.T) jobtest.Harness { return newHarness(t) })
//	}
//
// The cases state the rules of the ports. A reference adapter in memory, in
// the tests of this package, runs them, and so does each SQL adapter. The
// cases of job.Claimer are here too: each adapter builds the claimer over
// its own job store, durable store and namespaces. The cases of job.Recorder
// and job.ArtifactStore come with their first implementation, because they
// need the blob store and the item keys.
package jobtest

import (
	"context"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case. The stores share the
// database, as the store adapters of one installation do.
type Harness struct {
	Jobs    job.Store
	Durable durable.Store
	Freezes job.FreezeStore
	// Claimer is the job.Claimer over Jobs, Durable and the namespaces of
	// the database.
	Claimer job.Claimer
	// Namespaces reads the namespaces of the database, as the claimer does.
	// The cases that put a hook between the reads and the writes of a claimer
	// build their own claimer from it.
	Namespaces job.NamespaceReader
	Domain     Domain
	// WriteState writes the state of a unit through the real store of its
	// tool, as a state write of that tool does, and returns the state version
	// that the write made current. A final write releases the lock of the
	// write, as an apply does; a write of Pulumi ends a mutating update. A
	// harness with no real store of a tool leaves it nil, and the suite runs
	// no case that needs a state version.
	WriteState StateWriter
}

// StateWriter writes the state of a unit through the store of its tool. The
// suite cannot choose the text of the version: each write makes a new version
// and returns it.
type StateWriter func(
	ctx context.Context, fence namespace.Fence, unit identity.Object, final bool,
) (string, error)

// Domain makes the rows that a job needs and that other ports own, and
// reads the audit entries that the job store appends.
//
//nolint:interfacebloat // One fixture contract: the rows that the cases of the suite need from the other ports.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// CreateUnit creates a unit of tool in the namespace of fence and
	// returns its reference, with its stable unit ID set.
	CreateUnit(ctx context.Context, fence namespace.Fence, tool, name string) (identity.Object, error)
	// RenameUnit renames the unit of fence to newName, as the rename of its
	// tool does: the stable unit ID and the locks of the unit stay. It
	// returns the unit under its new name. The suite renames a Pulumi unit
	// only.
	RenameUnit(
		ctx context.Context,
		fence namespace.Fence,
		unit identity.Object,
		newName string,
	) (identity.Object, error)
	// RemoveUnit removes the unit from the inventory, as the delete of its
	// tool does. The suite removes a Pulumi unit only.
	RemoveUnit(ctx context.Context, fence namespace.Fence, unit identity.Object) error
	// BumpFence increments the failover version of the namespace of fence,
	// as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// PutManualLock puts a manual lock on target, as manuallock.Store does:
	// a lock on a unit covers that unit, a lock on a namespace covers each unit
	// of it. The store of jobs reads the lock in the transaction of a
	// dispatch. The suite locks a unit and a namespace only: the coverage by a
	// collection is the one of manuallock.Store.CoveringLock, which the suite
	// of that port proves.
	PutManualLock(ctx context.Context, fence namespace.Fence, target manuallock.Target, holder, reason string) error
	// RemoveManualLock removes the manual lock of target.
	RemoveManualLock(ctx context.Context, fence namespace.Fence, target manuallock.Target) error
	// RecordStepBegin appends r to the step records of the phase of pc, as
	// the recorder of a running phase does when a step begins. It checks the
	// claim like every write of the executor: the fence of pc, the job at
	// the phase and the operation of pc, the live claim in the transaction,
	// and the token of r. It returns job.ErrClaimLost for a claim that is
	// not live and for a phase that the job is not at (the same error for
	// a phase that does not exist), job.ErrInvalid for a record with another
	// token, and namespace.ErrFenced for a stale fence. The suite uses it to
	// show what a takeover does after a begin that an earlier claim
	// recorded.
	RecordStepBegin(ctx context.Context, pc job.PhaseClaim, r job.StepRecord) error
	// DamageJobBody replaces the body of the job id with text that this
	// version cannot decode, as a restore or another version of the server
	// wrote it. The SQL adapters run a direct update of the body column; the
	// suite uses it to put a damaged row in the store.
	DamageJobBody(ctx context.Context, id job.ID) error
	// AuditEntries returns the audit entries of the namespace ns, in the
	// order of their append.
	AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// createRuleCases returns the cases of the rules of a create for the request
// hash and for the first state.
func createRuleCases() []suiteCase {
	return []suiteCase{
		{"a key with another request hash conflicts", testCreateKeyHashConflict},
		{"a job that waits for approval is stored awaiting-approval", testCreateAwaitingApproval},
		{"a job in another first state is refused", testCreateBadState},
	}
}

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := slices.Concat([]suiteCase{
		{"create stores a queued job and get returns it", testCreateGet},
		{"create keeps the trigger reference of a job, and a job with none reads back with none", testCreateTrigger},
		{"create keeps the list of defaulted values, and a job with none reads back with none", testCreateDefaulted},
		{"get of an unknown ID is ErrNotFound", testGetUnknown},
		{"two creates with one key and one spec give one job", testCreateKeyOnce},
		{"a key with another spec conflicts", testCreateKeyConflict},
		{"create with a dispatch starts the durable operation", testCreateDispatch},
		{"create with a dispatch obeys the one-change rule", testCreateUnitBusy},
		{"after ErrUnitBusy, a create with no dispatch waits with unit-busy", testCreateWaits},
		{"create with a dispatch obeys a freeze", testCreateFrozen},
		{"create with a dispatch obeys a manual lock, and the job waits locked", testCreateLocked},
		{"create above the open jobs of a unit is ErrLimit", testCreateLimit},
		{"a write on an older version is ErrConflict", testApplyConflict},
		{"an event that the state does not allow changes nothing", testApplyTransition},
		{"a second change dispatch on one unit is ErrUnitBusy", testUnitBusy},
		{"a failed change rolls back its audit entries", testAuditRollback},
		{"the unit is free after its phase ends", testUnitFrees},
		{"a plan dispatch does not hold the unit", testPlanDoesNotHold},
		{"a change dispatch in a freeze is ErrFrozen", testFrozen},
		{"break-glass passes a freeze", testBreakGlass},
		{"a dispatch after the freeze ends", testFreezeEnds},
		{"a dispatch of a unit with a manual lock is ErrLocked, plan phases too", testLocked},
		{"a manual lock of the namespace stops the dispatch of each unit in it", testNamespaceLocked},
		{"break-glass does not pass a manual lock", testBreakGlassNotPastLock},
		{"a queued locked job keeps its queue timeout and dispatches after the unlock", testLockedQueue},
		{"a phase that runs when the lock arrives runs to its end", testLockRunsOn},
		{"a refused dispatch of a locked unit writes no audit entry", testLockedAuditRollback},
		{"a cancel of a dispatched job cancels its operation", testCancelDispatched},
		{"a cancel of a running job requests the cancel of its operation", testCancelRunning},
		{"a takeover is a new claim on the same operation", testTakeover},
		{"a failure during a cancel ends failed", testFailDuringCancel},
		{"a stale fence changes nothing", testStaleFence},
		{"a cancel in awaiting-state ends canceled and abandons", testCancelAwaitingState},
		{"after a restore, orphans are the jobs whose operation ended", testOrphans},
		{"a canceling job whose claim expires ends canceled at the reconcile", testCancelClaimExpires},
		{"a running job whose operation fails ends failed at the reconcile", testRunningOperationFails},
		{"the confirmation gate and a confirmation are stored", testConfirmationStored},
		{"list filters by unit and state, newest first", testList},
		{"a fence of another namespace reaches no job", testForeignNamespace},
		{"parallel creates with one key give one job", testParallelCreatesOneKey},
		{"two claimers of one phase: one wins", testTwoClaimers},
		{"parallel dispatches on one unit: one holds it", testParallelUnitBusy},
		{"a dispatch and a manual lock in parallel give one serial order", testDispatchAndLockInParallel},
		{"a dispatch of a renamed unit still obeys the lock of its namespace", testRenamedUnitLocked},
		{"a dispatch of a renamed unit still obeys the lock of the unit", testRenamedUnitOwnLock},
		{"a dispatch of a unit that left the inventory is refused", testUnitGone},
		{"parallel creates in several namespaces, then paging returns every job", testParallelNamespaces},
		{"parallel creates in one namespace, then paging returns every job", testParallelCreatesOneNamespace},
		{"freezes list, end once, and validate", testFreezes},
	}, createRuleCases(), reconcileCases(), damagedCases(), claimerCases(), missingNamespaceCases(), endClaimCases(),
		claimBoundCases(), claimGateCases())

	runCases(t, newHarness, cases)

	// The in-memory job store fakes no state version, so it also implements
	// no reader of one. A harness whose store reads final versions runs the
	// cases of that port.
	if _, ok := newHarness(t).Jobs.(job.FinalVersionReader); ok {
		runCases(t, newHarness, finalVersionCases())
	}

	// A state version comes from the store of a tool. A harness without one
	// runs no case that reads a version.
	if newHarness(t).WriteState == nil {
		return
	}

	runCases(t, newHarness, stateVersionCases())
}

// stateVersionCases returns the cases that need a state version of a unit.
func stateVersionCases() []suiteCase {
	return []suiteCase{
		{"a stale fence on a state version closes nothing", testObserveStaleFence},
		{"only a final state version closes the delivered jobs of its unit", testFinalStateVersionCloses},
		{"a review request closes after its merge, and then verifies", testMergeAndVerify},
		{"a lost webhook: a merge that a later poll sees closes on the earlier state version", testLostWebhook},
		{"a merge after the last final state version waits", testMergeBeforeOldVersion},
		{"a late trailer: a commit that a takeover finds closes on the earlier state version", testLateTrailer},
		{"the claim of a plan phase records the state version, and a takeover again", testPlanClaimRecordsVersion},
		{"an apply dispatch with a stale plan is ErrPlanStale, and replan goes back", testPlanStale},
		{"claimer: the claim of a plan phase records the state version", testClaimerPlanVersion},
		{"the reconcile sends a queued job with a stale plan back to its plan phase", testReconcileStalePlan},
		{"the reason of a replan names the state version of the plan and the current one", testReconcileReplanReason},
		{"a damaged row does not stop a state version from closing the good jobs", testDamagedClose},
		{"final version after a final write", testFinalVersionAfterFinal},
		{"an intermediate version is not recorded", testFinalVersionIntermediate},
		{"a newer final version replaces the older", testFinalVersionReplaces},
		{
			"the final version of a write with no token has no origin and the time of the write",
			testFinalVersionRoundTrip,
		},
		{"the answer is per namespace", testFinalVersionPerNamespace},
	}
}

// runCases runs each case against a fresh harness.
func runCases(t *testing.T, newHarness Factory, cases []suiteCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// suiteCase is one named case of the suite.
type suiteCase struct {
	name string
	run  func(t *testing.T, h Harness)
}

// mainRef is the ref of the code location that the cases share.
const mainRef = "main"

// fixture is a namespace with one OpenTofu unit.
type fixture struct {
	fence namespace.Fence
	unit  identity.Object
}

// setUp creates the namespace acme with the unit network.
func setUp(t *testing.T, h Harness) fixture {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	unit, err := h.Domain.CreateUnit(t.Context(), fence, "opentofu", "network")
	if err != nil {
		t.Fatalf("CreateUnit: %v", err)
	}

	return fixture{fence: fence, unit: unit}
}

// newJob returns a queued runner job of op on unit with the key key.
func newJob(unit identity.Object, op job.Operation, key string) *job.Job {
	phases, err := job.DefaultPhases(job.ExecutorRunner, op)
	if err != nil {
		panic(err)
	}

	j := job.Job{
		ID:             job.ID(ident.NewID(job.IDPrefix)),
		Spec:           spec(unit, op),
		State:          job.StateQueued,
		Wait:           job.WaitNone,
		Reason:         "",
		Phases:         make([]job.Phase, len(phases)),
		Current:        0,
		StartedBy:      "al",
		Source:         job.SourceAPI,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: key,
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	return &j
}

// spec returns the spec of a runner job of op on unit.
func spec(unit identity.Object, op job.Operation) job.Spec {
	return job.Spec{
		Unit:      unit,
		Operation: op,
		Purpose:   "",
		Executor:  job.ExecutorRunner,
		Location: job.Location{
			Connection: "github", Repository: "acme/infra", Ref: mainRef, Directory: "network", Revision: "abc123",
		},
		Steps: []job.Step{{
			Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
		}},
		Pool:      "rpl-default",
		Profile:   "prf-default",
		Edits:     nil,
		Delivery:  "",
		Defaulted: nil,
	}
}

// entry returns a valid audit entry for action on unit.
func entry(unit identity.Object, action identity.Action) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "", TokenID: "",
		Action: string(action), Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// dispatchOf returns the durable start request of the current phase of j.
func dispatchOf(j *job.Job) *durable.StartRequest {
	kind := job.RunnerKind(j.Spec.Pool)
	if j.Spec.Executor == job.ExecutorWriteBack {
		kind = job.KindWriteBack
	}

	return &durable.StartRequest{Key: job.PhaseKey(j.ID, j.Current), Kind: kind, Version: 1, Input: nil}
}

// create stores j and fails t on an error.
func create(t *testing.T, h Harness, f fixture, j *job.Job, d *durable.StartRequest) job.Job {
	t.Helper()

	stored, created, err := h.Jobs.Create(t.Context(), *j, d, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if err != nil || !created {
		t.Fatalf("Create = created %v, error %v; want a new job", created, err)
	}

	return stored
}

// apply makes the change and fails t on an error.
func apply(t *testing.T, h Harness, f fixture, c job.Change) job.Job {
	t.Helper()

	got, err := h.Jobs.Apply(t.Context(), c, f.fence, []audit.Entry{entry(f.unit, job.ActionJobRead)})
	if err != nil {
		t.Fatalf("Apply(%s) = %v", c.Event, err)
	}

	return got
}

// writeState writes the state of the unit of f through the real store of its
// tool, and returns the version that the write made current.
func writeState(t *testing.T, h Harness, f fixture, final bool) string {
	t.Helper()

	return writeStateOf(t, h, f.fence, f.unit, final)
}

// writeStateOf writes the state of unit through the real store of its tool,
// and returns the version that the write made current.
func writeStateOf(t *testing.T, h Harness, fence namespace.Fence, unit identity.Object, final bool) string {
	t.Helper()

	if h.WriteState == nil {
		t.Fatal("the harness gives no state writer")
	}

	v, err := h.WriteState(t.Context(), fence, unit, final)
	if err != nil {
		t.Fatalf("WriteState(final %t) of %s = %v", final, unit, err)
	}

	return v
}

// finalVersionOf returns the last final state version of the unit, and fails t
// when the store records none.
func finalVersionOf(t *testing.T, h Harness, ns namespace.Name, unitID string) job.FinalVersion {
	t.Helper()

	r, ok := h.Jobs.(job.FinalVersionReader)
	if !ok {
		t.Fatal("the store does not implement job.FinalVersionReader")
	}

	v, found, err := r.FinalVersion(t.Context(), ns, unitID)
	if err != nil || !found {
		t.Fatalf("FinalVersion of unit %s = %+v, %v, found %t; want a version", unitID, v, err, found)
	}

	return v
}

// change returns the change of j by ev.
func change(j *job.Job, ev job.Event) job.Change {
	return job.Change{
		Job: j.ID, Version: j.Version, Event: ev, Wait: job.WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}
}

// dispatch returns the dispatch change of the current phase of j.
func dispatch(j *job.Job) job.Change {
	c := change(j, job.EventDispatch)
	c.Dispatch = dispatchOf(j)

	return c
}

// claimOp claims the operation of kind k in the durable store, as an
// executor does before it applies EventClaim.
func claimOp(t *testing.T, h Harness, owner string, k durable.Kind, lease time.Duration) durable.Operation {
	t.Helper()

	op, err := h.Durable.Claim(t.Context(), durable.ClaimRequest{
		Owner: owner, Kinds: []durable.Kind{k}, MaxVersions: nil, Lease: lease,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("durable Claim = %v", err)
	}

	return op
}

// shortenOp moves the expiry of the claim c of a durable operation to
// shortLease from now with Extend, after the last write that needs a live
// claim, and fails at once when the expiry does not move earlier. A case
// claims with a long lease, so a slow store cannot end the claim before the
// write: only the wait for the end depends on time.
func shortenOp(t *testing.T, h Harness, c durable.Claim) {
	t.Helper()

	shorter, err := h.Durable.Extend(t.Context(), c, shortLease)
	if err != nil {
		t.Fatalf("durable Extend to a short lease = %v", err)
	}

	if !shorter.ExpiresAt.Before(c.ExpiresAt) {
		t.Fatalf("durable Extend to %v did not move the expiry earlier: %v after %v", shortLease, shorter.ExpiresAt,
			c.ExpiresAt)
	}
}

// claimed applies EventClaim for owner to j.
func claimed(t *testing.T, h Harness, f fixture, j *job.Job, owner string) job.Job {
	t.Helper()

	c := change(j, job.EventClaim)
	c.Executor = owner

	return apply(t, h, f, c)
}

// assertState fails t when the job id is not in state want.
func assertState(t *testing.T, h Harness, id job.ID, want job.State) {
	t.Helper()

	got, err := h.Jobs.Get(t.Context(), id)
	if err != nil || got.State != want {
		t.Fatalf("job %s = %s, %v; want %s", id, got.State, err, want)
	}
}

// ids returns the IDs of jobs, in order.
func ids(jobs []job.Job) []job.ID {
	out := make([]job.ID, 0, len(jobs))
	for i := range jobs {
		out = append(out, jobs[i].ID)
	}

	return out
}

// newFreeze returns an active freeze of the namespace of f, open ended.
func newFreeze(f fixture) job.Freeze {
	return job.Freeze{
		ID: ident.NewID("frz-"), Namespace: f.fence.Namespace, Reason: "release week",
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
	}
}

// freeze creates an active freeze of the namespace of f and returns it.
func freeze(t *testing.T, h Harness, f fixture) job.Freeze {
	t.Helper()

	fr := newFreeze(f)

	err := h.Freezes.CreateFreeze(t.Context(), fr, f.fence, entry(f.unit, job.ActionNamespaceManageFreezes))
	if err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	return fr
}
