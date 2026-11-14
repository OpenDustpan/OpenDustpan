// Package unmanagedtest is the conformance suite of the sweep store port of
// package unmanaged.
package unmanagedtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// Pool is the test pool of each namespace: an operator pool with the sweep
// purpose.
const Pool = "sweepers"

// JobPool is a pool with the jobs purpose. A target refuses it.
const JobPool = "builders"

// poolOf is one runner pool of the harness.
type poolOf struct {
	id       string
	ns       namespace.Name
	purpose  string
	operator bool
}

// Mem is the reference adapter of the sweep store. It runs the suite, so each
// case runs before any SQL adapter exists.
type Mem struct {
	mu      sync.Mutex
	fences  map[namespace.Name]int64
	gone    map[namespace.Name]bool
	pools   map[string]*poolOf
	targets map[namespace.Name]map[string]unmanaged.Target
	sweeps  map[namespace.Name][]*unmanaged.Sweep
	next    int
	// failNext, when true, fails the next write after its fence check, so
	// that a test proves the write rolled back.
	failNext bool
}

// NewMem returns an empty reference adapter.
func NewMem() *Mem {
	return &Mem{
		fences:  map[namespace.Name]int64{},
		gone:    map[namespace.Name]bool{},
		pools:   map[string]*poolOf{},
		targets: map[namespace.Name]map[string]unmanaged.Target{},
		sweeps:  map[namespace.Name][]*unmanaged.Sweep{},
	}
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// Harness is one fresh, empty store for one subtest.
type Harness struct {
	Store HarnessStore
	// Namespace makes the namespace name, a sweep pool and a job pool in it,
	// and returns the current fence of the namespace.
	Namespace func(t *testing.T, name string) namespace.Fence
	// SetPoolPurpose changes the purpose of the pool id.
	SetPoolPurpose func(t *testing.T, id, purpose string)
	// DeletePool deletes the pool id.
	DeletePool func(t *testing.T, id string)
	// BumpFence moves the live failover version of name, so the next write
	// with the old fence is stale.
	BumpFence func(t *testing.T, name string)
	// DeleteNamespace deletes the namespace name, so the next write to it is
	// fenced.
	DeleteNamespace func(t *testing.T, name string)
	// FailNext fails the next write of the store after its fence check.
	FailNext func(t *testing.T)
	// PoolID returns the ID of the sweep pool of the namespace name.
	PoolID func(t *testing.T, name string) string
	// JobPoolID returns the ID of the job pool of the namespace name.
	JobPoolID func(t *testing.T, name string) string
}

// HarnessStore is what the conformance cases need of the store: the port
// plus the error hook of the rollback case.
type HarnessStore interface {
	unmanaged.SweepStore
	setFailNext()
}

// NewHarness returns a factory whose harness holds one Mem.
func NewHarness() Factory {
	return func(t *testing.T) Harness {
		t.Helper()

		m := NewMem()

		return Harness{
			Store: m,
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				ns := namespace.Name(name)
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[ns] = 1
				delete(m.gone, ns)
				m.pools["pool-"+name+"-sweep"] = &poolOf{
					id: "pool-" + name + "-sweep", ns: ns,
					purpose: unmanaged.PurposeSweeps, operator: true,
				}
				m.pools["pool-"+name+"-job"] = &poolOf{
					id: "pool-" + name + "-job", ns: ns,
					purpose: unmanaged.PurposeJobs, operator: true,
				}

				return namespace.Fence{Namespace: ns, FailoverVersion: 1}
			},
			SetPoolPurpose: func(_ *testing.T, id, purpose string) {
				m.mu.Lock()
				defer m.mu.Unlock()

				if p := m.pools[id]; p != nil {
					p.purpose = purpose
				}
			},
			DeletePool: func(_ *testing.T, id string) {
				m.mu.Lock()
				defer m.mu.Unlock()

				delete(m.pools, id)
			},
			BumpFence: func(_ *testing.T, name string) {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[namespace.Name(name)]++
			},
			DeleteNamespace: func(_ *testing.T, name string) {
				m.mu.Lock()
				defer m.mu.Unlock()

				ns := namespace.Name(name)
				m.gone[ns] = true
			},
			FailNext: func(_ *testing.T) {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.failNext = true
			},
			PoolID: func(_ *testing.T, name string) string {
				return "pool-" + name + "-sweep"
			},
			JobPoolID: func(_ *testing.T, name string) string {
				return "pool-" + name + "-job"
			},
		}
	}
}

