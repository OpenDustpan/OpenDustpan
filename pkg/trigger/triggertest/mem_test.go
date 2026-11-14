package triggertest_test

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

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trigger/triggertest"
)

type key struct {
	ns namespace.Name
	id trigger.ID
}

// errClockNotSet is the setup error of a fake whose clock no test injected.
var errClockNotSet = errors.New("the clock of the store is not set")

// mem is the reference adapter of Store and Dispatcher. It runs the suite, so
// each case runs before any SQL adapter exists. It keeps the audit log that a
// SQL adapter writes in the same transaction.
type mem struct {
	fences   map[namespace.Name]int64
	triggers map[key]trigger.Trigger
	next     map[key]time.Time
	audited  map[namespace.Name]int
	// now is the clock of the store. The harness injects a fixed clock, and
	// Harness.Now reads the same one. A mem without a clock fails its writes.
	now func() time.Time
	mu  sync.Mutex
}

func newMem() *mem {
	return &mem{
		fences:   map[namespace.Name]int64{},
		triggers: map[key]trigger.Trigger{},
		next:     map[key]time.Time{},
		audited:  map[namespace.Name]int{},
		now:      nil,
		mu:       sync.Mutex{},
	}
}

// Fence implements trigger.Fences.
func (m *mem) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.fences[ns]
	if !ok {
		return namespace.Fence{}, namespace.ErrNotFound
	}

	return namespace.Fence{Namespace: ns, FailoverVersion: v}, nil
}

func (m *mem) Put(_ context.Context, t trigger.Trigger, fence namespace.Fence, e audit.Entry) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	if err := checkEntry(e, t.Unit.Namespace); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, t.Unit.Namespace); err != nil {
		return err
	}

	k := key{ns: t.Unit.Namespace, id: t.ID}

	if old, ok := m.triggers[k]; ok && (old.Unit != t.Unit || old.Kind != t.Kind) {
		return fmt.Errorf("%w: a trigger keeps its unit and its kind", trigger.ErrInvalid)
	}

	// A stored row that fails Validate starts no plan, so it is no edge.
	valid := slices.DeleteFunc(m.inNamespace(k.ns), func(o trigger.Trigger) bool { return o.Validate() != nil })

	if err := trigger.CheckCycle(valid, t); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	var prev *trigger.Trigger

	if old, found := m.triggers[k]; found {
		prev = &old
	}

	now, err := m.nowTime()
	if err != nil {
		return fmt.Errorf("put: %w", err)
	}

	slot, err := trigger.PutSlot(t, now, prev, m.next[k])
	if err != nil {
		return fmt.Errorf("put: %w", err)
	}

	m.triggers[k] = t
	m.next[k] = slot
	m.audited[k.ns]++

	return nil
}

