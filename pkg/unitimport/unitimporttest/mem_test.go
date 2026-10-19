package unitimporttest_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitimport"
	"opendustpan.org/opendustpan/pkg/unitimport/unitimporttest"
)

// memStore is the reference adapter of unitimport.Store and
// unitimport.UnitStore. It runs the suite, so each case of the suite runs
// before any SQL adapter does.
type memStore struct {
	fences  map[namespace.Name]int64
	imports map[string]unitimport.Import
	purged  map[string]bool
	units   map[string][]unitimport.ImportUnit
	ledger  map[string][]unitimport.LedgerRow
	audited []audit.Entry
	mu      sync.Mutex
}

var (
	_ unitimport.Store     = (*memStore)(nil)
	_ unitimport.UnitStore = (*memStore)(nil)
)

func newMem() *memStore {
	return &memStore{
		fences:  map[namespace.Name]int64{},
		imports: map[string]unitimport.Import{},
		purged:  map[string]bool{},
		units:   map[string][]unitimport.ImportUnit{},
		ledger:  map[string][]unitimport.LedgerRow{},
		audited: nil,
		mu:      sync.Mutex{},
	}
}

// key joins the namespace and the import ID.
func key(ns namespace.Name, id string) string { return ns.String() + "\x00" + id }

func (m *memStore) Create(
	_ context.Context, fence namespace.Fence, imp unitimport.Import, e audit.Entry,
) (unitimport.Import, error) {
	var out unitimport.Import

	err := m.write(fence, e, func() error {
		if err := imp.ValidateNew(); err != nil {
			return err
		}

		if imp.Namespace != fence.Namespace {
			return fmt.Errorf("%w: import names another namespace", unitimport.ErrInvalid)
		}

		k := key(imp.Namespace, imp.ID)
		if _, taken := m.imports[k]; taken || m.purged[k] {
			return fmt.Errorf("%w: import %q exists", unitimport.ErrInvalid, imp.ID)
		}

		var noPlan unitimport.Plan

		imp.State, imp.Plan, imp.EndedAt, imp.Reason = unitimport.StatePlanning, noPlan, time.Time{}, ""
		m.imports[k], out = imp, imp

		return nil
	})

	return out, err
}

func (m *memStore) Get(_ context.Context, ns namespace.Name, id string) (unitimport.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.find(ns, id)
}

func (m *memStore) List(_ context.Context, ns namespace.Name, f unitimport.Filter) ([]unitimport.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []unitimport.Import{}

	for k := range m.imports {
		imp := m.imports[k]
		if imp.Namespace == ns && !m.purged[k] && (len(f.States) == 0 || slices.Contains(f.States, imp.State)) {
			out = append(out, imp)
		}
	}

	slices.SortFunc(out, func(a, b unitimport.Import) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID, b.ID)
	})

	return out, nil
}

func (m *memStore) Transition(
	_ context.Context, fence namespace.Fence, id string, mv unitimport.Move, e audit.Entry,
) error {
	return m.write(fence, e, func() error {
		imp, err := m.find(fence.Namespace, id)
		if err != nil {
			return err
		}

		if imp.State != mv.From {
			return fmt.Errorf("%w: import is %q, not %q", unitimport.ErrTransition, imp.State, mv.From)
		}

		if err = unitimport.NextState(mv.From, mv.To); err != nil {
			return fmt.Errorf("next state: %w", err)
		}

		imp.State, imp.Reason = mv.To, mv.Reason
		if mv.To.Ended() {
			imp.EndedAt = e.At
		}

		m.imports[key(fence.Namespace, id)] = imp

		return nil
	})
}

func (m *memStore) SetPlan(
	_ context.Context,
	fence namespace.Fence,
	id string,
	plan unitimport.Plan,
	e audit.Entry,
) error {
	return m.write(fence, e, func() error {
		imp, err := m.find(fence.Namespace, id)
		if err != nil {
			return err
		}

		if imp.State == unitimport.StatePlanning || imp.State == unitimport.StatePlanned {
			imp.Plan = plan
		} else {
			imp.Plan.Reservations = plan.Reservations
		}

		m.imports[key(fence.Namespace, id)] = imp

		return nil
	})
}

func (m *memStore) Purge(_ context.Context, fence namespace.Fence, id string, e audit.Entry) error {
	return m.write(fence, e, func() error {
		imp, err := m.find(fence.Namespace, id)
		if err != nil {
			return err
		}

		if !imp.State.Ended() {
			return fmt.Errorf("%w: import is %q", unitimport.ErrTransition, imp.State)
		}

		k := key(fence.Namespace, id)
		delete(m.imports, k)
		delete(m.units, k)
		m.purged[k] = true

		return nil
	})
}

func (m *memStore) PutUnits(
	_ context.Context, fence namespace.Fence, id string, units []unitimport.ImportUnit, e audit.Entry,
) error {
	return m.write(fence, e, func() error {
		imp, err := m.find(fence.Namespace, id)
		if err != nil {
			return err
		}

		if imp.State != unitimport.StatePlanning && imp.State != unitimport.StatePlanned {
			return fmt.Errorf("%w: import is %q", unitimport.ErrTransition, imp.State)
		}

		if err = unitimport.ValidateUnits(id, units); err != nil {
			return fmt.Errorf("units: %w", err)
		}

		stored := make([]unitimport.ImportUnit, len(units))
		for i := range units {
			stored[i] = units[i]
			stored[i].ImportID = id
		}

		m.units[key(fence.Namespace, id)] = stored

		return nil
	})
}

