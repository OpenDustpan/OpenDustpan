package durabletest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Mem is a store of durable operations in memory, for tests of code that
// uses the port. It follows every rule of durable.Store and passes the
// suite. It serves one process only, and its clock is the clock of that
// process.
type Mem struct {
	db *memDB
}

// NewMem returns an empty store in memory.
func NewMem() *Mem {
	return &Mem{db: newMemDB("")}
}

// Store returns a handle on m. Every handle shares the operations of m, as
// handles on one database do.
//
//nolint:ireturn // A test of code that uses the port takes the port, as the store adapters give it.
func (m *Mem) Store() durable.Store {
	return memStore{db: m.db, skew: 0}
}

// Harness returns a suite harness on m.
func (m *Mem) Harness() Harness {
	return m.db.harness()
}

// StartClaimedWith starts the operation of req with the rules of
// durable.Store.Start and, when the start creates it, claims it for claim
// with the rules of durable.StartClaim, as a store method of another port
// does. After both writes, inside the transaction, it calls hooks.After. An
// error of hooks.After rolls both writes back. It returns
// durable.ErrKeyConflict when req.Key names an operation already and
// hooks.After accepts, and
// durable.ErrInvalid for a request or a claim that fails Validate.
func (m *Mem) StartClaimedWith(
	_ context.Context, req durable.StartRequest, claim durable.StartClaim, hooks Hooks,
) (durable.Operation, error) {
	if err := claim.Validate(); err != nil {
		return durable.Operation{}, err
	}

	d := m.db

	d.mu.Lock()
	defer d.mu.Unlock()

	// A copy of the operations stands in for the transaction: a rollback
	// puts it back.
	ops, byKey, nextID := maps.Clone(d.ops), maps.Clone(d.byKey), d.nextID

	now := d.now()

	op, created, err := d.start(req, now)

	if err == nil && created {
		started := d.ops[op.ID]
		if started == nil {
			// The start stored the operation under its ID, so this is a
			// fault of the store in memory.
			err = fmt.Errorf("started operation %s: %w", op.ID, durable.ErrNotFound)
		} else {
			d.take(started, durable.ClaimRequest{
				Owner: claim.Owner, Kinds: []durable.Kind{req.Kind}, MaxVersions: nil, Lease: claim.Lease,
				TakeCanceled: false,
			}, now)

			op = copyOp(started)
		}
	}

	// The domain write runs also for a key that exists, as StartWith does,
	// so its own refusal comes first.
	if err == nil {
		err = hooks.Run(hooks.After)
	}

	if err == nil && !created {
		err = durable.ErrKeyConflict
	}

	if err != nil {
		d.ops, d.byKey, d.nextID = ops, byKey, nextID

		return durable.Operation{}, err
	}

	return op, nil
}

// errUnknownState reports an operation state that memDB does not know.
var errUnknownState = errors.New("unknown operation state")

// The rules that a broken memDB breaks, for the sensitivity tests of the
// suite. Each one is a mutation that the suite must kill. The switch is
// unexported: only the tests of this package can make a broken store, with
// newMemDB. It sits in this file because the store that it breaks is the
// exported Mem.
const (
	// breakToken accepts a write with a stale token.
	breakToken = "token"
	// breakExpiry accepts a write of a claim that expired.
	breakExpiry = "expiry"
	// breakEndExpiry accepts Fail and Release of a claim that expired.
	breakEndExpiry = "end-expiry"
	// breakTakeoverSteps drops the recorded steps at a takeover.
	breakTakeoverSteps = "takeover-steps"
	// breakClock takes every time from the clock of the instance.
	breakClock = "clock"
	// breakTokenStays gives every claim the token 1.
	breakTokenStays = "token-stays"
	// breakLive lets a claim take a running operation with a live claim.
	breakLive = "live"
	// breakTxStart takes the time of a step write at the start of its
	// transaction, as now() does on PostgreSQL.
	breakTxStart = "tx-start"
	// breakExpiryCount counts every claim as an expiry.
	breakExpiryCount = "expiry-count"
	// breakMaxVersions ignores the version limits of a claim.
	breakMaxVersions = "max-versions"
	// breakTickBacklog moves the next run of a schedule by one interval
	// from the tick it claims, so missed ticks run one by one.
	breakTickBacklog = "tick-backlog"
	// breakNoScheduleOnNoWork answers a tick that is not due with no
	// schedule, so a caller sees a wait of 0.
	breakNoScheduleOnNoWork = "noschedule-on-nowork"
)

