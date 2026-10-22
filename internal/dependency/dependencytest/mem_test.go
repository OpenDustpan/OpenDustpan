package dependencytest_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/dependency"
	"opendustpan.org/opendustpan/internal/dependency/dependencytest"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestSuite runs the suite against an in-memory store, so the cases state
// their rules before a SQL adapter implements the port.
func TestSuite(t *testing.T) {
	t.Parallel()
	dependencytest.Run(t, func(*testing.T) dependencytest.Store { return newMem() })
}

// mem keeps namespaces and edges in memory.
type mem struct {
	namespaces map[namespace.Name]namespace.Namespace
	edges      []dependency.Edge
	mu         sync.Mutex
}

func newMem() *mem {
	return &mem{namespaces: map[namespace.Name]namespace.Namespace{}, edges: nil, mu: sync.Mutex{}}
}

func (m *mem) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

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

// fenced answers namespace.ErrFenced for a stale fence of ns. The caller
// holds the lock.
func (m *mem) RecordEdge(_ context.Context, e dependency.Edge, f namespace.Fence) error {
	if err := e.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(e.Source.Namespace, f); err != nil {
		return err
	}

	for i := range m.edges {
		stored := m.edges[i]
		if !dependency.SameEdge(stored, e) {
			continue
		}

		if e.LastSeen.After(stored.LastSeen) {
			stored.LastSeen = e.LastSeen
		}

		stored.Reads += e.Reads
		stored.Ambiguous = stored.Ambiguous || e.Ambiguous
		m.edges[i] = stored

		return nil
	}

	m.edges = append(m.edges, e)

	return nil
}

// newestFirst orders edges by the last time seen, newest first.
func newestFirst(a, b dependency.Edge) int {
	return b.LastSeen.Compare(a.LastSeen)
}

// selectEdges returns the edges that keep matches, seen at or after since.
func (m *mem) Dependents(_ context.Context, source history.Unit, since time.Time) ([]dependency.Edge, error) {
	return m.selectEdges(since, func(e dependency.Edge) bool { return e.Source == source }), nil
}

func (m *mem) Sources(_ context.Context, dependent history.Unit, since time.Time) ([]dependency.Edge, error) {
	return m.selectEdges(
		since,
		func(e dependency.Edge) bool { return e.Dependent != nil && *e.Dependent == dependent },
	), nil
}

func (m *mem) DropUnit(_ context.Context, unit history.Unit, f namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(unit.Namespace, f); err != nil {
		return err
	}

	m.edges = slices.DeleteFunc(m.edges, func(e dependency.Edge) bool {
		return e.Source == unit ||
			(e.Dependent != nil && *e.Dependent == unit && e.Source.Namespace == unit.Namespace)
	})

	return nil
}

func (m *mem) fenced(ns namespace.Name, f namespace.Fence) error {
	stored, ok := m.namespaces[ns]
	if !ok {
		return namespace.ErrNotFound
	}

	if f.Namespace != ns || f.FailoverVersion != stored.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

func (m *mem) selectEdges(since time.Time, keep func(dependency.Edge) bool) []dependency.Edge {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []dependency.Edge

	for i := range m.edges {
		e := m.edges[i]
		if keep(e) && !e.LastSeen.Before(since) {
			out = append(out, e)
		}
	}

	slices.SortStableFunc(out, newestFirst)

	return out
}