func (m *mem) Get(_ context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := id.Validate(); err != nil {
		return trigger.Trigger{}, fmt.Errorf("get: %w", err)
	}

	if _, err := namespace.ParseName(string(ns)); err != nil {
		return trigger.Trigger{}, fmt.Errorf("get: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.triggers[key{ns: ns, id: id}]
	if !ok {
		return trigger.Trigger{}, trigger.ErrNotFound
	}

	if err := t.Validate(); err != nil {
		return trigger.Trigger{}, fmt.Errorf("get: %w", err)
	}

	return t, nil
}

func (m *mem) Stored(_ context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := id.Validate(); err != nil {
		return trigger.Trigger{}, fmt.Errorf("stored: %w", err)
	}

	if _, err := namespace.ParseName(string(ns)); err != nil {
		return trigger.Trigger{}, fmt.Errorf("stored: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.triggers[key{ns: ns, id: id}]
	if !ok {
		return trigger.Trigger{}, trigger.ErrNotFound
	}

	return t, nil
}

func (m *mem) List(_ context.Context, unit graph.Unit) ([]trigger.Trigger, error) {
	if err := unit.Validate(); err != nil {
		return nil, fmt.Errorf("list: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []trigger.Trigger

	all := m.inNamespace(unit.Namespace)
	for i := range all {
		if all[i].Unit == unit {
			if err := all[i].Validate(); err != nil {
				return nil, fmt.Errorf("list: %w", err)
			}

			out = append(out, all[i])
		}
	}

	return out, nil
}

func (m *mem) Followers(_ context.Context, source graph.Unit) ([]trigger.Trigger, error) {
	if err := source.Validate(); err != nil {
		return nil, fmt.Errorf("followers: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []trigger.Trigger

	all := m.inNamespace(source.Namespace)
	for i := range all {
		if t := &all[i]; t.Kind == trigger.KindFollows && t.Enabled && t.Source == source {
			out = append(out, *t)
		}
	}

	return out, nil
}

func (m *mem) Delete(_ context.Context, ns namespace.Name, id trigger.ID, fence namespace.Fence, e audit.Entry) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	if _, err := namespace.ParseName(string(ns)); err != nil {
		return fmt.Errorf("delete: %w: %w", trigger.ErrInvalid, err)
	}

	if err := checkEntry(e, ns); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, ns); err != nil {
		return err
	}

	k := key{ns: ns, id: id}
	if _, ok := m.triggers[k]; !ok {
		return trigger.ErrNotFound
	}

	delete(m.triggers, k)
	delete(m.next, k)
	m.audited[ns]++

	return nil
}

// NextSlot implements trigger.Store.
func (m *mem) NextSlot(_ context.Context, ns namespace.Name, id trigger.ID) (time.Time, error) {
	if err := id.Validate(); err != nil {
		return time.Time{}, fmt.Errorf("next slot: %w", err)
	}

	if _, err := namespace.ParseName(string(ns)); err != nil {
		return time.Time{}, fmt.Errorf("next slot: %w: %w", trigger.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key{ns: ns, id: id}
	if _, ok := m.triggers[k]; !ok {
		return time.Time{}, trigger.ErrNotFound
	}

	return m.next[k], nil
}

// ClaimDue implements trigger.Store.
func (m *mem) ClaimDue(_ context.Context, c trigger.Claim, fence namespace.Fence, e audit.Entry) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	if err := checkEntry(e, c.Namespace); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence, c.Namespace); err != nil {
		return err
	}

	k := key{ns: c.Namespace, id: c.Trigger}

	t, ok := m.triggers[k]
	if !ok {
		return trigger.ErrNotFound
	}

	now, err := m.nowTime()
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	next, err := trigger.CheckClaim(c, t, m.next[k], now)
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	m.next[k] = next
	m.audited[c.Namespace]++

	return nil
}

// nowTime returns the injected clock of the fake. A mem without a clock is a
// setup error: its writes fail instead of reading the clock of the process.
func (m *mem) nowTime() (time.Time, error) {
	if m.now == nil {
		return time.Time{}, errClockNotSet
	}

	return m.now().UTC(), nil
}

// checkEntry refuses an audit entry that is not valid or names another
// namespace than ns.
func checkEntry(e audit.Entry, ns namespace.Name) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != ns {
		return fmt.Errorf("%w: the entry names namespace %q, not %q", audit.ErrInvalidEntry, e.Namespace, ns)
	}

	return nil
}

func (m *mem) checkFence(f namespace.Fence, ns namespace.Name) error {
	if f.Namespace != ns || m.fences[ns] != f.FailoverVersion {
		return fmt.Errorf("fence of %q: %w", ns, namespace.ErrFenced)
	}

	return nil
}

// inNamespace returns the triggers of ns.
func (m *mem) inNamespace(ns namespace.Name) []trigger.Trigger {
	var out []trigger.Trigger

	for _, k := range slices.SortedFunc(maps.Keys(m.triggers), func(a, b key) int { return strings.Compare(string(a.id), string(b.id)) }) {
		if k.ns == ns {
			out = append(out, m.triggers[k])
		}
	}

	return out
}

// storeRaw writes t without Validate and without the cycle check.
func (m *mem) storeRaw(t trigger.Trigger) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.triggers[key{ns: t.Unit.Namespace, id: t.ID}] = t
}

func TestConformance(t *testing.T) {
	t.Parallel()

	triggertest.Run(t, func(_ *testing.T, starter trigger.Starter) triggertest.Harness {
		clock := time.UnixMilli(1_700_000_000_000).UTC()

		m := newMem()
		m.now = func() time.Time { return clock }

		doubles := triggertest.NewDoubles()

		dispatcher, err := doubles.Dispatcher(m, starter, m)
		if err != nil {
			t.Fatalf("dispatcher: %v", err)
		}

		return triggertest.Harness{
			Store:      m,
			Dispatcher: dispatcher,
			Doubles:    doubles,
			Now:        func(_ *testing.T) time.Time { return clock },
			Namespace: func(_ *testing.T, name string) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				m.fences[namespace.Name(name)] = 1

				return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
			},
			StoreRaw: func(_ *testing.T, tr trigger.Trigger) { m.storeRaw(tr) },
			SetNextSlot: func(t *testing.T, ns namespace.Name, id trigger.ID, slot time.Time) {
				t.Helper()

				m.mu.Lock()
				defer m.mu.Unlock()

				m.next[key{ns: ns, id: id}] = slot
			},
			Audited: func(_ *testing.T, ns string) int {
				m.mu.Lock()
				defer m.mu.Unlock()

				return m.audited[namespace.Name(ns)]
			},
		}
	})
}