// memDB is a reference store in memory. Its lock makes each method one
// transaction. A method that fails returns before it changes anything, which
// is the rollback.
type memDB struct {
	ops       map[durable.ID]*durable.Operation
	byKey     map[string]durable.ID
	schedules map[string]*memSchedule
	beats     map[string]memBeat
	fences    map[namespace.Name]int64
	values    []string
	broken    string
	mu        sync.Mutex
	nextID    int
	lastStamp time.Time
}

func newMemDB(broken string) *memDB {
	return &memDB{
		ops:       map[durable.ID]*durable.Operation{},
		byKey:     map[string]durable.ID{},
		schedules: map[string]*memSchedule{},
		beats:     map[string]memBeat{},
		fences:    map[namespace.Name]int64{},
		values:    nil,
		broken:    broken,
		mu:        sync.Mutex{},
		nextID:    0,
		lastStamp: time.Time{},
	}
}

func (m *memDB) harness() Harness {
	return Harness{
		Open:   func(_ *testing.T, skew time.Duration) durable.Store { return memStore{db: m, skew: skew} },
		Domain: memDomain{db: m},
	}
}

// now is the clock of the store. It keeps milliseconds, as the SQL stores
// do, and never goes back.
func (m *memDB) now() time.Time {
	t := time.Now().UTC().Truncate(time.Millisecond)
	if t.Before(m.lastStamp) {
		t = m.lastStamp
	}

	m.lastStamp = t

	return t
}

func copyOp(op *durable.Operation) durable.Operation {
	var c durable.Operation
	if op == nil {
		return c
	}

	c = *op
	c.Input = slices.Clone(op.Input)
	c.Steps = slices.Clone(op.Steps)

	return c
}

// clock is the clock that a handle with skew uses. A correct store ignores
// skew: every time comes from the clock of the store.
func (m *memDB) clock(skew time.Duration) time.Time {
	now := m.now()
	if m.broken == breakClock {
		return now.Add(skew)
	}

	return now
}

// expiryRule says whether a write checks the expiry of its claim. Only a
// broken store skips it.
type expiryRule int

const (
	checkExpiry expiryRule = iota
	skipExpiry
)

// cancelRule says what an end write does when a cancel is requested.
type cancelRule int

const (
	// refuseOnCancel returns ErrCanceled and changes nothing.
	refuseOnCancel cancelRule = iota
	// endOnCancel ends the operation canceled.
	endOnCancel
)

// live checks that c is the live claim of its operation, at now.
func (m *memDB) live(c durable.Claim, now time.Time, rule expiryRule) (*durable.Operation, error) {
	op := m.ops[c.Operation]
	if op == nil || op.State != durable.StateRunning {
		return nil, durable.ErrClaimLost
	}

	if !now.Before(op.Claim.ExpiresAt) && rule == checkExpiry && m.broken != breakExpiry {
		return nil, durable.ErrClaimLost
	}

	if op.Claim.Token != c.Token && m.broken != breakToken {
		return nil, durable.ErrClaimLost
	}

	return op, nil
}

func (m *memDB) endCanceled(op *durable.Operation, now time.Time) {
	op.State = durable.StateCanceled
	op.Claim = zeroClaim()
	op.UpdatedAt = now
}

func zeroClaim() durable.Claim {
	return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}
}

// checkStep applies the rules of RecordStep up to the record, in their
// order: the claim, the cancel, the step. It changes nothing. It returns the
// operation when the step can be recorded.
func (m *memDB) checkStep(f durable.StepFence, now time.Time) (*durable.Operation, error) {
	c := durable.Claim{Operation: f.Operation, Owner: "", Token: f.Token, ExpiresAt: time.Time{}}

	op, err := m.live(c, now, checkExpiry)
	if err != nil {
		return nil, err
	}

	if op.CancelRequested {
		return nil, durable.ErrCanceled
	}

	if _, done := op.Step(f.Step); done {
		return nil, durable.ErrStepDone
	}

	return op, nil
}

