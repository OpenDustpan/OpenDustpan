package costtest_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/cost"
	"opendustpan.org/opendustpan/pkg/cost/costtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	costtest.Run(t, func(*testing.T) costtest.Harness {
		db := &memStore{
			Domain:    opsmem.New(),
			estimates: map[cost.Subject]estimateRow{},
			jobs:      map[string]bool{},
			budgets:   map[string]cost.Budget{},
			statuses:  map[statusKey]statusRow{},
		}

		return costtest.Harness{Estimates: db, Budgets: db, Domain: db}
	})
}

type estimateRow struct {
	ns namespace.Name
	e  cost.Estimate
}

type statusKey struct {
	budget string
	period cost.Period
}

type statusRow struct {
	st      cost.Status
	version int64
}

// memStore is the reference adapter of cost.EstimateStore and
// cost.BudgetStore in memory.
type memStore struct {
	*opsmem.Domain

	estimates map[cost.Subject]estimateRow
	jobs      map[string]bool
	budgets   map[string]cost.Budget
	statuses  map[statusKey]statusRow
}

func (m *memStore) SetJobOpen(_ context.Context, ns namespace.Name, id string, open bool) error {
	m.Read(func() { m.jobs[ns.String()+"/"+id] = open })

	return nil
}

// scoped translates opsmem.ErrScope into the sentinel of the port.
func scoped(err, sentinel error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%w: %w", sentinel, err)
	}

	return err
}

func (m *memStore) PutEstimate(_ context.Context, est cost.Estimate, fence namespace.Fence, entry *audit.Entry) error {
	if err := est.Validate(); err != nil {
		return err
	}

	var entries []audit.Entry
	if entry != nil {
		entries = []audit.Entry{*entry}
	}

	return scoped(m.Write(fence, opsmem.Scope(est.Namespace), entries, func() error {
		if old, ok := m.estimates[est.Subject]; ok && old.ns != est.Namespace {
			return fmt.Errorf(
				"estimate %s: %w: the subject has an estimate in another namespace",
				est.ID,
				cost.ErrInvalid,
			)
		}

		m.estimates[est.Subject] = estimateRow{ns: est.Namespace, e: est}

		return nil
	}), cost.ErrInvalid)
}

func (m *memStore) GetEstimate(_ context.Context, s cost.Subject) (cost.Estimate, error) {
	var (
		r  estimateRow
		ok bool
	)

	m.Read(func() { r, ok = m.estimates[s] })

	if !ok {
		return cost.Estimate{}, fmt.Errorf("estimate of %s: %w", s.Job, cost.ErrNotFound)
	}

	return r.e, nil
}

func (m *memStore) PendingEstimates(_ context.Context, ns namespace.Name) ([]cost.Estimate, error) {
	var out []cost.Estimate

	m.Read(func() {
		for s := range m.estimates {
			if r := m.estimates[s]; r.ns == ns && s.Job != "" && m.jobs[ns.String()+"/"+s.Job] {
				out = append(out, r.e)
			}
		}
	})

	return out, nil
}

func (m *memStore) CreateBudget(_ context.Context, b cost.Budget, fence namespace.Fence, e audit.Entry) error {
	if err := b.Validate(); err != nil {
		return err
	}

	return scoped(m.Write(fence, opsmem.Scope(b.Namespace), []audit.Entry{e}, func() error {
		if _, ok := m.budgets[b.ID]; ok {
			return fmt.Errorf("create %s: %w", b.ID, cost.ErrExists)
		}

		b.Version = 1
		m.budgets[b.ID] = b

		return nil
	}), cost.ErrInvalid)
}

func (m *memStore) UpdateBudget(_ context.Context, b cost.Budget, fence namespace.Fence, e audit.Entry) error {
	if err := b.Validate(); err != nil {
		return err
	}

	return scoped(m.Write(fence, m.budgetScope(b.ID), []audit.Entry{e}, func() error {
		old := m.budgets[b.ID]
		if old.Namespace != b.Namespace {
			return fmt.Errorf("update %s: %w: the namespace of a budget does not change", b.ID, cost.ErrInvalid)
		}

		if old.Version != b.Version {
			return fmt.Errorf("update %s: %w", b.ID, cost.ErrConflict)
		}

		b.Version++
		m.budgets[b.ID] = b

		return nil
	}), cost.ErrNotFound)
}

func (m *memStore) DeleteBudget(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return scoped(m.Write(fence, m.budgetScope(id), []audit.Entry{e}, func() error {
		delete(m.budgets, id)
		maps.DeleteFunc(m.statuses, func(k statusKey, _ statusRow) bool { return k.budget == id })

		return nil
	}), cost.ErrNotFound)
}

func (m *memStore) Budgets(_ context.Context, ns namespace.Name) ([]cost.Budget, error) {
	var out []cost.Budget

	m.Read(func() {
		for _, id := range slices.Sorted(maps.Keys(m.budgets)) {
			if b := m.budgets[id]; b.Namespace == ns {
				out = append(out, b)
			}
		}
	})

	return out, nil
}

func (m *memStore) SaveStatus(_ context.Context, st cost.Status, version int64, fence namespace.Fence) error {
	return scoped(m.Write(fence, m.budgetScope(st.Budget), nil, func() error {
		k := statusKey{budget: st.Budget, period: st.Period}
		if m.statuses[k].version != version {
			return fmt.Errorf("save status of %s: %w", st.Budget, cost.ErrConflict)
		}

		st.Crossed, st.Warned = slices.Clone(st.Crossed), slices.Clone(st.Warned)
		m.statuses[k] = statusRow{st: st, version: version + 1}

		return nil
	}), cost.ErrNotFound)
}

func (m *memStore) LastStatus(_ context.Context, id string, p cost.Period) (cost.Status, int64, error) {
	var (
		r  statusRow
		ok bool
	)

	m.Read(func() { r, ok = m.statuses[statusKey{budget: id, period: p}] })

	if !ok {
		return cost.Status{}, 0, fmt.Errorf("status of %s: %w", id, cost.ErrNotFound)
	}

	r.st.Crossed, r.st.Warned = slices.Clone(r.st.Crossed), slices.Clone(r.st.Warned)

	return r.st, r.version, nil
}

// budgetScope returns a scope function that finds the namespace of the
// stored budget id, under the mutex of the write.
func (m *memStore) budgetScope(id string) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		b, ok := m.budgets[id]
		if !ok {
			return "", fmt.Errorf("budget %s: %w", id, cost.ErrNotFound)
		}

		return b.Namespace, nil
	}
}
