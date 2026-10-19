package workspacehistorytest_test

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacehistorytest"
)

// TestSuite runs the suite against an in-memory store, so the cases state
// their rules before a SQL adapter implements the port.
func TestSuite(t *testing.T) {
	t.Parallel()
	workspacehistorytest.Run(t, func(*testing.T) workspacehistorytest.Store { return newMem() })
}

// mem keeps namespaces, rows, opaque and lineage marks and cursors in memory.
type mem struct {
	namespaces map[namespace.Name]namespace.Namespace
	cursors    map[workspace.ID]int64
	owners     map[workspace.ID]namespace.Name
	opaque     map[itemKey]bool
	lineage    map[itemKey]bool
	ordinals   map[itemKey]int64
	rows       []workspace.ResourceChange
	mu         sync.Mutex
}

// itemKey names one state version of one workspace.
type itemKey struct {
	workspace    workspace.ID
	stateVersion workspace.StateVersionID
}

func newMem() *mem {
	return &mem{
		namespaces: map[namespace.Name]namespace.Namespace{},
		cursors:    map[workspace.ID]int64{},
		owners:     map[workspace.ID]namespace.Name{},
		opaque:     map[itemKey]bool{},
		lineage:    map[itemKey]bool{},
		ordinals:   map[itemKey]int64{},
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

func (m *mem) CreateWorkspace(_ context.Context, w workspace.Workspace, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.owners[w.ID] = w.Ref.Namespace

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
	b workspace.ItemBlame,
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

	if err := b.Validate(); err != nil {
		return err
	}

	if owner, ok := m.owners[b.Workspace]; !ok || owner != ns {
		return workspace.ErrNotFound
	}

	if m.cursors[b.Workspace] >= b.Ordinal {
		return nil
	}

	key := itemKey{workspace: b.Workspace, stateVersion: b.StateVersion}
	m.rows = append(m.rows, b.Changes...)
	m.ordinals[key] = b.Ordinal
	m.opaque[key] = b.Opaque
	m.lineage[key] = b.LineageChanged
	m.cursors[b.Workspace] = b.Ordinal

	return nil
}

func (m *mem) BlameCursor(_ context.Context, id workspace.ID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.cursors[id], nil
}

func (m *mem) ResourceHistory(
	_ context.Context, id workspace.ID, address string, before int64, limit int,
) ([]workspace.ResourceChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []workspace.ResourceChange

	for n := range m.rows {
		r := m.rows[n]
		if r.Workspace == id && r.Address == address && (before == 0 || r.Ordinal < before) {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b workspace.ResourceChange) int { return cmp.Compare(b.Ordinal, a.Ordinal) })

	return out[:min(limit, len(out))], nil
}

func (m *mem) ItemChanges(
	_ context.Context,
	id workspace.ID,
	sv workspace.StateVersionID,
) (workspace.ItemBlame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := itemKey{workspace: id, stateVersion: sv}
	b := workspace.ItemBlame{
		Workspace:      id,
		StateVersion:   sv,
		Changes:        []workspace.ResourceChange{},
		Ordinal:        m.ordinals[key],
		Opaque:         m.opaque[key],
		LineageChanged: m.lineage[key],
	}

	for i := range m.rows {
		r := m.rows[i]
		if r.Workspace == id && r.StateVersion == sv {
			b.Changes = append(b.Changes, r)
		}
	}

	slices.SortFunc(b.Changes, func(a, c workspace.ResourceChange) int { return cmp.Compare(a.Address, c.Address) })

	return b, nil
}
