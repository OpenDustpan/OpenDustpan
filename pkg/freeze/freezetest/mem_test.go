package freezetest_test

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freeze"
	"opendustpan.org/opendustpan/pkg/freeze/freezetest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	freezetest.Run(t, func(*testing.T) freezetest.Harness {
		db := &memStore{
			Domain:    opsmem.New(),
			schedules: map[string]freeze.Schedule{},
			freezes:   map[string]*stored{},
		}

		return freezetest.Harness{Schedules: db, Sink: db, Domain: db}
	})
}

type stored struct {
	ns namespace.Name
	f  freezetest.Freeze
}

// memStore is the reference adapter of freeze.Store and freeze.Sink
// in memory.
type memStore struct {
	*opsmem.Domain

	schedules map[string]freeze.Schedule
	freezes   map[string]*stored
}

func (m *memStore) Create(_ context.Context, s freeze.Schedule, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.schedules[s.ID]; ok {
			return fmt.Errorf("create %s: %w", s.ID, freeze.ErrExists)
		}

		s.Version = 1
		m.schedules[s.ID] = s

		return nil
	})
}

func (m *memStore) Get(_ context.Context, id string) (freeze.Schedule, error) {
	var (
		s  freeze.Schedule
		ok bool
	)

	m.Read(func() { s, ok = m.schedules[id] })

	if !ok {
		return s, fmt.Errorf("get %s: %w", id, freeze.ErrNotFound)
	}

	return s, nil
}

func (m *memStore) Update(_ context.Context, s freeze.Schedule, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		old, ok := m.schedules[s.ID]
		if !ok {
			return fmt.Errorf("update %s: %w", s.ID, freeze.ErrNotFound)
		}

		if old.Version != s.Version {
			return fmt.Errorf("update %s: %w", s.ID, freeze.ErrConflict)
		}

		s.Version++
		m.schedules[s.ID] = s

		return nil
	})
}

func (m *memStore) Delete(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.schedules[id]; !ok {
			return fmt.Errorf("delete %s: %w", id, freeze.ErrNotFound)
		}

		delete(m.schedules, id)

		return nil
	})
}

func (m *memStore) List(_ context.Context, ns namespace.Name) ([]freeze.Schedule, error) {
	var out []freeze.Schedule

	m.Read(func() {
		for _, id := range slices.Sorted(maps.Keys(m.schedules)) {
			if s := m.schedules[id]; s.Namespace == ns {
				out = append(out, s)
			}
		}
	})

	return out, nil
}

func (m *memStore) ReplaceScheduled(
	_ context.Context, fence namespace.Fence, id string, from time.Time, occs []freeze.Occurrence,
) error {
	return m.Write(fence, nil, func() error {
		want := map[string]freeze.Occurrence{}
		for _, o := range occs {
			want[freeze.OccurrenceID(id, o.Start)] = o
		}

		m.dropUnwanted(id, from, want)
		m.addOrExtend(fence.Namespace, id, from, want)

		return nil
	})
}

func (m *memStore) Freezes(_ context.Context, ns namespace.Name) ([]freezetest.Freeze, error) {
	var out []freezetest.Freeze

	m.Read(func() {
		for _, s := range m.freezes {
			if s.ns == ns {
				out = append(out, s.f)
			}
		}
	})

	slices.SortFunc(
		out,
		func(a, b freezetest.Freeze) int { return cmp.Compare(a.Start.UnixMilli(), b.Start.UnixMilli()) },
	)

	return out, nil
}

func (m *memStore) EndFreeze(_ context.Context, id string, at time.Time) error {
	return m.Write(namespace.Fence{Namespace: "", FailoverVersion: 0}, nil, func() error {
		s, ok := m.freezes[id]
		if !ok {
			return fmt.Errorf("end freeze %s: %w", id, freeze.ErrNotFound)
		}

		s.f.End, s.f.Ended = at, true

		return nil
	})
}

// dropUnwanted deletes each freeze of the schedule id that starts at or
// after from and is not in want. The caller holds the mutex of the domain.
func (m *memStore) dropUnwanted(id string, from time.Time, want map[string]freeze.Occurrence) {
	for fid, s := range m.freezes {
		if s.f.Schedule != id || s.f.Start.Before(from) {
			continue
		}

		if _, ok := want[fid]; !ok {
			delete(m.freezes, fid)
		}
	}
}

// addOrExtend adds each freeze of want that is missing, and sets the end
// of each one that starts at or after from and has not ended. The caller
// holds the mutex of the domain.
func (m *memStore) addOrExtend(ns namespace.Name, id string, from time.Time, want map[string]freeze.Occurrence) {
	for fid, o := range want {
		s, ok := m.freezes[fid]

		switch {
		case !ok:
			f := freezetest.Freeze{ID: fid, Schedule: id, Start: o.Start, End: o.End, Ended: false}
			m.freezes[fid] = &stored{ns: ns, f: f}
		case !s.f.Start.Before(from) && !s.f.Ended:
			s.f.End = o.End
		default:
			// A freeze that started before from, or that ended, never changes.
		}
	}
}