func (m *memStore) Units(_ context.Context, ns namespace.Name, id string) ([]unitimport.ImportUnit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.find(ns, id); err != nil {
		return nil, err
	}

	return slices.Clone(m.units[key(ns, id)]), nil
}

func (m *memStore) Unit(_ context.Context, ns namespace.Name, id, unitID string) (unitimport.ImportUnit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.find(ns, id); err != nil {
		return unitimport.ImportUnit{}, err
	}

	units := m.units[key(ns, id)]
	for i := range units {
		if units[i].ID == unitID {
			return units[i], nil
		}
	}

	return unitimport.ImportUnit{}, unitimport.ErrNotFound
}

func (m *memStore) TransitionUnit(
	_ context.Context, fence namespace.Fence, key unitimport.UnitKey, mv unitimport.UnitMove, e audit.Entry,
) error {
	return m.changeUnit(fence, key, e, func(u *unitimport.ImportUnit) error {
		if u.State != mv.From {
			return fmt.Errorf("%w: unit is %q, not %q", unitimport.ErrTransition, u.State, mv.From)
		}

		if err := unitimport.NextUnitState(mv.From, mv.To); err != nil {
			return fmt.Errorf("next unit state: %w", err)
		}

		u.State, u.ErrorCode = mv.To, mv.Code

		return nil
	})
}

func (m *memStore) SetCounts(
	_ context.Context, fence namespace.Fence, key unitimport.UnitKey, c unitimport.Counts, e audit.Entry,
) error {
	return m.changeUnit(fence, key, e, func(u *unitimport.ImportUnit) error {
		if err := unitimport.ValidateCounts(c); err != nil {
			return fmt.Errorf("counts: %w", err)
		}

		u.Versions, u.Committed = c.Versions, c.Committed

		return nil
	})
}

func (m *memStore) Ledger(_ context.Context, ns namespace.Name, id, unitID string) ([]unitimport.LedgerRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.find(ns, id); err != nil && !m.purged[key(ns, id)] {
		return nil, err
	}

	rows := []unitimport.LedgerRow{}

	all := m.ledger[key(ns, id)]
	for i := range all {
		if all[i].ImportUnitID == unitID {
			rows = append(rows, all[i])
		}
	}

	slices.SortStableFunc(rows, func(a, b unitimport.LedgerRow) int { return a.CommittedAt.Compare(b.CommittedAt) })

	return rows, nil
}

// write runs fn under the lock after the checks of a fenced, audited write,
// and appends e when fn succeeds.
func (m *memStore) write(fence namespace.Fence, e audit.Entry, fn func() error) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names another namespace", audit.ErrInvalidEntry)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	version, ok := m.fences[fence.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if version != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	if err := fn(); err != nil {
		return err
	}

	m.audited = append(m.audited, e)

	return nil
}

// find returns the import, ErrPurged or ErrNotFound. The caller holds the lock.
func (m *memStore) find(ns namespace.Name, id string) (unitimport.Import, error) {
	if m.purged[key(ns, id)] {
		return unitimport.Import{}, unitimport.ErrPurged
	}

	imp, ok := m.imports[key(ns, id)]
	if !ok {
		return unitimport.Import{}, unitimport.ErrNotFound
	}

	return imp, nil
}

// changeUnit applies fn to the import unit under the write checks.
func (m *memStore) changeUnit(
	fence namespace.Fence, k unitimport.UnitKey, e audit.Entry, fn func(u *unitimport.ImportUnit) error,
) error {
	return m.write(fence, e, func() error {
		if _, err := m.find(fence.Namespace, k.ImportID); err != nil {
			return err
		}

		units := m.units[key(fence.Namespace, k.ImportID)]
		for i := range units {
			if units[i].ID == k.UnitID {
				return fn(&units[i])
			}
		}

		return unitimport.ErrNotFound
	})
}

func memHarness(m *memStore) unitimporttest.Harness {
	return unitimporttest.Harness{
		Store: m,
		Units: m,
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			m.fences[namespace.Name(name)] = 1

			return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
		},
		SeedLedger: func(t *testing.T, ns namespace.Name, row unitimport.LedgerRow) {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			m.ledger[key(ns, row.ImportID)] = append(m.ledger[key(ns, row.ImportID)], row)
		},
		SetTarget: func(t *testing.T, ns namespace.Name, k unitimport.UnitKey, target history.Unit) {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			units := m.units[key(ns, k.ImportID)]
			for i := range units {
				if units[i].ID == k.UnitID {
					units[i].Target = target
				}
			}
		},
		Audited: func(t *testing.T, ns namespace.Name) []audit.Entry {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			var out []audit.Entry

			for i := range m.audited {
				if m.audited[i].Namespace == ns {
					out = append(out, m.audited[i])
				}
			}

			return out
		},
	}
}

func TestMemStore(t *testing.T) {
	t.Parallel()

	unitimporttest.RunStore(t, func(t *testing.T) unitimporttest.Harness {
		t.Helper()

		return memHarness(newMem())
	})
}