func (m *memDB) start(req durable.StartRequest, now time.Time) (durable.Operation, bool, error) {
	if err := req.Validate(); err != nil {
		return durable.Operation{}, false, err
	}

	if op := m.ops[m.byKey[req.Key]]; op != nil {
		if op.Kind != req.Kind {
			return durable.Operation{}, false, durable.ErrKeyConflict
		}

		return copyOp(op), false, nil
	}

	m.nextID++
	op := &durable.Operation{
		ID:              durable.ID(fmt.Sprintf("op-%06d", m.nextID)),
		Key:             req.Key,
		Kind:            req.Kind,
		Version:         req.Version,
		Input:           slices.Clone(req.Input),
		State:           durable.StatePending,
		Steps:           nil,
		Attempt:         0,
		Expiries:        0,
		Failures:        0,
		LastError:       "",
		Claim:           zeroClaim(),
		CancelRequested: false,
		NotBefore:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	m.ops[op.ID] = op
	m.byKey[req.Key] = op.ID

	return copyOp(op), true, nil
}

// memStore is one handle on a memDB. skew is the offset of the clock of the
// instance that holds the handle.
type memStore struct {
	db   *memDB
	skew time.Duration
}

func (s memStore) Start(_ context.Context, req durable.StartRequest) (durable.Operation, bool, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	return s.db.start(req, s.db.clock(s.skew))
}

func (s memStore) Get(_ context.Context, id durable.ID) (durable.Operation, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	op := s.db.ops[id]
	if op == nil {
		return durable.Operation{}, durable.ErrNotFound
	}

	return copyOp(op), nil
}

func (s memStore) List(_ context.Context, f durable.Filter) ([]durable.Operation, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	out := []durable.Operation{}

	for _, op := range s.db.sorted() {
		if f.Kind != "" && op.Kind != f.Kind {
			continue
		}

		if len(f.States) > 0 && !slices.Contains(f.States, op.State) {
			continue
		}

		out = append(out, copyOp(op))
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}

	return out, nil
}

func (m *memDB) sorted() []*durable.Operation {
	ops := make([]*durable.Operation, 0, len(m.ops))
	for _, op := range m.ops {
		ops = append(ops, op)
	}

	slices.SortFunc(ops, func(a, b *durable.Operation) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}

		return strings.Compare(string(a.ID), string(b.ID))
	})

	return ops
}

