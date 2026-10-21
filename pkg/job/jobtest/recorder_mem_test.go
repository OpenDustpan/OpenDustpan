package jobtest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// TestMemRecorder runs the suite of job.Recorder and job.ArtifactReader
// against the reference adapter in memory. It runs before any SQL adapter.
func TestMemRecorder(t *testing.T) {
	t.Parallel()

	jobtest.RunRecorder(t, func(t *testing.T) jobtest.RecorderHarness {
		t.Helper()

		return newMemRecorderHarness(t)
	})
}

// newMemRecorderHarness builds the recorder over a memDB, a directory of
// blobs and the item keys in memory.
func newMemRecorderHarness(t *testing.T) jobtest.RecorderHarness {
	t.Helper()

	db := newMemDB()

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = blobs.Close() }) //nolint:errcheck // Test cleanup.

	keys := &memKeyIndex{db: db, mu: sync.Mutex{}, keys: map[secret.ItemRef]secret.ItemKey{}}

	return buildRecorderHarness(t, db, keys, blobs)
}

// buildRecorderHarness wires the recorder and the reader over the stores.
func buildRecorderHarness(t *testing.T, db *memDB, keys secret.ItemKeyIndex, blobs blob.Store) jobtest.RecorderHarness {
	t.Helper()

	return jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
		Harness: jobtest.Harness{
			Jobs: db, Durable: db.durable, Freezes: db, Claimer: job.NewClaimer(db, db.durable, db), Namespaces: db,
			Domain: db,
		},
		Records:  db,
		ItemKeys: keys,
		Blobs:    blobs,
	})
}

// memKeyIndex is the item key index in memory. It checks the fence against the
// namespaces of a memDB.
type memKeyIndex struct {
	db   *memDB
	mu   sync.Mutex
	keys map[secret.ItemRef]secret.ItemKey
}

var _ secret.ItemKeyIndex = (*memKeyIndex)(nil)

