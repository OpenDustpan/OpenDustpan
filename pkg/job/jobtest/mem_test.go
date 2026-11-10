package jobtest_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	jobtest.Run(t, func(*testing.T) jobtest.Harness {
		db := newMemDB()

		return jobtest.Harness{
			Jobs:       db,
			Durable:    db.durable,
			Freezes:    db,
			Claimer:    job.NewClaimer(db, db.durable, db, jobtest.NoGates{}),
			Namespaces: db,
			Domain:     db,
			WriteState: nil,
		}
	})
}

// errNotInInventory is the error for a unit that the inventory does not hold.
var errNotInInventory = errors.New("the unit is not in the inventory")

// errUnitBusyIndex is the error of the partial unique index on the unit
// of the jobs that hold it, as a SQL adapter gets it from its driver.
var errUnitBusyIndex = errors.New("unique index on the unit of the jobs that hold it")

// memDB is the reference adapter of job.Store, job.FreezeStore and the
// domain of the suite, in memory. One mutex stands for the transaction of
// each write. It checks every rule before it changes anything, so a write
// that fails changes nothing, its audit entries included. The durable
// operations live in the durable store in memory of package durabletest.
type memDB struct {
	mu      sync.Mutex
	durable durable.Store
	fences  map[namespace.Name]int64
	// nsIDs maps a namespace to its ID, which names its blob scope.
	nsIDs map[namespace.Name]namespace.ID
	// records holds the rows that the recorder adds to the jobs.
	records memRecords
	// itemKeys holds the item keys that the recorder makes in a write.
	itemKeys *memKeyIndex
	jobs     map[job.ID]*job.Job
	order    []job.ID
	keys     map[string]job.ID
	// damaged maps the ID of a job whose stored body does not decode to the
	// kind of the damage, as DamageJobBody marks it.
	damaged map[job.ID]string
	freezes map[string]*job.Freeze
	audit   map[namespace.Name][]audit.Entry
	// locks maps the String of a manual lock target to the lock.
	locks map[string]manuallock.Lock
	// units maps a unit ID to the unit in the inventory, under its current name.
	units map[string]collection.UnitRef
}

func newMemDB() *memDB {
	return &memDB{
		mu:       sync.Mutex{},
		durable:  durabletest.NewMem().Store(),
		fences:   map[namespace.Name]int64{},
		itemKeys: nil,
		nsIDs:    map[namespace.Name]namespace.ID{},
		records:  newMemRecords(),
		jobs:     map[job.ID]*job.Job{},
		order:    nil,
		keys:     map[string]job.ID{},
		damaged:  map[job.ID]string{},
		freezes:  map[string]*job.Freeze{},
		audit:    map[namespace.Name][]audit.Entry{},
		locks:    map[string]manuallock.Lock{},
		units:    map[string]collection.UnitRef{},
	}
}

// CreateNamespace implements jobtest.Domain.
func (m *memDB) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[name] = 1
	m.nsIDs[name] = namespace.NewID()

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

// CreateUnit implements jobtest.Domain.
func (m *memDB) CreateUnit(_ context.Context, fence namespace.Fence, tool, name string) (identity.Object, error) {
	o, err := identity.UnitObject(tool, fence.Namespace, name)
	if err != nil {
		return o, fmt.Errorf("unit object: %w", err)
	}

	o = o.WithUnitID(ident.NewID("unt-"))

	m.mu.Lock()
	defer m.mu.Unlock()

	m.units[o.UnitID()] = collection.UnitRef{Namespace: o.Namespace(), Tool: o.Tool(), Name: o.UnitName()}

	return o, nil
}

// RenameUnit implements jobtest.Domain. The locks of the unit follow it.
func (m *memDB) RenameUnit(
	_ context.Context, fence namespace.Fence, unit identity.Object, newName string,
) (identity.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return identity.Object{}, err
	}

	old, ok := m.units[unit.UnitID()]
	if !ok {
		return identity.Object{}, fmt.Errorf("unit %s: %w", unit, job.ErrNotFound)
	}

	renamed := collection.UnitRef{Namespace: old.Namespace, Tool: old.Tool, Name: newName}

	if l, ok := m.locks[manuallock.UnitTarget(old).String()]; ok {
		delete(m.locks, manuallock.UnitTarget(old).String())

		l.Target = manuallock.UnitTarget(renamed)
		m.locks[l.Target.String()] = l
	}

	m.units[unit.UnitID()] = renamed

	o, err := identity.UnitObject(renamed.Tool, renamed.Namespace, renamed.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return o.WithUnitID(unit.UnitID()), nil
}