func (m *Mem) setFailNext() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.failNext = true
}

// fenced reports whether fence is the live fence of its namespace. The caller
// holds m.mu.
func (m *Mem) fenced(fence namespace.Fence) error {
	if m.gone[fence.Namespace] {
		return namespace.ErrFenced
	}

	v, ok := m.fences[fence.Namespace]
	if !ok || v != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// takeFail returns true when the next write must fail after its fence check.
// The caller holds m.mu.
func (m *Mem) takeFail() bool {
	fail := m.failNext
	m.failNext = false

	return fail
}

// checkPool reports whether pool id is an operator pool with the sweep
// purpose of the namespace ns. The caller holds m.mu.
func (m *Mem) checkPool(ns namespace.Name, id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty pool", unmanaged.ErrInvalidTarget)
	}

	p, ok := m.pools[id]
	if !ok {
		return fmt.Errorf("%w: unknown pool %q", unmanaged.ErrInvalidTarget, id)
	}

	// Defense in depth: the pool belongs to the namespace of the target;
	// a pool of another namespace never passes, even when its purpose is
	// sweeps. The check of the purpose below does not rely on this check.
	if p.ns != ns {
		return fmt.Errorf("%w: pool %q is of another namespace", unmanaged.ErrInvalidTarget, id)
	}

	if !p.operator || p.purpose != unmanaged.PurposeSweeps {
		return fmt.Errorf("%w: pool %q is not an operator sweep pool", unmanaged.ErrInvalidTarget, id)
	}

	return nil
}

// CreateTarget implements unmanaged.SweepStore.
func (m *Mem) CreateTarget(_ context.Context, t unmanaged.Target, fence namespace.Fence) error {
	if err := t.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if m.takeFail() {
		return errors.New("injected failure after the fence check")
	}

	if err := m.checkPool(t.Namespace, t.PoolID); err != nil {
		return err
	}

	if m.targets[t.Namespace] == nil {
		m.targets[t.Namespace] = map[string]unmanaged.Target{}
	}

	if _, ok := m.targets[t.Namespace][t.Name]; ok {
		return unmanaged.ErrAlreadyExists
	}

	if len(m.targets[t.Namespace]) >= unmanaged.MaxTargets {
		return unmanaged.ErrLimit
	}

	m.targets[t.Namespace][t.Name] = t

	return nil
}

// UpdateTarget implements unmanaged.SweepStore.
func (m *Mem) UpdateTarget(_ context.Context, t unmanaged.Target, fence namespace.Fence) error {
	if err := t.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if m.takeFail() {
		return errors.New("injected failure after the fence check")
	}

	if _, ok := m.targets[t.Namespace][t.Name]; !ok {
		return unmanaged.ErrNotFound
	}

	if err := m.checkPool(t.Namespace, t.PoolID); err != nil {
		return err
	}

	m.targets[t.Namespace][t.Name] = t

	return nil
}

// DeleteTarget implements unmanaged.SweepStore.
func (m *Mem) DeleteTarget(
	_ context.Context, ns namespace.Name, name string, fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if m.takeFail() {
		return errors.New("injected failure after the fence check")
	}

	if _, ok := m.targets[ns][name]; !ok {
		return unmanaged.ErrNotFound
	}

	for _, s := range m.sweeps[ns] {
		if s.Target == name && !s.Status.Terminal() {
			return unmanaged.ErrScanBusy
		}
	}

	delete(m.targets[ns], name)

	return nil
}

// Targets implements unmanaged.SweepStore.
func (m *Mem) Targets(_ context.Context, ns namespace.Name) ([]unmanaged.Target, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.targets[ns]))
	for name := range m.targets[ns] {
		names = append(names, name)
	}

	slices.Sort(names)

	out := make([]unmanaged.Target, 0, len(names))

	for _, name := range names {
		out = append(out, m.targets[ns][name])
	}

	return out, nil
}