// CreateItemKey implements secret.ItemKeyIndex.
func (m *memKeyIndex) CreateItemKey(_ context.Context, k secret.ItemKey, f namespace.Fence) (secret.ItemKey, error) {
	if k.Ref.Scope.Namespace != f.Namespace {
		return secret.ItemKey{}, namespace.ErrFenced
	}

	m.db.mu.Lock()
	err := m.db.checkFence(f)
	m.db.mu.Unlock()

	if err != nil {
		return secret.ItemKey{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if have, ok := m.keys[k.Ref]; ok {
		if have.Shredded() {
			return have, secret.ErrShredded
		}

		return have, nil
	}

	m.keys[k.Ref] = k

	return k, nil
}

// GetItemKey implements secret.ItemKeyIndex.
func (m *memKeyIndex) GetItemKey(_ context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]
	if !ok {
		return secret.ItemKey{}, secret.ErrKeyNotFound
	}

	if k.Shredded() {
		return k, secret.ErrShredded
	}

	return k, nil
}

// ShredItemKey implements secret.ItemKeyIndex.
func (m *memKeyIndex) ShredItemKey(
	_ context.Context, ref secret.ItemRef, at time.Time, f namespace.Fence,
) (secret.ItemKey, error) {
	m.db.mu.Lock()
	err := m.db.checkFence(f)
	m.db.mu.Unlock()

	if err != nil {
		return secret.ItemKey{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]
	if !ok {
		return secret.ItemKey{}, secret.ErrKeyNotFound
	}

	if !k.Shredded() {
		k.ShreddedAt = at
		m.keys[ref] = k
	}

	return k, nil
}

// ListItemKeys implements secret.ItemKeyIndex. The recorder suite does not
// list keys.
func (m *memKeyIndex) ListItemKeys(context.Context, secret.Scope, secret.ItemKeyID, int) ([]secret.ItemKey, error) {
	return nil, nil
}

// ListItemKeyWrapperIDs implements secret.ItemKeyIndex.
func (m *memKeyIndex) ListItemKeyWrapperIDs(context.Context) ([]string, error) {
	return []string{}, nil
}

// memRecords holds the rows that the recorder adds to the jobs in a memDB: the
// rows of log chunks, and the lookup of plans and log versions.
type memRecords struct {
	chunks map[string]map[int]job.ChunkRow
	plans  map[string]memPlanEntry
	logs   map[string]memLogEntry
}

// memPlanEntry is the lookup row of a stored plan.
type memPlanEntry struct {
	job  job.ID
	plan job.PlanRef
}

// memLogEntry is the lookup row of a log version.
type memLogEntry struct {
	job   job.ID
	phase int
	step  string
	token int64
}

func newMemRecords() memRecords {
	return memRecords{
		chunks: map[string]map[int]job.ChunkRow{},
		plans:  map[string]memPlanEntry{},
		logs:   map[string]memLogEntry{},
	}
}

var _ job.RecordStore = (*memDB)(nil)

// guard returns a copy of the job of pc after the checks that every write of
// an executor makes, as the SQL adapters make them in the transaction of the
// write. The caller holds m.mu.
func (m *memDB) guard(ctx context.Context, pc job.PhaseClaim) (job.Job, error) {
	if err := m.checkFence(pc.Fence); err != nil {
		return job.Job{}, err
	}

	j, ok := m.lookup(pc.Job)
	if !ok || j.Spec.Unit.Namespace() != pc.Fence.Namespace {
		return job.Job{}, job.ErrNotFound
	}

	runs := j.State == job.StateRunning || j.State == job.StateCanceling
	if !runs || j.Current != pc.Phase || pc.Phase < 0 || pc.Phase >= len(j.Phases) ||
		j.Phases[pc.Phase].Operation != string(pc.Claim.Operation) {
		return job.Job{}, fmt.Errorf("%w: %w: job %s is %s at phase %d", job.ErrClaimLost, durable.ErrClaimLost, j.ID,
			j.State, j.Current)
	}

	if err := m.checkClaim(ctx, pc.Claim); err != nil {
		return job.Job{}, fmt.Errorf("%w: %w", job.ErrClaimLost, err)
	}

	return copyJob(j), nil
}

// store keeps next as the new version of its job.
func (m *memDB) store(next *job.Job, now time.Time) {
	next.Version++
	next.UpdatedAt = now
	m.jobs[next.ID] = next
}

// BeginStep implements job.RecordStore.
func (m *memDB) BeginStep(ctx context.Context, pc job.PhaseClaim, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	changed, err := job.BeginStepRecord(&next, pc.Phase, pc.Claim.Token, name, now)
	if err != nil {
		return fmt.Errorf("begin step: %w", err)
	}

	if changed {
		m.store(&next, now)
	}

	return nil
}

// EndStep implements job.RecordStore.
func (m *memDB) EndStep(ctx context.Context, pc job.PhaseClaim, name string, exit int, closing *job.LogClose) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	changed, err := job.EndStepRecord(&next, pc.Phase, pc.Claim.Token, name, exit, closing, now)
	if err != nil {
		return fmt.Errorf("end step: %w", err)
	}

	if changed {
		m.store(&next, now)
	}

	return nil
}

// ReserveLogChunk implements job.RecordStore.
func (m *memDB) ReserveLogChunk(
	ctx context.Context, pc job.PhaseClaim, step string, c job.ChunkReserve, newLog *job.LogRef,
) (job.ChunkPlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return job.ChunkPlan{}, err
	}

	lookup := func(version string, seq int) (job.ChunkRow, bool, error) {
		row, ok := m.records.chunks[version][seq]

		return row, ok, nil
	}

	plan, err := job.PlanChunk(&next, pc.Phase, pc.Claim.Token, step, c, newLog, lookup)
	if err != nil {
		return job.ChunkPlan{}, fmt.Errorf("reserve log chunk: %w", err)
	}

	now := time.Now().UTC()

	if plan.CreateLog {
		job.CreateLogRecord(&next, pc.Phase, pc.Claim.Token, step, newLog)
		m.records.logs[plan.Version] = memLogEntry{job: next.ID, phase: pc.Phase, step: step, token: pc.Claim.Token}
		m.records.chunks[plan.Version] = map[int]job.ChunkRow{}

		m.store(&next, now)
	}

	if plan.Insert {
		m.records.chunks[plan.Version][c.Seq] = job.ChunkRow{SHA256: c.SHA256, Size: c.Size, Confirmed: false}
	}

	return plan, nil
}

