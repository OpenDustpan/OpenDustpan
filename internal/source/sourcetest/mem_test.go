package sourcetest_test

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/internal/source/sourcetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memNamespace is one namespace of memFilters: its failover version, its
// stored filter text and its audit entries.
type memNamespace struct {
	raw     string
	audited []audit.Entry
	version int64
}

// memFilters is a source.Filters in memory. It proves that the cases hold for
// a correct adapter.
type memFilters struct {
	spaces map[namespace.Name]*memNamespace
	mu     sync.Mutex
}

var _ source.Filters = (*memFilters)(nil)

func newMemFilters() *memFilters {
	return &memFilters{spaces: map[namespace.Name]*memNamespace{}, mu: sync.Mutex{}}
}

// Filter implements source.Filters.
func (m *memFilters) Filter(_ context.Context, ns namespace.Name) (source.Filter, error) {
	var none source.Filter

	if _, err := namespace.ParseName(ns.String()); err != nil {
		return none, source.ErrInvalid
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	space, ok := m.spaces[ns]
	if !ok {
		return none, namespace.ErrNotFound
	}

	return source.DecodeFilter(space.raw)
}

// SetFilter implements source.Filters.
func (m *memFilters) SetFilter(
	_ context.Context, ns namespace.Name, f source.Filter, fence namespace.Fence, e audit.Entry,
) error {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return source.ErrInvalid
	}

	text, err := source.EncodeFilter(f)
	if err != nil {
		return err
	}

	if fence.Namespace != ns || e.Namespace != ns {
		return namespace.ErrFenced
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	space, ok := m.spaces[ns]
	if !ok {
		return namespace.ErrNotFound
	}

	if fence.FailoverVersion != space.version {
		return namespace.ErrFenced
	}

	shown := space.raw

	if old, decodeErr := source.DecodeFilter(space.raw); decodeErr != nil {
		shown = "invalid:" + strconv.Quote(space.raw)
	} else if shown, err = source.EncodeFilter(old); err != nil {
		return err
	}

	e.Changes = append([]audit.Change(nil), e.Changes...)
	if len(e.Changes) == 1 {
		e.Changes[0].Old = &shown
	}

	space.raw = text
	space.audited = append(space.audited, e)

	return nil
}

// harness returns the Harness over m.
func (m *memFilters) harness() sourcetest.Harness {
	return sourcetest.Harness{
		Filters: m,
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			m.spaces[namespace.Name(name)] = &memNamespace{raw: "", audited: nil, version: 1}

			return namespace.Fence{Namespace: namespace.Name(name), FailoverVersion: 1}
		},
		Audited: func(t *testing.T, ns string) []audit.Entry {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			return append([]audit.Entry(nil), m.spaces[namespace.Name(ns)].audited...)
		},
		FilterRaw: func(t *testing.T, ns namespace.Name, raw string) {
			t.Helper()

			m.mu.Lock()
			defer m.mu.Unlock()

			m.spaces[ns].raw = raw
		},
	}
}

// TestSuiteOverAMemoryFilters runs the suite with a Filters in memory.
func TestSuiteOverAMemoryFilters(t *testing.T) {
	t.Parallel()

	sourcetest.Run(t, func(t *testing.T) sourcetest.Harness {
		t.Helper()

		return newMemFilters().harness()
	})
}

// TestCaseNamesAreUnique proves that each case has a name of its own, so a
// skip or a failure names one behavior.
func TestCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, name := range sourcetest.Names() {
		if seen[name] {
			t.Fatalf("case %q is in the suite twice", name)
		}

		seen[name] = true
	}

	if len(seen) == 0 {
		t.Fatal("the suite has no case")
	}
}