// RemoveUnit implements jobtest.Domain.
func (m *memDB) RemoveUnit(_ context.Context, fence namespace.Fence, unit identity.Object) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	delete(m.units, unit.UnitID())

	return nil
}

// PutManualLock implements jobtest.Domain.
func (m *memDB) PutManualLock(
	_ context.Context, fence namespace.Fence, target manuallock.Target, holder, reason string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	m.locks[target.String()] = manuallock.Lock{Since: time.Now().UTC(), Target: target, Holder: holder, Reason: reason}

	return nil
}

// RemoveManualLock implements jobtest.Domain.
func (m *memDB) RemoveManualLock(_ context.Context, fence namespace.Fence, target manuallock.Target) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	delete(m.locks, target.String())

	return nil
}

// BumpFence implements jobtest.Domain.
func (m *memDB) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[fence.Namespace]++

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: m.fences[fence.Namespace]}, nil
}

// GetNamespace implements job.NamespaceReader: it answers the name and the
// failover version, which is all that the claimer reads.
func (m *memDB) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.namespaceLocked(name)
}

// RecordStepBegin implements jobtest.Domain. It checks the claim as the SQL
// adapters do in the transaction of the write.
func (m *memDB) RecordStepBegin(ctx context.Context, pc job.PhaseClaim, r job.StepRecord) error {
	if r.Token != pc.Claim.Token {
		return fmt.Errorf("%w: the record carries token %d, the claim has token %d", job.ErrInvalid, r.Token,
			pc.Claim.Token)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(pc.Fence); err != nil {
		return err
	}

	j, ok := m.lookup(pc.Job)
	if !ok || j.Spec.Unit.Namespace() != pc.Fence.Namespace {
		return job.ErrNotFound
	}

	runs := j.State == job.StateRunning || j.State == job.StateCanceling
	if !runs || j.Current != pc.Phase || pc.Phase < 0 || pc.Phase >= len(j.Phases) ||
		j.Phases[pc.Phase].Operation != string(pc.Claim.Operation) {
		return fmt.Errorf("%w: %w: job %s is %s at phase %d", job.ErrClaimLost, durable.ErrClaimLost, j.ID, j.State,
			j.Current)
	}

	if err := m.checkClaim(ctx, pc.Claim); err != nil {
		return fmt.Errorf("%w: %w", job.ErrClaimLost, err)
	}

	j.Phases[pc.Phase].Steps = append(j.Phases[pc.Phase].Steps, r)
	j.Version++

	return nil
}

// AuditEntries implements jobtest.Domain.
func (m *memDB) AuditEntries(_ context.Context, ns namespace.Name) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.audit[ns]), nil
}

// Create implements job.Store.
//
//nolint:gocritic // hugeParam: the signature of job.Store takes the job by value.
func (m *memDB) Create(ctx context.Context, j job.Job, d *durable.StartRequest, fence namespace.Fence,
	e []audit.Entry,
) (job.Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.admit(&j, d, fence, e); err != nil {
		return job.Job{}, false, err
	}

	ns := j.Spec.Unit.Namespace()
	key := string(ns) + "/" + j.IdempotencyKey

	if first, ok := m.byKey(key, &j); ok {
		if !reflect.DeepEqual(first.Spec, j.Spec) || first.RequestHash != j.RequestHash {
			return job.Job{}, false, job.ErrKeyConflict
		}

		return copyJob(first), false, nil
	}

	if m.open(ns, j.Spec.Unit.UnitID()) >= job.MaxOpenPerUnit || m.open(ns, "") >= job.MaxOpenPerNamespace {
		return job.Job{}, false, job.ErrLimit
	}

	now := time.Now().UTC()
	stored := copyJob(&j)
	stored.State, stored.Version, stored.CreatedAt, stored.UpdatedAt = job.StateQueued, 1, now, now
	stored.StateChangedAt = now

	if j.State == job.StateAwaitingApproval {
		stored.State, stored.Wait = job.StateAwaitingApproval, job.WaitNone
	}

	if d != nil {
		if err := m.checkDispatch(&stored, time.Time{}, now); err != nil {
			return job.Job{}, false, err
		}

		op, err := m.startOp(ctx, d)
		if err != nil {
			return job.Job{}, false, err
		}

		stored.State = job.StateDispatched
		stored.Phases[stored.Current].Operation = op
	}

	m.jobs[stored.ID] = &stored
	m.order = append(m.order, stored.ID)

	if j.IdempotencyKey != "" {
		m.keys[key] = stored.ID
	}

	m.audit[ns] = append(m.audit[ns], e...)

	return copyJob(&stored), true, nil
}