// ConfirmLogChunk implements job.RecordStore.
func (m *memDB) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, step, version string, seq int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return err
	}

	row, ok := m.records.chunks[version][seq]
	if !ok {
		return fmt.Errorf("%w: chunk %d of log %s was not reserved", job.ErrInvalid, seq, version)
	}

	changed, err := job.ConfirmChunkRecord(&next, pc.Phase, pc.Claim.Token, step, version, seq, row)
	if err != nil {
		return fmt.Errorf("confirm log chunk: %w", err)
	}

	row.Confirmed = true
	m.records.chunks[version][seq] = row

	if changed {
		m.store(&next, time.Now().UTC())
	}

	return nil
}

// PutPlan implements job.RecordStore.
func (m *memDB) PutPlan(ctx context.Context, pc job.PhaseClaim, p job.PlanRef) (job.PlanRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return job.PlanRef{}, err
	}

	current := m.states[next.Spec.Unit.UnitID()]

	if checkErr := job.CheckPutPlan(&next, pc.Phase, current); checkErr != nil {
		if errors.Is(checkErr, job.ErrPlanStale) {
			if failErr := m.failStalePhase(ctx, pc, checkErr); failErr != nil {
				return job.PlanRef{}, failErr
			}
		}

		return job.PlanRef{}, fmt.Errorf("put plan: %w", checkErr)
	}

	p.StateVersion = next.Phases[pc.Phase].StateVersion
	next.Phases[pc.Phase].Plan = &p

	m.records.plans[p.ID] = memPlanEntry{job: next.ID, plan: p}
	m.store(&next, time.Now().UTC())

	return p, nil
}

// failStalePhase fails the phase of pc and ends its operation, as the SQL
// adapters do in the transaction of the PutPlan that finds a stale plan.
func (m *memDB) failStalePhase(ctx context.Context, pc job.PhaseClaim, why error) error {
	cur, ok := m.lookup(pc.Job)
	if !ok {
		return job.ErrNotFound
	}

	msg := why.Error()
	c := job.Change{
		Job: cur.ID, Version: cur.Version, Event: job.EventPhaseFailed, Wait: job.WaitNone, Reason: msg,
		Dispatch: nil, PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{},
		Approval: nil,
		End: &job.OperationEnd{
			Claim: pc.Claim, Kind: job.EndFail,
			Failure: durable.Failure{Message: msg, Final: true, RetryAfter: 0},
		},
	}

	next := copyJob(cur)

	if err := m.applyIn(ctx, cur, &next, &c, time.Now().UTC()); err != nil {
		return fmt.Errorf("fail the stale plan phase: %w", err)
	}

	m.jobs[next.ID] = &next

	return nil
}

// Delivered implements job.RecordStore.
func (m *memDB) Delivered(ctx context.Context, pc job.PhaseClaim, d job.DeliveryResult, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return err
	}

	if err = checkNamespace(pc.Fence.Namespace, pc.Fence, []audit.Entry{e}); err != nil {
		return err
	}

	changed, err := job.CheckDelivered(&next, pc.Phase, &d)
	if err != nil {
		return fmt.Errorf("delivered: %w", err)
	}

	if !changed {
		return nil
	}

	next.Delivery = &d
	m.store(&next, time.Now().UTC())
	m.audit[pc.Fence.Namespace] = append(m.audit[pc.Fence.Namespace], e)

	return nil
}

