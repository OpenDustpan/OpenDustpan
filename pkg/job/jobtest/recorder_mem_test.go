package jobtest_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
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

	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Errorf("close blob store: %v", err)
		}
	})

	return buildRecorderHarness(t, db, newMemKeyIndex(db), blobs)
}

// buildRecorderHarness wires the recorder and the reader over the stores.
func buildRecorderHarness(t *testing.T, db *memDB, keys *memKeyIndex, blobs blob.Store) jobtest.RecorderHarness {
	t.Helper()

	db.itemKeys = keys

	return jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
		Harness: jobtest.Harness{
			Jobs:       db,
			Durable:    db.durable,
			Freezes:    db,
			Claimer:    job.NewClaimer(db, db.durable, db, jobtest.NoGates{}),
			Namespaces: db,
			Domain:     db,
		},
		Records:  db,
		ItemKeys: keys,
		Blobs:    blobs,
		Wrapper:  nil,
	})
}

// memKeyIndex is the item key index in memory. It checks the fence against the
// namespaces of a memDB.
type memKeyIndex struct {
	db   *memDB
	mu   sync.Mutex
	keys map[secret.ItemRef]secret.ItemKey
}

var (
	_ secret.ItemKeyStore = (*memKeyIndex)(nil)
	_ secret.ItemKeyTx    = (*memKeyTx)(nil)
)

// newMemKeyIndex returns an empty item key index that checks the fences of db.
func newMemKeyIndex(db *memDB) *memKeyIndex {
	return &memKeyIndex{db: db, mu: sync.Mutex{}, keys: map[secret.ItemRef]secret.ItemKey{}}
}

// memKeyTx is the item key part of one transaction of a memDB. It stages the
// records it creates, and the index applies them when the transaction commits.
// The caller holds the lock of the memDB for the whole transaction.
type memKeyTx struct {
	idx    *memKeyIndex
	fence  namespace.Fence
	staged map[secret.ItemRef]secret.ItemKey
}

// CreateItemKey implements secret.ItemKeyTx.
func (t *memKeyTx) CreateItemKey(ctx context.Context, k secret.ItemKey) (secret.ItemKey, error) {
	if k.Ref.Scope.Namespace != t.fence.Namespace {
		return secret.ItemKey{}, namespace.ErrFenced
	}

	if have, err := t.GetItemKey(ctx, k.Ref); err == nil || errors.Is(err, secret.ErrShredded) {
		return have, err
	}

	t.staged[k.Ref] = k

	return k, nil
}

// GetItemKey implements secret.ItemKeyTx.
func (t *memKeyTx) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	if k, ok := t.staged[ref]; ok {
		return k, nil
	}

	return t.idx.GetItemKey(ctx, ref)
}

// GetNamespace implements secret.ItemKeyTx. The memDB is locked already.
func (t *memKeyTx) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return t.idx.db.namespaceLocked(name)
}

// commit stores the staged records.
func (t *memKeyTx) commit() {
	t.idx.mu.Lock()
	defer t.idx.mu.Unlock()

	maps.Copy(t.idx.keys, t.staged)
}

// InItemKeyTx implements secret.ItemKeyStore.
func (m *memKeyIndex) InItemKeyTx(_ context.Context, fence namespace.Fence, fn func(secret.ItemKeyTx) error) error {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	if err := m.db.checkFence(fence); err != nil {
		return err
	}

	tx := m.begin(fence)
	if err := fn(tx); err != nil {
		return err
	}

	tx.commit()

	return nil
}

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

// begin starts a transaction under fence. The caller holds the lock of the
// memDB.
func (m *memKeyIndex) begin(fence namespace.Fence) *memKeyTx {
	return &memKeyTx{idx: m, fence: fence, staged: map[secret.ItemRef]secret.ItemKey{}}
}

// memRecords holds the rows that the recorder adds to the jobs in a memDB: the
// rows of log chunks, and the lookup of plans and log versions.
type memRecords struct {
	chunks map[string]map[int]job.ChunkRow
	plans  map[string]memPlanEntry
	logs   map[string]memLogEntry
}