// damagedBodyReason is the kind of the damage that DamageJobBody marks: the
// SQL adapters store a body that is not JSON.
const damagedBodyReason = "the body is not JSON"

// DamageJobBody implements jobtest.Domain. It marks the job id as a row whose
// stored body does not decode, as a restore or another version of the server
// leaves one. The SQL adapters write a body that is not JSON.
func (m *memDB) DamageJobBody(_ context.Context, id job.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.lookup(id); !ok {
		return job.ErrNotFound
	}

	m.damaged[id] = damagedBodyReason

	return nil
}

// Get implements job.Store.
func (m *memDB) Get(_ context.Context, id job.ID) (job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.damaged[id]; ok {
		return job.Job{}, fmt.Errorf("%w: job %s", job.ErrDamaged, id)
	}

	j, ok := m.lookup(id)
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return copyJob(j), nil
}

// List implements job.Store.
func (m *memDB) List(_ context.Context, f job.Filter) (job.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		out  job.Page
		read int
	)

	// After is the last row of the previous page: the page starts behind it.
	passed := f.After == ""

	for _, id := range slices.Backward(m.order) {
		if !passed {
			passed = id == f.After

			continue
		}

		j, ok := m.lookup(id)
		if !ok {
			continue
		}

		if !selects(&f, j) {
			continue
		}

		read++

		if reason, damaged := m.damaged[id]; damaged {
			out.Damaged = append(out.Damaged, job.DamagedRow{ID: id, UnitID: j.Spec.Unit.UnitID(), Reason: reason})
		} else {
			out.Jobs = append(out.Jobs, copyJob(j))
		}

		if f.Limit > 0 && read == f.Limit {
			out.After = id

			break
		}
	}

	return out, nil
}

// selects reports whether the filter f selects the job j.
func selects(f *job.Filter, j *job.Job) bool {
	switch {
	case f.Namespace != "" && j.Spec.Unit.Namespace() != f.Namespace:
		return false
	case f.UnitID != "" && j.Spec.Unit.UnitID() != f.UnitID:
		return false
	case len(f.States) > 0 && !slices.Contains(f.States, j.State):
		return false
	case !f.StateChangedAfter.IsZero() && !j.StateChangedAt.After(f.StateChangedAfter):
		return false
	default:
		return f.StateChangedBefore.IsZero() || j.StateChangedAt.Before(f.StateChangedBefore)
	}
}

// Apply implements job.Store.
func (m *memDB) Apply(ctx context.Context, c job.Change, fence namespace.Fence, e []audit.Entry) (job.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return job.Job{}, err
	}

	cur, ok := m.lookup(c.Job)
	if !ok || cur.Spec.Unit.Namespace() != fence.Namespace {
		return job.Job{}, job.ErrNotFound
	}

	if _, damaged := m.damaged[c.Job]; damaged {
		return job.Job{}, fmt.Errorf("%w: job %s", job.ErrDamaged, c.Job)
	}

	if err := checkNamespace(fence.Namespace, fence, e); err != nil {
		return job.Job{}, err
	}

	if err := checkEnd(&c); err != nil {
		return job.Job{}, err
	}

	if err := c.ValidateClaim(); err != nil {
		return job.Job{}, err
	}

	if cur.Version != c.Version {
		return job.Job{}, job.ErrConflict
	}

	if _, err := job.Next(cur.State, c.Event); err != nil {
		return job.Job{}, err
	}

	next := copyJob(cur)
	now := time.Now().UTC()

	if err := m.applyIn(ctx, cur, &next, &c, now); err != nil {
		return job.Job{}, err
	}

	if next.State != cur.State {
		next.StateChangedAt = now
	}

	m.jobs[next.ID] = &next
	m.audit[next.Spec.Unit.Namespace()] = append(m.audit[next.Spec.Unit.Namespace()], e...)

	return copyJob(&next), nil
}