// Plan implements job.RecordStore.
func (m *memDB) Plan(_ context.Context, ns namespace.Name, plan string) (job.ArtifactRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.records.plans[plan]
	if !ok {
		return job.ArtifactRecord{}, job.ErrNotFound
	}

	j, ok := m.lookup(e.job)
	if !ok || j.Spec.Unit.Namespace() != ns {
		return job.ArtifactRecord{}, job.ErrNotFound
	}

	p := e.plan

	return job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: &p, Log: nil}, nil
}

// Log implements job.RecordStore.
func (m *memDB) Log(_ context.Context, ns namespace.Name, version string) (job.ArtifactRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.records.logs[version]
	if !ok {
		return job.ArtifactRecord{}, job.ErrNotFound
	}

	j, ok := m.lookup(e.job)
	if !ok || j.Spec.Unit.Namespace() != ns {
		return job.ArtifactRecord{}, job.ErrNotFound
	}

	for i := range j.Phases[e.phase].Steps {
		r := &j.Phases[e.phase].Steps[i]
		if r.Name == e.step && r.Token == e.token && r.Log != nil && r.Log.ID == version {
			l := *r.Log

			return job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: e.step, Plan: nil, Log: &l}, nil
		}
	}

	return job.ArtifactRecord{}, job.ErrNotFound
}

// errInjected is the failure that the fault tests inject into the blob store.
var errInjected = errors.New("injected blob failure")

// newFaultyHarness is newMemRecorderHarness with faults in the blob store. The
// first Put of a log or a plan stores the item key blob, so the call numbers of
// the faults count that one too.
func newFaultyHarness(t *testing.T, faults ...blobfault.Fault) (jobtest.RecorderHarness, *blobfault.Store) {
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

	return buildRecorderHarness(t, db, keys, faulty), faulty
}

// planAssignment stores a plan job on a fresh unit and claims its plan phase.
func planAssignment(t *testing.T, h jobtest.RecorderHarness) job.Assignment {
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

	stored, created, err := h.Jobs.Create(t.Context(), *j, &durable.StartRequest{
		Key: job.PhaseKey(j.ID, 0), Kind: job.RunnerKind(j.Spec.Pool), Version: 1, Input: nil,
	}, fence, nil)
	if err != nil || !created {
		t.Fatalf("Create = %v, %v", created, err)
	}

	a, err := h.Claimer.Claim(t.Context(), job.ClaimRequest{
		Owner: "runner-1", Kinds: []durable.Kind{job.RunnerKind(stored.Spec.Pool)}, MaxVersions: nil,
		Lease: time.Minute,
	})
	if err != nil {
		t.Fatalf("Claim = %v", err)
	}

	return a
}

// newPlanJob returns a queued plan job on unit with one step called plan.
func newPlanJob(unit identity.Object) *job.Job {
	phases, _ := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)), State: job.StateQueued, Wait: job.WaitNone, StartedBy: "al",
		Source: job.SourceAPI, Verification: job.VerificationNone,
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Executor: job.ExecutorRunner,
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"},
			}},
			Pool: "rpl-default", Profile: "prf-default",
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	return &j
}