// StartSweep implements unmanaged.SweepStore.
func (m *Mem) StartSweep(_ context.Context, s unmanaged.Sweep, fence namespace.Fence) (unmanaged.Sweep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return unmanaged.Sweep{}, err
	}

	if m.takeFail() {
		return unmanaged.Sweep{}, errors.New("injected failure after the fence check")
	}

	if _, ok := m.targets[s.Namespace][s.Target]; !ok {
		return unmanaged.Sweep{}, unmanaged.ErrNotFound
	}

	// Defense in depth: the start re-reads the pool of the target by its
	// stable ID; a pool that changed purpose or was deleted after the save
	// cannot start a sweep. The create and update checks do not replace it.
	t := m.targets[s.Namespace][s.Target]
	if err := m.checkPool(s.Namespace, t.PoolID); err != nil {
		return unmanaged.Sweep{}, err
	}

	for _, x := range m.sweeps[s.Namespace] {
		if x.Target == s.Target && !x.Status.Terminal() {
			return unmanaged.Sweep{}, unmanaged.ErrScanBusy
		}
	}

	m.next++
	s.ID = fmt.Sprintf("swp-%d", m.next)
	s.Status = unmanaged.SweepQueued
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}

	stored := s
	m.sweeps[s.Namespace] = append([]*unmanaged.Sweep{&stored}, m.sweeps[s.Namespace]...)

	return s, nil
}

// AdvanceSweep implements unmanaged.SweepStore.
func (m *Mem) AdvanceSweep(_ context.Context, a unmanaged.SweepAdvance, fence namespace.Fence) (unmanaged.Sweep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return unmanaged.Sweep{}, err
	}

	if m.takeFail() {
		return unmanaged.Sweep{}, errors.New("injected failure after the fence check")
	}

	var s *unmanaged.Sweep

	for _, x := range m.sweeps[a.Namespace] {
		if x.ID == a.ID {
			s = x
		}
	}

	if s == nil {
		return unmanaged.Sweep{}, unmanaged.ErrNotFound
	}

	to, err := unmanaged.NextSweep(s.Status, a.Event)
	if err != nil {
		return unmanaged.Sweep{}, err
	}

	s.Status = to
	if a.Reason != "" {
		s.Reason = a.Reason
	}

	if to.Terminal() {
		s.EndedAt = time.Now()
	}

	return *s, nil
}

// GetSweep implements unmanaged.SweepStore.
func (m *Mem) GetSweep(_ context.Context, ns namespace.Name, id string) (unmanaged.Sweep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sweeps[ns] {
		if s.ID == id {
			return *s, nil
		}
	}

	return unmanaged.Sweep{}, unmanaged.ErrNotFound
}

// ListSweeps implements unmanaged.SweepStore.
func (m *Mem) ListSweeps(
	_ context.Context, ns namespace.Name, target, after string, limit int,
) ([]unmanaged.Sweep, error) {
	if limit < 1 || limit > unmanaged.MaxPage {
		return nil, fmt.Errorf("%w: limit %d", unmanaged.ErrLimit, limit)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []unmanaged.Sweep

	started := after == ""

	for _, s := range m.sweeps[ns] {
		if !started {
			started = s.ID == after

			continue
		}

		if target == "" || s.Target == target {
			out = append(out, *s)
		}

		if len(out) == limit {
			break
		}
	}

	if after != "" && !started {
		return nil, unmanaged.ErrNotFound
	}

	return out, nil
}

func at() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

func targetOf(ns, name, pool string) unmanaged.Target {
	return unmanaged.Target{
		Namespace: namespace.Name(ns), Name: name, PoolID: pool,
		Every: time.Hour, Timeout: time.Minute, Enabled: true,
	}
}

func sweepOf(ns, target string) unmanaged.Sweep {
	return unmanaged.Sweep{
		Namespace: namespace.Name(ns), Target: target,
		CreatedAt: at(), Status: "",
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", what, err, want)
	}
}