// CancelDamaged implements job.Store. It reads no body: a row that a reader
// cannot decode can still be canceled by its ID.
func (m *memDB) CancelDamaged(_ context.Context, id job.ID, fence namespace.Fence, e []audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	cur, ok := m.lookup(id)
	if !ok || cur.Spec.Unit.Namespace() != fence.Namespace {
		return job.ErrNotFound
	}

	// A cancel through this path does not stop the durable operation of
	// the job, so it is only for a row that no other path can read.
	// Defense in depth: the caller checks that Get gives ErrDamaged;
	// this check stays so that a new caller cannot cancel a readable
	// job here.
	if _, damaged := m.damaged[id]; !damaged {
		return fmt.Errorf("%w: job %s is not damaged", job.ErrInvalid, id)
	}

	if cur.State.Terminal() {
		return fmt.Errorf("%w: job %s is %s", job.ErrFinished, id, cur.State)
	}

	if err := checkNamespace(fence.Namespace, fence, e); err != nil {
		return err
	}

	next := copyJob(cur)
	now := time.Now().UTC()
	next.State, next.Wait, next.Version = job.StateCanceled, job.WaitNone, cur.Version+1
	next.UpdatedAt, next.StateChangedAt = now, now

	m.jobs[id] = &next
	m.audit[next.Spec.Unit.Namespace()] = append(m.audit[next.Spec.Unit.Namespace()], e...)

	return nil
}

// settle sets the state, the version, the wait reason and the reason of next
// after the event of c moved the job from the version prev to state to.
func settle(next *job.Job, to job.State, c *job.Change, prev int64, now time.Time) {
	next.State, next.Version, next.UpdatedAt = to, prev+1, now

	next.Wait = job.WaitNone
	if to == job.StateQueued {
		next.Wait = c.Wait
	}

	// A terminal state records why the job ended, and a replan records why the
	// job plans again while it stays queued.
	if to.Terminal() || c.Event == job.EventReplan {
		next.Reason = c.Reason
	}
}

// SetVerification implements job.Store.
func (m *memDB) SetVerification(_ context.Context, id job.ID, v job.Verification, fence namespace.Fence,
	e []audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	j, ok := m.lookup(id)
	if !ok || j.Spec.Unit.Namespace() != fence.Namespace {
		return job.ErrNotFound
	}

	if j.Closed == nil || (v != job.VerificationVerified && v != job.VerificationUnverified) {
		return fmt.Errorf("%w: verification %q of job %s", job.ErrInvalid, v, id)
	}

	if j.Verification == v {
		return nil
	}

	next := copyJob(j)
	next.Verification = v
	next.Version++
	m.jobs[id] = &next
	m.audit[next.Spec.Unit.Namespace()] = append(m.audit[next.Spec.Unit.Namespace()], e...)

	return nil
}

// Orphans implements job.Store.
func (m *memDB) Orphans(ctx context.Context, limit int) ([]job.Orphan, []job.DamagedRow, error) {
	if limit <= 0 {
		return nil, nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		out     []job.Orphan
		damaged []job.DamagedRow
	)

	for _, id := range m.order {
		j, ok := m.lookup(id)
		if !ok || !j.State.HoldsUnit() || j.Current >= len(j.Phases) {
			continue
		}

		end, err := m.endedOperation(ctx, j)
		if err != nil {
			return nil, nil, err
		}

		if !end.ended {
			continue
		}

		if reason, isDamaged := m.damaged[id]; isDamaged {
			damaged = append(damaged, job.DamagedRow{ID: id, UnitID: j.Spec.Unit.UnitID(), Reason: reason})
		} else {
			out = append(out, job.Orphan{Job: copyJob(j), Missing: end.missing})
		}

		if len(out)+len(damaged) == limit {
			break
		}
	}

	return out, damaged, nil
}

// CreateFreeze implements job.FreezeStore.
func (m *memDB) CreateFreeze(_ context.Context, f job.Freeze, fence namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if err := f.Validate(); err != nil {
		return err
	}

	m.freezes[f.ID] = &f
	m.audit[f.Namespace] = append(m.audit[f.Namespace], e)

	return nil
}

// EndFreeze implements job.FreezeStore.
func (m *memDB) EndFreeze(_ context.Context, id, by string, fence namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	f, ok := m.freezes[id]
	if !ok {
		return job.ErrNotFound
	}

	now := time.Now()
	if !f.ActiveAt(now) && !f.EndsAt.IsZero() {
		return job.ErrFinished
	}

	f.EndsAt, f.EndedBy = now, by
	m.audit[f.Namespace] = append(m.audit[f.Namespace], e)

	return nil
}

// ListFreezes implements job.FreezeStore.
func (m *memDB) ListFreezes(_ context.Context, ns namespace.Name, since time.Time) ([]job.Freeze, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []job.Freeze

	for _, f := range m.freezes {
		if f.Namespace == ns && (f.EndsAt.IsZero() || f.EndsAt.After(since)) {
			out = append(out, *f)
		}
	}

	slices.SortFunc(out, func(a, b job.Freeze) int { return a.StartsAt.Compare(b.StartsAt) })

	return out, nil
}