func (s memStore) Claim(_ context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	if err := req.Validate(); err != nil {
		return durable.Operation{}, err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	for _, op := range s.db.sorted() {
		if !s.db.allows(req, op) {
			continue
		}

		due := s.db.due(op, now)

		switch {
		case due == notDue:
		case due == dueTakeover && op.CancelRequested && !req.TakeCanceled:
			s.db.endCanceled(op, now)
		default:
			s.db.takeOver(op, due, req, now)

			return copyOp(op), nil
		}
	}

	return durable.Operation{}, durable.ErrNoWork
}

// takeOver gives op a new claim for req, with the changes of a takeover when
// due says that the claim before it expired.
func (m *memDB) takeOver(op *durable.Operation, due dueKind, req durable.ClaimRequest, now time.Time) {
	if due == dueTakeover && m.broken == breakTakeoverSteps {
		op.Steps = nil
	}

	if due == dueTakeover || m.broken == breakExpiryCount {
		op.Expiries++
	}

	m.take(op, req, now)
}

// dueKind says whether a claim can take an operation, and how.
type dueKind int

const (
	notDue dueKind = iota
	// dueFirst is a pending operation whose retry delay passed.
	dueFirst
	// dueTakeover is a running operation whose claim expired.
	dueTakeover
)

// allows reports whether req lets a claim take op. The broken store
// ignores the version limits.
func (m *memDB) allows(req durable.ClaimRequest, op *durable.Operation) bool {
	if m.broken == breakMaxVersions {
		return slices.Contains(req.Kinds, op.Kind)
	}

	return req.Allows(op.Kind, op.Version)
}

// due reports whether a claim can take op at now.
func (m *memDB) due(op *durable.Operation, now time.Time) dueKind {
	switch {
	case op.State == durable.StatePending && !now.Before(op.NotBefore):
		return dueFirst
	case op.State == durable.StateRunning && (!now.Before(op.Claim.ExpiresAt) || m.broken == breakLive):
		return dueTakeover
	default:
		return notDue
	}
}

// take gives op a new claim for req at now.
func (m *memDB) take(op *durable.Operation, req durable.ClaimRequest, now time.Time) {
	// The attempt grows on each claim, so it serves as the token.
	token := int64(op.Attempt + 1)
	if m.broken == breakTokenStays {
		token = 1
	}

	op.State = durable.StateRunning
	op.Attempt++
	op.Claim = durable.Claim{Operation: op.ID, Owner: req.Owner, Token: token, ExpiresAt: now.Add(req.Lease)}
	op.UpdatedAt = now
}

func (s memStore) Extend(_ context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	if lease <= 0 {
		return durable.Claim{}, durable.ErrInvalid
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	op, err := s.db.live(c, now, checkExpiry)
	if err != nil {
		return durable.Claim{}, err
	}

	if op.CancelRequested {
		return durable.Claim{}, durable.ErrCanceled
	}

	op.Claim.ExpiresAt = now.Add(lease)
	op.UpdatedAt = now

	return op.Claim, nil
}

func (s memStore) RecordStep(_ context.Context, f durable.StepFence, result []byte) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	op, err := s.db.checkStep(f, now)
	if err != nil {
		return err
	}

	op.Steps = append(op.Steps, durable.StepRecord{Name: f.Step, Result: slices.Clone(result), DoneAt: now})
	op.UpdatedAt = now

	return nil
}

func (s memStore) Complete(_ context.Context, c durable.Claim) error {
	return s.end(c, refuseOnCancel, func(op *durable.Operation, _ time.Time) { op.State = durable.StateSucceeded })
}

func (s memStore) Fail(_ context.Context, c durable.Claim, f durable.Failure) error {
	return s.end(c, endOnCancel, func(op *durable.Operation, now time.Time) {
		op.Failures++
		op.LastError = f.Message
		if f.Final {
			op.State = durable.StateFailed

			return
		}

		op.State = durable.StatePending
		op.NotBefore = now.Add(f.RetryAfter)
	})
}

func (s memStore) Release(_ context.Context, c durable.Claim) error {
	return s.end(c, endOnCancel, func(op *durable.Operation, now time.Time) {
		op.State = durable.StatePending
		op.NotBefore = now
	})
}

func (s memStore) GiveBack(_ context.Context, c durable.Claim) error {
	return s.end(c, refuseOnCancel, func(op *durable.Operation, now time.Time) {
		op.State = durable.StatePending
		op.NotBefore = now
	})
}

func (s memStore) Cancel(_ context.Context, id durable.ID) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	op := s.db.ops[id]
	if op == nil {
		return durable.ErrNotFound
	}

	return cancelOp(op, s.db.clock(s.skew))
}