// TestRecorderBlobFailures proves the crash windows between the blob and the
// record: a failed write of a blob leaves no record that names it, and a retry
// stores the blob and the record.
func TestRecorderBlobFailures(t *testing.T) {
	t.Parallel()

	t.Run("a chunk whose blob failed is reserved, and its retry stores it", func(t *testing.T) {
		t.Parallel()

		// Put 1 is the item key blob, put 2 is the blob of chunk 0.
		h, _ := newFaultyHarness(t, blobfault.Fault{Method: "Put", Call: 2, Kind: blobfault.KindFail, Err: errInjected})
		a := planAssignment(t, h)

		if err := h.Recorder.BeginStep(t.Context(), a.Claim, "plan"); err != nil {
			t.Fatalf("BeginStep = %v", err)
		}

		err := h.Recorder.AppendLog(t.Context(), a.Claim, "plan", 0, strings.NewReader("line\n"))
		if !errors.Is(err, errInjected) {
			t.Fatalf("AppendLog with a failing blob store = %v, want the injected failure", err)
		}

		got, _ := h.Jobs.Get(t.Context(), a.Claim.Job)
		log := got.Phases[0].Steps[0].Log

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

		if err = h.Recorder.EndStep(t.Context(), a.Claim, job.StepRecord{
			Name: "plan", Token: a.Claim.Claim.Token,
		}); err != nil {
			t.Fatalf("EndStep = %v", err)
		}

		got, _ = h.Jobs.Get(t.Context(), a.Claim.Job)

		rc, err := h.Reader.OpenLog(t.Context(), "acme", got.Phases[0].Steps[0].Log.ID)
		if err != nil {
			t.Fatalf("OpenLog = %v", err)
		}

		data, err := io.ReadAll(rc)
		_ = rc.Close() //nolint:errcheck // Test read.

		if err != nil || string(data) != "line\n" {
			t.Fatalf("log = %q, %v; want the retried chunk", data, err)
		}
	})

	t.Run("a plan whose second part failed leaves no blob and no plan", func(t *testing.T) {
		t.Parallel()

		// Put 1 is the item key blob, put 2 and 3 are the two parts.
		h, store := newFaultyHarness(t, blobfault.Fault{Method: "Put", Call: 3, Kind: blobfault.KindFail, Err: errInjected})
		a := planAssignment(t, h)

		parts := []job.PlanUpload{
			{Kind: job.OutputPlanDisplay, Body: strings.NewReader("display")},
			{Kind: job.OutputPlanApply, Body: strings.NewReader("apply")},
		}

		if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts); !errors.Is(err, errInjected) {
			t.Fatalf("PutPlan with a failing blob store = %v, want the injected failure", err)
		}

		got, _ := h.Jobs.Get(t.Context(), a.Claim.Job)
		if got.Phases[0].Plan != nil {
			t.Fatalf("plan after the failure = %+v, want none", got.Phases[0].Plan)
		}

		for _, o := range listJobBlobs(t, h, store) {
			t.Errorf("blob %s stays after a failed plan", o)
		}

		// The phase is not used up: the whole plan goes in on a retry.
		parts = []job.PlanUpload{
			{Kind: job.OutputPlanDisplay, Body: strings.NewReader("display")},
			{Kind: job.OutputPlanApply, Body: strings.NewReader("apply")},
		}

		if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts); err != nil {
			t.Fatalf("PutPlan retry = %v", err)
		}
	})

	t.Run("a plan whose store write failed leaves no part blob", func(t *testing.T) {
		t.Parallel()

		h, store := newFaultyHarness(t)
		a := planAssignment(t, h)

		// The claim ends before the plan, so the store refuses the plan after the
		// parts are stored.
		if err := h.Claimer.Release(t.Context(), a.Claim); err != nil {
			t.Fatalf("Release = %v", err)
		}

		parts := []job.PlanUpload{{Kind: job.OutputPlanDisplay, Body: strings.NewReader("display")}}

		if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts); !errors.Is(err, job.ErrClaimLost) {
			t.Fatalf("PutPlan after the release = %v, want ErrClaimLost", err)
		}

		for _, o := range listJobBlobs(t, h, store) {
			t.Errorf("blob %s stays after a refused plan", o)
		}
	})
}

// listJobBlobs returns the keys of the plan and log blobs in the store.
func listJobBlobs(t *testing.T, h jobtest.RecorderHarness, _ *blobfault.Store) []string {
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