// operationEnd is what Orphans reads of the durable operation of the current
// phase of a job: whether it has ended, and whether the durable store does not
// hold it.
type operationEnd struct {
	ended   bool
	missing bool
}

// endedOperation returns the end of the durable operation of the current phase
// of j.
func (m *memDB) endedOperation(ctx context.Context, j *job.Job) (operationEnd, error) {
	op, err := m.durable.Get(ctx, durable.ID(j.Phases[j.Current].Operation))
	if errors.Is(err, durable.ErrNotFound) {
		// The operation of a job that a restore of the control tables left
		// out is a missing orphan.
		return operationEnd{ended: true, missing: true}, nil
	}

	if err != nil {
		return operationEnd{ended: false, missing: false}, fmt.Errorf("get operation: %w", err)
	}

	return operationEnd{ended: op.State.Terminal(), missing: false}, nil
}

// namespaceLocked returns the namespace name. The caller holds m.mu.
func (m *memDB) namespaceLocked(name namespace.Name) (namespace.Namespace, error) {
	v, ok := m.fences[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: m.nsIDs[name], CreatedAt: time.Time{}, Name: name, PrimaryInstallation: "", FailoverVersion: v,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// lookup returns the job id, and false when the store does not know it.
func (m *memDB) lookup(id job.ID) (*job.Job, bool) {
	j, ok := m.jobs[id]
	if !ok || j == nil {
		return nil, false
	}

	return j, true
}

// admit returns the error of a create that the store refuses before it looks
// at the key: a stale fence, entries of another namespace, and a first state
// other than queued, or awaiting-approval with no dispatch.
func (m *memDB) admit(j *job.Job, d *durable.StartRequest, fence namespace.Fence, e []audit.Entry) error {
	if err := m.checkFence(fence); err != nil {
		return err
	}

	if err := checkNamespace(j.Spec.Unit.Namespace(), fence, e); err != nil {
		return err
	}

	queued := j.State == job.StateQueued
	waits := j.State == job.StateAwaitingApproval && d == nil

	if !queued && !waits {
		return job.ErrInvalid
	}

	return nil
}

// byKey returns the job that the idempotency key of j already names, as
// key, and false when j has no key or the key names no job.
func (m *memDB) byKey(key string, j *job.Job) (*job.Job, bool) {
	if j.IdempotencyKey == "" {
		return nil, false
	}

	id, ok := m.keys[key]
	if !ok {
		return nil, false
	}

	return m.lookup(id)
}

// checkFence returns namespace.ErrFenced when f is stale.
func (m *memDB) checkFence(f namespace.Fence) error {
	if v, ok := m.fences[f.Namespace]; !ok || v != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// open counts the jobs that are not terminal, of the unit unitID, or of the
// namespace ns when unitID is empty.
func (m *memDB) open(ns namespace.Name, unitID string) int {
	n := 0

	for _, j := range m.jobs {
		if j.State.Terminal() || j.Spec.Unit.Namespace() != ns {
			continue
		}

		if unitID == "" || j.Spec.Unit.UnitID() == unitID {
			n++
		}
	}

	return n
}

// checkDispatch checks the rules of the dispatch of the current phase of j
// at now: the unit index, the freeze and the plans.
func (m *memDB) checkDispatch(j *job.Job, notBefore, now time.Time) error {
	if j.Current >= len(j.Phases) {
		return nil
	}

	// The manual lock stops the dispatch of every phase, and no break-glass
	// passes it.
	if err := m.checkManualLock(j); err != nil {
		return err
	}

	if !j.Phases[j.Current].Kind.Changes() {
		return nil
	}

	if err := m.checkUnit(j); err != nil {
		return err
	}

	if err := m.checkFreeze(j, now); err != nil {
		return err
	}

	return m.checkPlans(j, notBefore)
}

// checkManualLock returns ErrLocked when a manual lock covers the unit of j: the
// lock of the unit, else the lock of its namespace. It finds the unit by its
// ID, as the transaction of a dispatch does, and fails closed for a unit that
// is not in the inventory.
func (m *memDB) checkManualLock(j *job.Job) error {
	unit := j.Spec.Unit

	ref, ok := m.units[unit.UnitID()]
	if !ok || ref.Namespace != unit.Namespace() {
		return fmt.Errorf("check the manual locks of unit %s: %w: %w", unit, job.ErrUnitGone, errNotInInventory)
	}

	for _, t := range []manuallock.Target{manuallock.UnitTarget(ref), manuallock.NamespaceTarget(unit.Namespace())} {
		if l, ok := m.locks[t.String()]; ok {
			return fmt.Errorf(
				"%w: %w",
				job.ErrLocked,
				&manuallock.LockedError{
					Lock: l,
					Unit: collection.UnitRef{Namespace: unit.Namespace(), Tool: unit.Tool(), Name: unit.UnitName()},
					Noun: "unit",
					Name: unit.String(),
				},
			)
		}
	}

	return nil
}

// checkUnit stands for the partial unique index on the unit of the jobs
// that hold it.
func (m *memDB) checkUnit(j *job.Job) error {
	for _, other := range m.jobs {
		if other.ID != j.ID && other.Spec.Unit.UnitID() == j.Spec.Unit.UnitID() && holds(other) {
			return fmt.Errorf("%w: %w", job.ErrUnitBusy, errUnitBusyIndex)
		}
	}

	return nil
}

// checkFreeze returns ErrFrozen when a freeze of the namespace of j is
// active at now and j has no break-glass.
func (m *memDB) checkFreeze(j *job.Job, now time.Time) error {
	if j.BreakGlass != nil {
		return nil
	}

	for _, f := range m.freezes {
		if f.Namespace == j.Spec.Unit.Namespace() && f.ActiveAt(now) {
			return fmt.Errorf("%w: %s", job.ErrFrozen, f.ID)
		}
	}

	return nil
}

// checkPlans checks the plans of the plan phase before the current phase
// of j, and the binding of each approval of the current phase.
func (m *memDB) checkPlans(j *job.Job, notBefore time.Time) error {
	if j.Current == 0 || j.Phases[j.Current-1].Kind != job.PhasePlan {
		return nil
	}

	plan := j.Phases[j.Current-1].Plan
	current := ""
	bindings := []*job.PlanBinding{nil}

	approvals := j.Phases[j.Current].Approvals
	for i := range approvals {
		if approvals[i].Plan != nil {
			bindings = append(bindings, approvals[i].Plan)
		}
	}

	for _, b := range bindings {
		if err := job.CheckPlan(plan, b, current, notBefore); err != nil {
			return fmt.Errorf("dispatch of phase %d: %w", j.Current, err)
		}
	}

	return nil
}

// startOp starts the operation of req and returns its ID.
func (m *memDB) startOp(ctx context.Context, req *durable.StartRequest) (string, error) {
	op, _, err := m.durable.Start(ctx, *req)
	if err != nil {
		return "", fmt.Errorf("start %s: %w", req.Kind, err)
	}

	return string(op.ID), nil
}

// followUp starts the follow-up operation of kind k of the job id.
func (m *memDB) followUp(ctx context.Context, id job.ID, k durable.Kind) error {
	_, err := m.startOp(ctx, &durable.StartRequest{Key: job.FollowUpKey(id, k), Kind: k, Version: 1, Input: nil})

	return err
}

// change makes the fields of c on next, and checks the rules of its event.
// It keeps nothing on an error.
func (m *memDB) change(ctx context.Context, next *job.Job, c *job.Change, now time.Time) error {
	if c.Approval != nil && next.Current < len(next.Phases) {
		next.Phases[next.Current].Approvals = append(next.Phases[next.Current].Approvals, *c.Approval)
	}

	if c.Event == job.EventDispatch {
		return m.dispatch(ctx, next, c, now)
	}

	if c.Event == job.EventClaim && next.Current < len(next.Phases) && next.Phases[next.Current].Kind == job.PhasePlan {
		next.Phases[next.Current].StateVersion = ""
	}

	if f, ok := changes[c.Event]; ok {
		return f(next, c, now)
	}

	return nil
}

// dispatch checks the rules of a dispatch and starts its operation.
func (m *memDB) dispatch(ctx context.Context, next *job.Job, c *job.Change, now time.Time) error {
	if c.Dispatch == nil {
		return fmt.Errorf("%w: a dispatch needs a start request", job.ErrInvalid)
	}

	if err := m.checkDispatch(next, c.PlanNotBefore, now); err != nil {
		return err
	}

	op, err := m.startOp(ctx, c.Dispatch)
	if err != nil {
		return err
	}

	next.Phases[next.Current].Operation = op

	return nil
}

// afterChange starts or cancels the operations that the change of cur to
// next needs.
func (m *memDB) afterChange(ctx context.Context, cur, next *job.Job, ev job.Event) error {
	if ev == job.EventCancel && cur.State.HoldsUnit() && cur.Current < len(cur.Phases) {
		id := durable.ID(cur.Phases[cur.Current].Operation)
		if err := m.durable.Cancel(ctx, id); err != nil && !errors.Is(err, durable.ErrFinished) {
			return fmt.Errorf("cancel %s: %w", id, err)
		}
	}

	if k, ok := followUpOf(next, ev); ok {
		return m.followUp(ctx, next.ID, k)
	}

	return nil
}

// followUpOf returns the kind of the follow-up operation that the event ev
// to next starts, and false when it starts none.
func followUpOf(next *job.Job, ev job.Event) (durable.Kind, bool) {
	switch {
	case next.Delivery == nil:
		return "", false
	case ev == job.EventDelivered && next.Delivery.Delivery == job.DeliveryReviewRequest &&
		next.State == job.StateAwaitingState:
		return job.KindReviewWatch, true
	case ev == job.EventCancel || ev == job.EventRejected || ev == job.EventExpire:
		return job.KindAbandon, true
	default:
		return "", false
	}
}

// changes maps an event to the change of the job record that it makes,
// apart from its state. An event with no entry changes only the state.
//
//nolint:gochecknoglobals // A fixed table, read only.
var changes = map[job.Event]func(next *job.Job, c *job.Change, now time.Time) error{
	job.EventClaim:         claimChange,
	job.EventReplan:        replanChange,
	job.EventPhaseDone:     finishChange,
	job.EventLastPhaseDone: finishChange,
	job.EventDelivered:     finishChange,
	job.EventMerged:        mergeChange,
}

// claimChange records a claim or a takeover of the current phase.
func claimChange(next *job.Job, c *job.Change, now time.Time) error {
	p := &next.Phases[next.Current]
	p.Claims++
	p.Executor = c.Executor

	if p.StartedAt.IsZero() {
		p.StartedAt = now
	}

	return nil
}

// replanChange moves the job back to the plan phase before the current
// phase.
func replanChange(next *job.Job, _ *job.Change, _ time.Time) error {
	if next.Current == 0 || next.Phases[next.Current-1].Kind != job.PhasePlan {
		return fmt.Errorf("%w: no plan phase before phase %d", job.ErrInvalid, next.Current)
	}

	next.Phases[next.Current].Approvals = nil
	next.Current--
	next.Phases[next.Current].Plan = nil
	next.Phases[next.Current].StateVersion = ""
	next.Phases[next.Current].Operation = ""

	return nil
}

// finishChange ends the current phase, and keeps the delivery of a
// deliver phase.
func finishChange(next *job.Job, c *job.Change, now time.Time) error {
	next.Phases[next.Current].FinishedAt = now
	next.Current++

	if c.Delivery != nil {
		d := *c.Delivery
		next.Delivery = &d
	}

	return nil
}

// mergeChange records the merge of the review request.
func mergeChange(next *job.Job, c *job.Change, _ time.Time) error {
	if next.Delivery == nil || next.Delivery.Delivery != job.DeliveryReviewRequest || c.MergedAt.IsZero() {
		return fmt.Errorf("%w: a merge needs a review request and a time", job.ErrInvalid)
	}

	next.Delivery.MergedAt = c.MergedAt

	return nil
}

// copyJob returns a deep enough copy of j for a caller: the phases and
// their records are new slices.
func copyJob(j *job.Job) job.Job {
	out := *j
	out.Phases = slices.Clone(j.Phases)

	for i := range out.Phases {
		out.Phases[i].Steps = slices.Clone(out.Phases[i].Steps)

		for k := range out.Phases[i].Steps {
			if l := out.Phases[i].Steps[k].Log; l != nil {
				c := *l
				out.Phases[i].Steps[k].Log = &c
			}
		}

		if p := out.Phases[i].Plan; p != nil {
			c := *p
			c.Parts = slices.Clone(p.Parts)
			out.Phases[i].Plan = &c
		}

		out.Phases[i].Gates = slices.Clone(out.Phases[i].Gates)
		out.Phases[i].Approvals = slices.Clone(out.Phases[i].Approvals)
	}

	return out
}

// holds reports whether j holds its unit for a change.
func holds(j *job.Job) bool {
	return j.State.HoldsUnit() && j.Current < len(j.Phases) && j.Phases[j.Current].Kind.Changes()
}

// checkNamespace returns job.ErrInvalid when ns is not the namespace of fence,
// and audit.ErrInvalidEntry for an entry of another namespace.
func checkNamespace(ns namespace.Name, fence namespace.Fence, e []audit.Entry) error {
	if ns != fence.Namespace {
		return fmt.Errorf("%w: namespace %q, fence %q", job.ErrInvalid, ns, fence.Namespace)
	}

	for i := range e {
		if e[i].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e[i].Namespace,
				fence.Namespace)
		}
	}

	return nil
}

// checkClaim returns durable.ErrClaimLost unless c is the live claim of its
// operation.
func (m *memDB) checkClaim(ctx context.Context, c durable.Claim) error {
	op, err := m.durable.Get(ctx, c.Operation)
	if err != nil {
		return durable.ErrClaimLost
	}

	if op.State != durable.StateRunning || op.Claim.Token != c.Token || !op.Claim.ExpiresAt.After(time.Now()) {
		return durable.ErrClaimLost
	}

	return nil
}

// checkEnd returns job.ErrInvalid for a change whose end is not valid or
// whose event cannot carry an end.
func checkEnd(c *job.Change) error {
	if c.End == nil {
		return nil
	}

	if !job.AllowsEnd(c.Event) {
		return fmt.Errorf("%w: event %s cannot end an operation", job.ErrInvalid, c.Event)
	}

	return c.End.Validate()
}

// bindClaim binds the event of c.Claim to its claim, with the checks of
// jobstore.bindClaim. It changes nothing.
func (m *memDB) bindClaim(ctx context.Context, cur *job.Job, c *job.Change) error {
	if c.Claim == nil {
		return nil
	}

	if cur.Current >= len(cur.Phases) || cur.Phases[cur.Current].Operation != string(c.Claim.Operation) {
		return fmt.Errorf("%w: %w: job %s is not at the phase of operation %s", job.ErrClaimLost,
			durable.ErrClaimLost, cur.ID, c.Claim.Operation)
	}

	if err := m.checkClaim(ctx, *c.Claim); err != nil {
		return fmt.Errorf("claim of operation %s: %w: %w", c.Claim.Operation, job.ErrClaimLost, err)
	}

	return nil
}

// endOperation ends the durable operation of c.End, in the one step of the
// change of cur. It runs last, after the checks of the event (as the SQL
// adapters run it), so a refused change ends nothing. It checks that the
// job is at the phase of the operation, so the event binds to its claim.
func (m *memDB) endOperation(ctx context.Context, cur *job.Job, c *job.Change) error {
	if c.End == nil {
		return nil
	}

	if cur.Current >= len(cur.Phases) || cur.Phases[cur.Current].Operation != string(c.End.Claim.Operation) {
		return fmt.Errorf("%w: %w: job %s is not at the phase of operation %s", job.ErrClaimLost,
			durable.ErrClaimLost, cur.ID, c.End.Claim.Operation)
	}

	var err error

	switch c.End.Kind {
	case job.EndComplete:
		err = m.durable.Complete(ctx, c.End.Claim)
		if errors.Is(err, durable.ErrCanceled) {
			err = m.durable.Release(ctx, c.End.Claim)
		}
	case job.EndFail:
		err = m.durable.Fail(ctx, c.End.Claim, c.End.Failure)
	case job.EndRelease:
		err = m.durable.Release(ctx, c.End.Claim)
	default:
		err = fmt.Errorf("%w: unknown end %q", job.ErrInvalid, c.End.Kind)
	}

	if errors.Is(err, durable.ErrClaimLost) {
		return fmt.Errorf("end operation %s: %w: %w", c.End.Claim.Operation, job.ErrClaimLost, err)
	}

	if err != nil {
		return fmt.Errorf("end operation %s: %w", c.End.Claim.Operation, err)
	}

	return nil
}

// applyIn makes the change c of cur on next, with the rules of its event, then
// ends the operation of the end of c and binds the event to its claim.
func (m *memDB) applyIn(ctx context.Context, cur, next *job.Job, c *job.Change, now time.Time) error {
	to, err := job.Next(cur.State, c.Event)
	if err != nil {
		return err
	}

	if err := m.change(ctx, next, c, now); err != nil {
		return err
	}

	settle(next, to, c, cur.Version, now)

	if err := m.endOperation(ctx, cur, c); err != nil {
		return err
	}

	if err := m.bindClaim(ctx, cur, c); err != nil {
		return err
	}

	return m.afterChange(ctx, cur, next, c.Event)
}
