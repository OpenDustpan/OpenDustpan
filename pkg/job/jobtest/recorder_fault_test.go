package jobtest_test

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/secret"
)

// errInjected is the failure that the fault tests inject into the blob store.
var errInjected = errors.New("injected blob failure")

// putFault fails the call number call of Put in the blob store.
func putFault(call int) blobfault.Fault {
	return blobfault.Fault{Method: "Put", Call: call, Kind: blobfault.KindFail, Err: errInjected, Delay: 0}
}

// newFaultyHarness is newMemRecorderHarness with faults in the blob store. The
// first Put of a log or a plan stores the item key blob, so the call numbers of
// the faults count that one too.
func newFaultyHarness(t *testing.T, faults ...blobfault.Fault) jobtest.RecorderHarness {
	t.Helper()

	db := newMemDB()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	faulty, err := blobfault.New(inner, faults...)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	keys := &memKeyIndex{db: db, mu: sync.Mutex{}, keys: map[secret.ItemRef]secret.ItemKey{}}

	return buildRecorderHarness(t, db, keys, faulty)
}

// planAssignment stores a plan job on a fresh unit and claims its plan phase.
func planAssignment(t *testing.T, h *jobtest.RecorderHarness) *job.Assignment {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	unit, err := h.Domain.CreateUnit(t.Context(), fence, "opentofu", "network")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	j := newPlanJob(unit)
	start := &durable.StartRequest{
		Key:     job.PhaseKey(j.ID, 0),
		Kind:    job.RunnerKind(j.Spec.Pool),
		Version: 1,
		Input:   nil,
	}

	stored, created, err := h.Jobs.Create(t.Context(), *j, start, fence, nil)
	if err != nil || !created {
		t.Fatalf("Create = %v, %v", created, err)
	}

	req := job.ClaimRequest{
		Owner:       "runner-1",
		Kinds:       []durable.Kind{job.RunnerKind(stored.Spec.Pool)},
		MaxVersions: nil,
		Lease:       time.Minute,
	}

	a, err := h.Claimer.Claim(t.Context(), req)
	if err != nil {
		t.Fatalf("Claim = %v", err)
	}

	return &a
}

// newPlanJob returns a queued plan job on unit with one step called plan.
func newPlanJob(unit identity.Object) *job.Job {
	phases, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		panic(err)
	}

	step := job.Step{
		Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil, Dir: "",
		Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
	}

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)), State: job.StateQueued, Wait: job.WaitNone, Reason: "",
		Current: 0, StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Executor: job.ExecutorRunner,
			Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
			Steps:    []job.Step{step}, Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "",
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	return &j
}

// mustGetJob reads the job id and fails t on an error.
func mustGetJob(t *testing.T, h *jobtest.RecorderHarness, id job.ID) job.Job {
	t.Helper()

	j, err := h.Jobs.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%s) = %v", id, err)
	}

	return j
}

// listJobBlobs returns the keys of the plan and log blobs in the store.
func listJobBlobs(t *testing.T, h *jobtest.RecorderHarness) []string {
	t.Helper()

	n, err := h.Namespaces.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	objects, err := h.Blobs.List(t.Context(), "namespaces/"+n.ID.String()+"/job/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}

	keys := make([]string, 0, len(objects))
	for _, o := range objects {
		keys = append(keys, o.Key.String())
	}

	return keys
}

// planClaim returns the end record of the step plan under the claim of a.
func planClaim(a *job.Assignment) job.StepRecord {
	return job.StepRecord{
		Name: "plan", Token: a.Claim.Claim.Token, ExitCode: 0, StartedAt: time.Time{}, FinishedAt: time.Time{},
		Log: nil,
	}
}

// TestRecorderChunkBlobFailure proves the crash window between the reserve of a
// chunk and its blob: the chunk is reserved, the content of the number is
// fixed, and a retry with the same content stores the blob.
func TestRecorderChunkBlobFailure(t *testing.T) {
	t.Parallel()

	// Put 1 is the item key blob, put 2 is the blob of chunk 0.
	h := newFaultyHarness(t, putFault(2))
	a := planAssignment(t, &h)

	if err := h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); err != nil {
		t.Fatalf("BeginStep = %v", err)
	}

	err := h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n"))
	if !errors.Is(err, errInjected) {
		t.Fatalf("AppendLog with a failing blob store = %v, want the injected failure", err)
	}

	log := mustGetJob(t, &h, a.Claim.Job).Phases[0].Steps[0].Log
	if log == nil || log.Chunks != 0 {
		t.Fatalf("log after the failure = %+v, want a log with no counted chunk", log)
	}

	// The chunk is reserved: another content for the number is a conflict.
	err = h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("other\n"))
	if !errors.Is(err, job.ErrConflict) {
		t.Fatalf("AppendLog of another content for a reserved chunk = %v, want ErrConflict", err)
	}

	// The same content goes in.
	if err = h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n")); err != nil {
		t.Fatalf("retry of the chunk = %v", err)
	}

	if err = h.Recorder.EndStep(t.Context(), a.Claim, planClaim(a)); err != nil {
		t.Fatalf("EndStep = %v", err)
	}

	rc, err := h.Reader.OpenLog(t.Context(), "acme", mustGetJob(t, &h, a.Claim.Job).Phases[0].Steps[0].Log.ID)
	if err != nil {
		t.Fatalf("OpenLog = %v", err)
	}

	data, err := io.ReadAll(rc)
	if closeErr := rc.Close(); err != nil || closeErr != nil || string(data) != "line\n" {
		t.Fatalf("log = %q, %v, %v; want the retried chunk", data, err, closeErr)
	}
}

// TestRecorderPlanPartFailure proves that a plan whose second part failed
// leaves no blob and no plan, and that the phase takes a whole plan on a retry.
func TestRecorderPlanPartFailure(t *testing.T) {
	t.Parallel()

	// Put 1 is the item key blob, put 2 and 3 are the two parts.
	h := newFaultyHarness(t, putFault(3))
	a := planAssignment(t, &h)

	parts := func() []job.PlanUpload {
		return []job.PlanUpload{
			{Kind: job.OutputPlanDisplay, Body: strings.NewReader("display")},
			{Kind: job.OutputPlanApply, Body: strings.NewReader("apply")},
		}
	}

	if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts()); !errors.Is(err, errInjected) {
		t.Fatalf("PutPlan with a failing blob store = %v, want the injected failure", err)
	}

	if got := mustGetJob(t, &h, a.Claim.Job); got.Phases[0].Plan != nil {
		t.Fatalf("plan after the failure = %+v, want none", got.Phases[0].Plan)
	}

	for _, k := range listJobBlobs(t, &h) {
		t.Errorf("blob %s stays after a failed plan", k)
	}

	if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts()); err != nil {
		t.Fatalf("PutPlan retry = %v", err)
	}
}

// TestRecorderPlanStoreFailure proves that a plan whose record the store
// refused leaves no part blob.
func TestRecorderPlanStoreFailure(t *testing.T) {
	t.Parallel()

	h := newFaultyHarness(t)
	a := planAssignment(t, &h)

	// The claim ends before the plan, so the store refuses the plan after the
	// parts are stored.
	if err := h.Claimer.Release(t.Context(), a.Claim); err != nil {
		t.Fatalf("Release = %v", err)
	}

	parts := []job.PlanUpload{{Kind: job.OutputPlanDisplay, Body: strings.NewReader("display")}}

	if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("PutPlan after the release = %v, want ErrClaimLost", err)
	}

	for _, k := range listJobBlobs(t, &h) {
		t.Errorf("blob %s stays after a refused plan", k)
	}
}
