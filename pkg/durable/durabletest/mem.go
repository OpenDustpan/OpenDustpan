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
	return memStore{db: m.db}
}

// Harness returns a suite harness on m.
func (m *Mem) Harness() Harness {
	return m.db.harness()
}

// errUnknownState reports an operation state that memDB does not know.
var errUnknownState = errors.New("unknown operation state")

// The rules that a broken memDB breaks.
const (
	// breakToken accepts a write from any claim of a running operation
	// whose live claim has not expired, whatever its token.
	breakToken = "token"
	// breakLive lets a claim take a running operation with a live claim.
	breakLive = "live"
)

// memDB is a reference store in memory. Its lock makes each method one
// transaction. A method that fails returns before it changes anything, which
// is the rollback.
type memDB struct {
	ops       map[durable.ID]*durable.Operation
	byKey     map[string]durable.ID
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
		Open:   func(*testing.T) durable.Store { return memStore{db: m} },
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

// live checks that c is the live claim of its operation, at now.
func (m *memDB) live(c durable.Claim, now time.Time) (*durable.Operation, error) {
	op := m.ops[c.Operation]
	if op == nil || op.State != durable.StateRunning || !now.Before(op.Claim.ExpiresAt) {
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

// checkStep applies the rules of RecordStep up to the record. It returns the
// operation when the step can be recorded.
func (m *memDB) checkStep(f durable.StepFence, now time.Time) (*durable.Operation, error) {
	op, err := m.live(durable.Claim{Operation: f.Operation, Owner: "", Token: f.Token, ExpiresAt: time.Time{}}, now)
	if err != nil {
		return nil, err
	}

	if op.CancelRequested {
		m.endCanceled(op, now)

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
		Input:           slices.Clone(req.Input),
		State:           durable.StatePending,
		Steps:           nil,
		Attempt:         0,
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

// memStore is one handle on a memDB.
type memStore struct {
	db *memDB
}

func (s memStore) Start(_ context.Context, req durable.StartRequest) (durable.Operation, bool, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	return s.db.start(req, s.db.now())
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

	now := s.db.now()

	for _, op := range s.db.sorted() {
		if !slices.Contains(req.Kinds, op.Kind) {
			continue
		}

		pendingDue := op.State == durable.StatePending && !now.Before(op.NotBefore)
		expired := op.State == durable.StateRunning && (!now.Before(op.Claim.ExpiresAt) || s.db.broken == breakLive)

		if !pendingDue && !expired {
			continue
		}

		if expired && op.CancelRequested {
			s.db.endCanceled(op, now)

			continue
		}

		op.State = durable.StateRunning
		op.Attempt++
		op.Claim = durable.Claim{
			Operation: op.ID,
			Owner:     req.Owner,
			Token:     int64(op.Attempt), // The attempt grows on each claim, so it serves as the token.
			ExpiresAt: now.Add(req.Lease),
		}
		op.UpdatedAt = now

		return copyOp(op), nil
	}

	return durable.Operation{}, durable.ErrNoWork
}

func (s memStore) Extend(_ context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	if lease <= 0 {
		return durable.Claim{}, durable.ErrInvalid
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.now()

	op, err := s.db.live(c, now)
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

	now := s.db.now()

	op, err := s.db.checkStep(f, now)
	if err != nil {
		return err
	}

	op.Steps = append(op.Steps, durable.StepRecord{Name: f.Step, Result: slices.Clone(result), DoneAt: now})
	op.UpdatedAt = now

	return nil
}

func (s memStore) Complete(_ context.Context, c durable.Claim) error {
	return s.end(c, func(op *durable.Operation, _ time.Time) { op.State = durable.StateSucceeded })
}

func (s memStore) Fail(_ context.Context, c durable.Claim, f durable.Failure) error {
	return s.end(c, func(op *durable.Operation, now time.Time) {
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
	return s.end(c, func(op *durable.Operation, now time.Time) {
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

	now := s.db.now()

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
		return fmt.Errorf("operation %s: %w: %q", id, errUnknownState, op.State)
	}

	return nil
}

// end ends the attempt of c with fn, after the checks that Complete, Fail and
// Release share.
func (s memStore) end(c durable.Claim, fn func(op *durable.Operation, now time.Time)) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	now := s.db.now()

	op, err := s.db.live(c, now)
	if err != nil {
		return err
	}

	if op.CancelRequested {
		s.db.endCanceled(op, now)

		return durable.ErrCanceled
	}

	fn(op, now)
	op.Claim = zeroClaim()
	op.UpdatedAt = now

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
	_ context.Context, f durable.StepFence, ns namespace.Fence, value string, after func() error,
) error {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	if d.db.fences[ns.Namespace] != ns.FailoverVersion {
		return namespace.ErrFenced
	}

	now := d.db.now()

	op, err := d.db.checkStep(f, now)
	if err != nil {
		return err
	}

	if err := after(); err != nil {
		return err
	}

	op.Steps = append(op.Steps, durable.StepRecord{Name: f.Step, Result: nil, DoneAt: now})
	op.UpdatedAt = now
	d.db.values = append(d.db.values, value)

	return nil
}

func (d memDomain) StartWith(
	_ context.Context, req durable.StartRequest, value string, after func() error,
) (durable.Operation, error) {
	d.db.mu.Lock()
	defer d.db.mu.Unlock()

	// A copy of the operations stands in for the transaction: a rollback
	// puts it back.
	ops, byKey, nextID := maps.Clone(d.db.ops), maps.Clone(d.db.byKey), d.db.nextID

	op, _, err := d.db.start(req, d.db.now())
	if err == nil {
		err = after()
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