// memPlanEntry is the lookup row of a stored plan. phase, operation and token
// name the reserve of a plan with no part yet: only the claim that made the
// reserve fills it.
type memPlanEntry struct {
	job       job.ID
	plan      job.PlanRef
	phase     int
	operation durable.ID
	token     int64
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

// memKey returns the key of the record of the step called name under the claim
// pc.
func memKey(pc *job.PhaseClaim, name string) job.StepKey {
	return job.StepKey{Phase: pc.Phase, Token: pc.Claim.Token, Name: name}
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

	changed, err := job.BeginStepRecord(&next, memKey(&pc, name), now)
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

	changed, err := job.EndStepRecord(&next, memKey(&pc, name), exit, closing, now)
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
	ctx context.Context, pc job.PhaseClaim, step string, c job.ChunkReserve, newLog *job.NewLog,
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

	var template *job.LogRef
	if newLog != nil {
		template = &newLog.Log
	}

	plan, err := job.PlanChunk(&next, memKey(&pc, step), c, template, lookup)
	if err != nil {
		return job.ChunkPlan{}, fmt.Errorf("reserve log chunk: %w", err)
	}

	now := time.Now().UTC()

	if plan.CreateLog {
		if newLog == nil {
			return job.ChunkPlan{}, fmt.Errorf("reserve log chunk: %w: no new log for a first chunk", job.ErrInvalid)
		}

		log := newLog.Log

		key, keyErr := m.newKey(
			ctx,
			pc.Fence,
			&memNewItem{mk: newLog.NewKey, ref: newLog.Ref, kind: secret.ItemKindJobLog, id: log.ID},
		)
		if keyErr != nil {
			return job.ChunkPlan{}, fmt.Errorf("reserve log chunk: %w", keyErr)
		}

		log.ItemKey = key.ID
		job.CreateLogRecord(&next, memKey(&pc, step), &log)
		m.records.logs[plan.Version] = memLogEntry{job: next.ID, phase: pc.Phase, step: step, token: pc.Claim.Token}
		m.records.chunks[plan.Version] = map[int]job.ChunkRow{}

		m.store(&next, now)
	}

	if plan.Insert {
		rows := m.records.chunks[plan.Version]
		if rows == nil {
			rows = map[int]job.ChunkRow{}
			m.records.chunks[plan.Version] = rows
		}

		rows[c.Seq] = job.ChunkRow{SHA256: c.SHA256, Size: c.Size, Confirmed: false, BlobKey: ""}
	}

	return plan, nil
}

// chunkKeys returns the blob key of each confirmed chunk of the log version, by
// number. The caller holds m.mu.
func (m *memDB) chunkKeys(version string) []string {
	rows := m.records.chunks[version]
	keys := make([]string, 0, len(rows))

	for seq := 0; ; seq++ {
		row, ok := rows[seq]
		if !ok || !row.Confirmed {
			return keys
		}

		keys = append(keys, row.BlobKey)
	}
}

// ConfirmLogChunk implements job.RecordStore. The first confirm of a chunk
// wins, as in the SQL adapters.
func (m *memDB) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, c job.ChunkConfirm) (bool, error) {
	if c.Stored.Size <= 0 {
		return false, fmt.Errorf("%w: chunk %d of log %s has no stored size", job.ErrInvalid, c.Seq, c.Version)
	}

	if _, err := job.ChunkBlobKey(pc.Job, c.Version, c.Seq, c.BlobKey); err != nil {
		return false, fmt.Errorf("confirm log chunk: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.guard(ctx, pc)
	if err != nil {
		return false, err
	}

	row, ok := m.records.chunks[c.Version][c.Seq]
	if !ok {
		return false, fmt.Errorf("%w: chunk %d of log %s was not reserved", job.ErrInvalid, c.Seq, c.Version)
	}

	changed, err := job.ConfirmChunkRecord(&next, memKey(&pc, c.Step), c.Version, c.Seq, row)
	if err != nil {
		return false, fmt.Errorf("confirm log chunk: %w", err)
	}

	// A repeat of a confirm that the row holds with the same key answers true
	// at any age.
	if row.Confirmed && row.BlobKey == c.BlobKey {
		return true, nil
	}

	if err := blob.CheckAttempt(c.AttemptStartedAt, time.Now()); err != nil {
		return false, fmt.Errorf("confirm log chunk: %w", err)
	}

	if row.Confirmed {
		return false, nil
	}

	row.Confirmed = true
	row.BlobKey = c.BlobKey
	m.records.chunks[c.Version][c.Seq] = row

	if changed {
		m.store(&next, time.Now().UTC())
	}

	return true, nil
}

// gatePlan applies the rules of a plan write. For a stale plan it fails the
// phase and returns the stale error in stale. The caller holds m.mu.
func (m *memDB) gatePlan(ctx context.Context, pc job.PhaseClaim) (memGate, error) {
	next, err := m.guard(ctx, pc)
	if err != nil {
		return memGate{}, err
	}

	checkErr := job.CheckPutPlan(&next, pc.Phase, m.states[next.Spec.Unit.UnitID()])

	switch {
	case checkErr == nil:
		return memGate{next: next, stale: nil}, nil
	case !errors.Is(checkErr, job.ErrPlanStale):
		return memGate{}, checkErr
	}

	if failErr := m.failStalePhase(ctx, pc, checkErr); failErr != nil {
		return memGate{}, failErr
	}

	return memGate{next: next, stale: checkErr}, nil
}

// memGate is the result of gatePlan: the job, and the stale error.
type memGate struct {
	next  job.Job
	stale error
}

// memNewItem names a new plan or log version and its item key function.
type memNewItem struct {
	mk   job.NewItemKeyFunc
	ref  secret.ItemRef
	kind secret.ItemKind
	id   string
}

// newKey makes the item key of a new item with mk in a transaction of the key
// index, and commits the transaction when mk succeeds. The caller holds m.mu.
func (m *memDB) newKey(ctx context.Context, fence namespace.Fence, n *memNewItem) (secret.ItemKey, error) {
	mk, ref := n.mk, n.ref
	if mk == nil || ref.Kind != n.kind || ref.Item != n.id || ref.Scope.Namespace != fence.Namespace {
		return secret.ItemKey{}, fmt.Errorf("%w: the item key function or reference does not fit the new item",
			job.ErrInvalid)
	}

	keyTx := m.itemKeys.begin(fence)

	key, err := mk(ctx, keyTx, ref, fence)
	if err != nil {
		return secret.ItemKey{}, err
	}

	keyTx.commit()

	return key, nil
}

// ReservePlan implements job.RecordStore.
func (m *memDB) ReservePlan(ctx context.Context, pc job.PhaseClaim, np *job.NewPlan) (job.PlanRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, err := m.gatePlan(ctx, pc)
	if err != nil {
		return job.PlanRef{}, err
	}

	if g.stale != nil {
		return job.PlanRef{}, fmt.Errorf("reserve plan: %w", g.stale)
	}

	next := g.next

	// A repeat of a reserve returns the plan of the first one, with no second
	// item key.
	if e, ok := m.records.plans[np.Plan.ID]; ok {
		if e.job != next.ID || len(e.plan.Parts) > 0 {
			return job.PlanRef{}, fmt.Errorf("%w: plan %s is not reservable again", job.ErrConflict, np.Plan.ID)
		}

		return e.plan, nil
	}

	key, err := m.newKey(
		ctx,
		pc.Fence,
		&memNewItem{mk: np.NewKey, ref: np.Ref, kind: secret.ItemKindPlan, id: np.Plan.ID},
	)
	if err != nil {
		return job.PlanRef{}, fmt.Errorf("reserve plan: %w", err)
	}

	p := np.Plan
	p.ItemKey, p.Parts, p.StateVersion = key.ID, nil, next.Phases[pc.Phase].StateVersion
	m.records.plans[p.ID] = memPlanEntry{
		job: next.ID, plan: p, phase: pc.Phase, operation: pc.Claim.Operation, token: pc.Claim.Token,
	}

	return p, nil
}

// PutPlan implements job.RecordStore.
func (m *memDB) PutPlan(ctx context.Context, pc job.PhaseClaim, p job.PlanRef) (job.PlanRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, err := m.gatePlan(ctx, pc)
	if err != nil {
		return job.PlanRef{}, err
	}

	if g.stale != nil {
		return job.PlanRef{}, fmt.Errorf("put plan: %w", g.stale)
	}

	next := g.next

	e, ok := m.records.plans[p.ID]

	switch {
	case !ok || e.job != next.ID:
		return job.PlanRef{}, fmt.Errorf("%w: plan %s was not reserved", job.ErrNotFound, p.ID)
	case len(e.plan.Parts) > 0:
		return job.PlanRef{}, fmt.Errorf("%w: plan %s has parts", job.ErrConflict, p.ID)
	case e.plan.ItemKey != p.ItemKey:
		return job.PlanRef{}, fmt.Errorf("%w: plan %s has another item key", job.ErrInvalid, p.ID)
	case e.phase != pc.Phase || e.operation != pc.Claim.Operation || e.token != pc.Claim.Token:
		return job.PlanRef{}, fmt.Errorf("%w: plan %s was reserved by another phase or claim", job.ErrConflict, p.ID)
	}

	if partsErr := job.CheckPlanParts(p.Parts); partsErr != nil {
		return job.PlanRef{}, partsErr
	}

	plan := e.plan
	plan.Parts, plan.StateVersion = p.Parts, next.Phases[pc.Phase].StateVersion
	next.Phases[pc.Phase].Plan = &plan

	m.records.plans[plan.ID] = memPlanEntry{
		job: next.ID, plan: plan, phase: pc.Phase, operation: pc.Claim.Operation, token: pc.Claim.Token,
	}
	m.store(&next, time.Now().UTC())

	return plan, nil
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

	if nsErr := checkNamespace(pc.Fence.Namespace, pc.Fence, []audit.Entry{e}); nsErr != nil {
		return nsErr
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
	if !ok || len(e.plan.Parts) == 0 {
		return job.ArtifactRecord{}, job.ErrNotFound
	}

	j, ok := m.lookup(e.job)
	if !ok || j.Spec.Unit.Namespace() != ns {
		return job.ArtifactRecord{}, job.ErrNotFound
	}

	p := e.plan

	return job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: &p, Log: nil, ChunkKeys: nil}, nil
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

			return job.ArtifactRecord{
				Job: j.ID, Unit: j.Spec.Unit, Step: e.step, Plan: nil, Log: &l, ChunkKeys: m.chunkKeys(version),
			}, nil
		}
	}

	return job.ArtifactRecord{}, job.ErrNotFound
}

