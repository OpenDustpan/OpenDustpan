package inventorytest_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/inventory/inventorytest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memStore holds the namespaces and the change log in memory. It numbers the
// rows of each namespace under one lock, as the SQL adapters number them in
// one transaction.
type memStore struct {
	namespaces map[namespace.Name]namespace.Namespace
	heads      map[namespace.ID]int64
	rows       map[namespace.ID][]inventory.Change
	mu         sync.Mutex
}

var _ inventorytest.Fixture = (*memStore)(nil)

func newMemStore() *memStore {
	return &memStore{
		namespaces: map[namespace.Name]namespace.Namespace{},
		heads:      map[namespace.ID]int64{},
		rows:       map[namespace.ID][]inventory.Change{},
		mu:         sync.Mutex{},
	}
}

func (m *memStore) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, taken := m.namespaces[ns.Name]; taken {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (m *memStore) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]namespace.Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		out = append(out, ns)
	}

	return out, nil
}

func (m *memStore) Ping(context.Context) error { return nil }

func (m *memStore) PurgeNamespace(_ context.Context, name namespace.Name) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.namespaces, name)

	return nil
}

func (m *memStore) AppendChange(
	_ context.Context, c inventory.Change, fence namespace.Fence,
) (inventory.Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[fence.Namespace]
	if !ok {
		return inventory.Change{}, namespace.ErrNotFound
	}

	if ns.FailoverVersion != fence.FailoverVersion {
		return inventory.Change{}, namespace.ErrFenced
	}

	m.heads[ns.ID]++
	c.Seq = m.heads[ns.ID]
	c.Namespace = fence.Namespace
	m.rows[ns.ID] = append(m.rows[ns.ID], c)

	return c, nil
}

func (m *memStore) ListChanges(
	_ context.Context, name namespace.Name, until time.Time, after *inventory.ChangeCursor, limit int,
) ([]inventory.Change, error) {
	out := []inventory.Change{}

	rows := m.snapshot(name)
	for i := range rows {
		if rows[i].At.After(until) || (after != nil && rows[i].Seq <= after.Seq) {
			continue
		}

		if len(out) == limit {
			break
		}

		out = append(out, rows[i])
	}

	return out, nil
}

func (m *memStore) UnitsAt(_ context.Context, name namespace.Name, at time.Time) ([]inventory.Entry, error) {
	return inventory.ReplayAt(m.snapshot(name), at), nil
}

func (m *memStore) ViewAt(_ context.Context, name namespace.Name, at time.Time) (inventory.UnitsView, error) {
	return inventory.ReplayView(m.snapshot(name), at), nil
}

func (m *memStore) UnitNames(
	_ context.Context, name namespace.Name, tool, unitID string, at time.Time,
) (inventory.Names, error) {
	return inventory.NamesAt(m.snapshot(name), tool, unitID, at), nil
}

// snapshot returns the rows of the namespace called name now.
func (m *memStore) snapshot(name namespace.Name) []inventory.Change {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return nil
	}

	rows := slices.Clone(m.rows[ns.ID])
	for i := range rows {
		rows[i].Namespace = name
	}

	return rows
}

func TestHistoryConformance(t *testing.T) {
	t.Parallel()

	inventorytest.RunHistory(t, func(*testing.T) inventorytest.Fixture { return newMemStore() })
}