// CancelBefore implements durable.StepCanceler. The lock of the database
// makes the check of the step and the cancel one step.
func (s memStore) CancelBefore(_ context.Context, id durable.ID, step string) error {
	if step == "" {
		return durable.ErrInvalid
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	op := s.db.ops[id]
	if op == nil {
		return durable.ErrNotFound
	}

	if op.State == durable.StateSucceeded || op.State == durable.StateFailed {
		return durable.ErrFinished
	}

	if _, done := op.Step(step); done {
		return durable.ErrStepDone
	}

	return cancelOp(op, s.db.clock(s.skew))
}

// cancelOp applies a cancel to op at now, with the rules of Cancel. The
// caller holds the lock of the database.
func cancelOp(op *durable.Operation, now time.Time) error {
	switch op.State {
	case durable.StatePending:
		op.State = durable.StateCanceled
		op.UpdatedAt = now
	case durable.StateRunning:
		op.CancelRequested = true
		op.UpdatedAt = now
	case durable.StateSucceeded, durable.StateFailed:
		return durable.ErrFinished
	case durable.StateCanceled:
	default:
		return fmt.Errorf("operation %s: %w: %q", op.ID, errUnknownState, op.State)
	}

	return nil
}

// memDomain is the domain side of a memDB.
type memDomain struct {
	db *memDB
}

func (d memDomain) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	d.db.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

func (d memDomain) BumpFences(context.Context) error {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	for name := range d.db.fences {
		d.db.fences[name]++
	}

	return nil
}

func (d memDomain) WriteStep(
	_ context.Context,
	f durable.StepFence,
	ns namespace.Fence,
	value string,
	hooks Hooks,
) error {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	// The lock is the transaction, so before runs inside it.
	txStart := d.db.now()

	if err := hooks.Run(hooks.Before); err != nil {
		return err
	}

	if d.db.fences[ns.Namespace] != ns.FailoverVersion {
		return namespace.ErrFenced
	}

	now := d.db.now()
	if d.db.broken == breakTxStart {
		now = txStart
	}

	op, err := d.db.checkStep(f, now)
	if err != nil {
		return err
	}

	if err := hooks.Run(hooks.After); err != nil {
		return err
	}

	op.Steps = append(op.Steps, durable.StepRecord{Name: f.Step, Result: nil, DoneAt: now})
	op.UpdatedAt = now
	d.db.values = append(d.db.values, value)

	return nil
}

func (d memDomain) StartWith(
	_ context.Context, req durable.StartRequest, value string, hooks Hooks,
) (durable.Operation, error) {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	// A copy of the operations stands in for the transaction: a rollback
	// puts it back.
	ops, byKey, nextID := maps.Clone(d.db.ops), maps.Clone(d.db.byKey), d.db.nextID

	op, _, err := d.db.start(req, d.db.now())
	if err == nil {
		err = hooks.Run(hooks.After)
	}

	if err != nil {
		d.db.ops, d.db.byKey, d.db.nextID = ops, byKey, nextID

		return durable.Operation{}, err
	}

	d.db.values = append(d.db.values, value)

	return op, nil
}

func (d memDomain) Values(context.Context) ([]string, error) {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	return slices.Clone(d.db.values), nil
}

// memSchedule is the stored state of one schedule.
type memSchedule struct {
	interval time.Duration
	next     time.Time
	last     time.Time
}

func (m *memDB) schedule(name string, sc *memSchedule, now time.Time) durable.Schedule {
	return durable.Schedule{Name: name, Interval: sc.interval, NextRunAt: sc.next, LastClaimedAt: sc.last, Now: now}
}

// ScheduleStore returns a schedule handle on m, with the clock skew of an
// instance.
//
//nolint:ireturn // A test of code that uses the port takes the port, as the store adapters give it.
func (m *Mem) ScheduleStore(skew time.Duration) ScheduledStore {
	return memStore{db: m.db, skew: skew}
}

// ScheduleHarness returns a schedule suite harness on m.
func (m *Mem) ScheduleHarness() ScheduleHarness {
	return m.db.scheduleHarness()
}

func (m *memDB) scheduleHarness() ScheduleHarness {
	return ScheduleHarness{
		Open: func(_ *testing.T, skew time.Duration) ScheduledStore { return memStore{db: m, skew: skew} },
	}
}

func (s memStore) EnsureSchedule(_ context.Context, spec durable.ScheduleSpec) (durable.Schedule, error) {
	if err := spec.Validate(); err != nil {
		return durable.Schedule{}, err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	sc := s.db.schedules[spec.Name]
	if sc == nil {
		sc = &memSchedule{interval: spec.Interval, next: now, last: time.Time{}}
		s.db.schedules[spec.Name] = sc
	}

	sc.interval = spec.Interval
	if soonest := now.Add(spec.Interval); soonest.Before(sc.next) {
		sc.next = soonest
	}

	return s.db.schedule(spec.Name, sc, now), nil
}

func (s memStore) GetSchedule(_ context.Context, name string) (durable.Schedule, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	sc := s.db.schedules[name]
	if sc == nil {
		return durable.Schedule{}, durable.ErrNotFound
	}

	return s.db.schedule(name, sc, s.db.clock(s.skew)), nil
}

func (s memStore) ClaimTick(_ context.Context, spec durable.ScheduleSpec) (durable.Tick, error) {
	if err := spec.Validate(); err != nil {
		return durable.Tick{}, err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	sc := s.db.schedules[spec.Name]
	if sc == nil {
		return durable.Tick{}, durable.ErrNotFound
	}

	if now.Before(sc.next) {
		var none durable.Tick
		if s.db.broken != breakNoScheduleOnNoWork {
			none.Schedule = s.db.schedule(spec.Name, sc, now)
		}

		return none, durable.ErrNoWork
	}

	dueAt := sc.next

	op, _, err := s.db.start(durable.StartRequest{
		Key: durable.TickKey(spec.Name, dueAt), Kind: spec.Kind, Version: spec.Version, Input: spec.Input,
	}, now)
	if err != nil {
		return durable.Tick{}, err
	}

	sc.next, sc.last = now.Add(sc.interval), now
	if s.db.broken == breakTickBacklog {
		sc.next = dueAt.Add(sc.interval)
	}

	return durable.Tick{Operation: op, Schedule: s.db.schedule(spec.Name, sc, now)}, nil
}

// HeartbeatHarness returns a heartbeat suite harness on m.
func (m *Mem) HeartbeatHarness() HeartbeatHarness {
	return m.db.heartbeatHarness()
}

func (m *memDB) heartbeatHarness() HeartbeatHarness {
	return HeartbeatHarness{
		Open: func(_ *testing.T, skew time.Duration) durable.HeartbeatStore { return memStore{db: m, skew: skew} },
		Seed: func(_ *testing.T, owner string, at time.Time, lease time.Duration) {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.beats[owner] = memBeat{at: at, lease: lease}
		},
	}
}

// memBeat is the beat of one owner: its time and the lease period that it
// recorded.
type memBeat struct {
	at    time.Time
	lease time.Duration
}

func (s memStore) Beat(_ context.Context, owner string, lease time.Duration) error {
	if owner == "" {
		return fmt.Errorf("heartbeat owner is empty: %w", durable.ErrInvalid)
	}

	if lease <= 0 {
		return fmt.Errorf("heartbeat lease %v is not positive: %w", lease, durable.ErrInvalid)
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	for name, b := range s.db.beats {
		if now.Sub(b.at) > durable.HeartbeatRetention {
			delete(s.db.beats, name)
		}
	}

	s.db.beats[owner] = memBeat{at: now, lease: lease}

	return nil
}

func (s memStore) LastBeat(_ context.Context) (durable.Beat, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	beat := durable.Beat{Owner: "", At: time.Time{}, Lease: 0, Now: s.db.clock(s.skew)}

	var validUntil int64

	for owner, b := range s.db.beats {
		// The same int64 millisecond sum as the SQL stores, so a long lease
		// cannot overflow time.Duration.
		until := b.at.UnixMilli() + 2*b.lease.Milliseconds()

		if beat.Owner == "" || until > validUntil ||
			(until == validUntil && (b.at.After(beat.At) || (b.at.Equal(beat.At) && owner > beat.Owner))) {
			beat.Owner, beat.At, beat.Lease, validUntil = owner, b.at, b.lease, until
		}
	}

	return beat, nil
}

// end ends the attempt of c with fn, after the checks that Complete, Fail and
// Release share. A requested cancel makes Complete fail with no change, and
// makes Fail and Release end the operation canceled.
func (s memStore) end(c durable.Claim, onCancel cancelRule, fn func(op *durable.Operation, now time.Time)) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.clock(s.skew)

	rule := checkExpiry
	if onCancel == endOnCancel && s.db.broken == breakEndExpiry {
		rule = skipExpiry
	}

	op, err := s.db.live(c, now, rule)
	if err != nil {
		return err
	}

	if op.CancelRequested {
		if onCancel == refuseOnCancel {
			return durable.ErrCanceled
		}

		s.db.endCanceled(op, now)

		return nil
	}

	fn(op, now)
	op.Claim = zeroClaim()
	op.UpdatedAt = now

	return nil
}