// ArtifactNames implements job.ArtifactKeyStore.
func (m *memDB) ArtifactNames(_ context.Context, ns namespace.Name, id job.ID) (job.ArtifactNames, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.lookup(id)
	if !ok || j.Spec.Unit.Namespace() != ns {
		return job.ArtifactNames{}, job.ErrNotFound
	}

	out := job.ArtifactNames{Plans: []string{}, Parts: map[string][]job.OutputKind{}, ChunkKeys: []string{}}

	for planID := range m.records.plans {
		if m.records.plans[planID].job != id {
			continue
		}

		out.Plans = append(out.Plans, planID)

		for _, part := range m.records.plans[planID].plan.Parts {
			out.Parts[planID] = append(out.Parts[planID], part.Kind)
		}
	}

	for version, e := range m.records.logs {
		if e.job != id {
			continue
		}

		for seq := range m.records.chunks[version] {
			if c := m.records.chunks[version][seq]; c.Confirmed {
				out.ChunkKeys = append(out.ChunkKeys, c.BlobKey)
			}
		}
	}

	slices.Sort(out.Plans)
	slices.Sort(out.ChunkKeys)

	return out, nil
}

func TestMemArtifactSweep(t *testing.T) {
	t.Parallel()

	jobtest.RunArtifactSweep(t, func(t *testing.T) jobtest.ArtifactSweepHarness {
		t.Helper()

		db := newMemDB()

		blobs, err := blobfs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open blob store: %v", err)
		}

		t.Cleanup(func() {
			if err := blobs.Close(); err != nil {
				t.Errorf("close blob store: %v", err)
			}
		})

		return jobtest.ArtifactSweepHarness{
			RecorderHarness: buildRecorderHarness(t, db, newMemKeyIndex(db), blobs), Names: db,
			Clock: machineClock{}, Audit: &memServerAudit{mu: sync.Mutex{}, entries: nil},
		}
	})
}

// machineClock is the clock of the database of the memory store: the clock of
// the machine.
type machineClock struct{}

func (machineClock) DatabaseNow(context.Context) (time.Time, error) { return time.Now(), nil }

// memServerAudit keeps the audit entries of the scope of the server.
type memServerAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *memServerAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("append server entry: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}
