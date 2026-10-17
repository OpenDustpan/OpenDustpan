package stackhistorytest_test

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stackhistorytest"
)

// TestSuite runs the suite against an in-memory store, so the cases state
// their rules before a SQL adapter implements the port.
func TestSuite(t *testing.T) {
	t.Parallel()
	stackhistorytest.Run(t, func(*testing.T) stackhistorytest.Store { return newMem() })
}

// mem keeps namespaces, rows and cursors in memory.
type mem struct {
	namespaces map[namespace.Name]namespace.Namespace
	cursors    map[stack.ID]int
	rows       []stack.ResourceChange
	mu         sync.Mutex
}

func newMem() *mem {
	return &mem{
		namespaces: map[namespace.Name]namespace.Namespace{},
		cursors:    map[stack.ID]int{},
		rows:       nil,
		mu:         sync.Mutex{},
	}
}

func (m *mem) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *mem) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *mem) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (m *mem) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (m *mem) Ping(context.Context) error { return nil }

func (m *mem) RecordResourceChanges(
	_ context.Context,
	ns namespace.Name,
	b stack.VersionBlame,
	f namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.namespaces[ns]
	if !ok {
		return namespace.ErrNotFound
	}

	if f.Namespace != ns || f.FailoverVersion != stored.FailoverVersion {
		return namespace.ErrFenced
	}

	if m.cursors[b.Stack] >= b.Version {
		return nil
	}

	m.rows = append(m.rows, b.Changes...)
	m.cursors[b.Stack] = b.Version

	return nil
}

func (m *mem) BlameCursor(_ context.Context, id stack.ID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.cursors[id], nil
}

func (m *mem) ResourceHistory(
	_ context.Context,
	id stack.ID,
	urn string,
	before, limit int,
) ([]stack.ResourceChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []stack.ResourceChange

	for n := range m.rows {
		r := m.rows[n]
		if r.Stack == id && r.URN == urn && (before == 0 || r.Version < before) {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b stack.ResourceChange) int { return cmp.Compare(b.Version, a.Version) })

	return out[:min(limit, len(out))], nil
}

func (m *mem) VersionChanges(_ context.Context, id stack.ID, version int) ([]stack.ResourceChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []stack.ResourceChange{}

	for i := range m.rows {
		r := m.rows[i]
		if r.Stack == id && r.Version == version {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b stack.ResourceChange) int { return cmp.Compare(a.URN, b.URN) })

	return out, nil
}
