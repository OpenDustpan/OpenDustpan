package freezetest_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/freeze"
	"opendustpan.org/opendustpan/internal/freeze/freezetest"
	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
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
			freezes:   map[string]*job.Freeze{},
			now:       time.Time{},
		}

		return freezetest.Harness{Schedules: db, Freezes: db, Domain: db}
	})
}

// memStore is the reference adapter of freeze.Store and freeze.Sink in
// memory. The fields below the domain change only under its mutex.
type memStore struct {
	*opsmem.Domain

	schedules map[string]freeze.Schedule
	freezes   map[string]*job.Freeze
	now       time.Time
}

// scoped translates the error of a fence that does not belong to the
// namespace of the row into sentinel.
func scoped(err, sentinel error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%w: %w", sentinel, err)
	}

	return err
}

func (m *memStore) SetNow(t time.Time) {
	m.Read(func() { m.now = t })
}

func (m *memStore) Create(_ context.Context, s freeze.Schedule, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	err := m.Write(fence, opsmem.Scope(s.Namespace), []audit.Entry{e}, func() error {
		if _, ok := m.schedules[s.ID]; ok {
			return fmt.Errorf("create %s: %w", s.ID, freeze.ErrExists)
		}

		s.Version = 1
		m.schedules[s.ID] = s

		return nil
	})

	return scoped(err, freeze.ErrInvalid)
}

func (m *memStore) Get(_ context.Context, ns namespace.Name, id string) (freeze.Schedule, error) {
	var (
		s  freeze.Schedule
		ok bool
	)

	m.Read(func() { s, ok = m.schedules[id] })

	if !ok || s.Namespace != ns {
		var none freeze.Schedule

		return none, fmt.Errorf("get %s/%s: %w", ns, id, freeze.ErrNotFound)
	}

	return s, nil
}

func (m *memStore) Update(_ context.Context, s freeze.Schedule, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	var old freeze.Schedule

	err := m.Write(fence, m.schedule(s.ID, &old), []audit.Entry{e}, func() error {
		if old.Namespace != s.Namespace {
			return fmt.Errorf("%w: a schedule cannot move to another namespace", freeze.ErrInvalid)
		}

		if old.Version != s.Version {
			return fmt.Errorf("update %s: %w", s.ID, freeze.ErrConflict)
		}

		s.Version++
		m.schedules[s.ID] = s

		return nil
	})

	return scoped(err, freeze.ErrNotFound)
}

func (m *memStore) Delete(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	var old freeze.Schedule

	err := m.Write(fence, m.schedule(id, &old), []audit.Entry{e}, func() error {
		delete(m.schedules, id)

		return nil
	})

	return scoped(err, freeze.ErrNotFound)
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
	_ context.Context, s freeze.Schedule, occs []freeze.Occurrence, fence namespace.Fence,
) error {
	var stored freeze.Schedule

	err := m.Write(fence, m.schedule(s.ID, &stored), nil, func() error {
		if stored.Version != s.Version {
			return fmt.Errorf("replace %s: %w", s.ID, freeze.ErrConflict)
		}

		want := map[string]job.Freeze{}

		for _, o := range occs {
			if o.Schedule != stored.ID || !o.End.After(o.Start) {
				return fmt.Errorf("%w: an occurrence of %s needs its schedule ID and an end after its start",
					freeze.ErrInvalid, stored.ID)
			}

			f := stored.FreezeOf(o)
			want[f.ID] = f
		}

		m.refresh(stored.Namespace, freeze.Subject(stored.ID), want)
		m.add(stored.Namespace, freeze.Subject(stored.ID), want)

		return nil
	})

	return scoped(err, freeze.ErrNotFound)
}

func (m *memStore) DeleteScheduled(_ context.Context, id string, fence namespace.Fence) error {
	subject := freeze.Subject(id)

	scope := func() (namespace.Name, error) {
		if s, ok := m.schedules[id]; ok && s.Namespace == fence.Namespace {
			return "", fmt.Errorf("delete freezes of %s: %w", id, freeze.ErrExists)
		}

		if fence.Namespace == "" {
			return "", fmt.Errorf("delete freezes of %s: %w", id, opsmem.ErrScope)
		}

		return fence.Namespace, nil
	}

	err := m.Write(fence, scope, nil, func() error {
		for fid, f := range m.freezes {
			if f.Namespace == fence.Namespace && f.CreatedBy == subject && f.EndedBy == "" &&
				f.StartsAt.After(m.now) {
				delete(m.freezes, fid)
			}
		}

		return nil
	})

	return scoped(err, freeze.ErrNotFound)
}

// CreateFreeze implements job.FreezeStore.
func (m *memStore) CreateFreeze(_ context.Context, f job.Freeze, fence namespace.Fence, e audit.Entry) error {
	if err := f.Validate(); err != nil {
		return err
	}

	if strings.HasPrefix(f.CreatedBy, freeze.SubjectPrefix) {
		return fmt.Errorf("%w: a person cannot create a freeze as a schedule", job.ErrInvalid)
	}

	if freeze.IsOccurrenceID(f.ID) {
		return fmt.Errorf("%w: only a schedule makes the ID %s", job.ErrInvalid, f.ID)
	}

	err := m.Write(fence, opsmem.Scope(f.Namespace), []audit.Entry{e}, func() error {
		if _, ok := m.freezes[f.ID]; ok {
			return fmt.Errorf("%w: freeze %s exists", job.ErrInvalid, f.ID)
		}

		m.freezes[f.ID] = &f

		return nil
	})

	return scoped(err, job.ErrInvalid)
}

// EndFreeze implements job.FreezeStore, on the clock of the store.
func (m *memStore) EndFreeze(_ context.Context, id, by string, fence namespace.Fence, e audit.Entry) error {
	var f *job.Freeze

	scope := func() (namespace.Name, error) {
		var ok bool
		if f, ok = m.freezes[id]; !ok {
			return "", fmt.Errorf("end freeze %s: %w", id, job.ErrNotFound)
		}

		return f.Namespace, nil
	}

	err := m.Write(fence, scope, []audit.Entry{e}, func() error {
		if f == nil || !f.ActiveAt(m.now) {
			return fmt.Errorf("end freeze %s: %w", id, job.ErrFinished)
		}

		f.EndsAt, f.EndedBy = m.now, by

		return nil
	})

	return scoped(err, job.ErrNotFound)
}

// ListFreezes implements job.FreezeStore.
func (m *memStore) ListFreezes(_ context.Context, ns namespace.Name, since time.Time) ([]job.Freeze, error) {
	var out []job.Freeze

	m.Read(func() {
		for _, f := range m.freezes {
			if f.Namespace == ns && (f.EndsAt.IsZero() || f.EndsAt.After(since)) {
				out = append(out, *f)
			}
		}
	})

	slices.SortFunc(out, func(a, b job.Freeze) int { return a.StartsAt.Compare(b.StartsAt) })

	return out, nil
}

// schedule returns a scope function that looks up the schedule id, keeps
// it in got, and returns its namespace, or ErrNotFound.
func (m *memStore) schedule(id string, got *freeze.Schedule) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		s, ok := m.schedules[id]
		if !ok {
			return "", fmt.Errorf("schedule %s: %w", id, freeze.ErrNotFound)
		}

		*got = s

		return s.Namespace, nil
	}
}

// refresh changes the stored freezes of subject in ns toward want. The
// caller holds the mutex of the domain.
func (m *memStore) refresh(ns namespace.Name, subject string, want map[string]job.Freeze) {
	for fid, f := range m.freezes {
		if f.Namespace != ns || f.CreatedBy != subject || f.EndedBy != "" {
			continue
		}

		w, ok := want[fid]

		switch {
		case f.StartsAt.After(m.now) && !ok:
			delete(m.freezes, fid)
		case f.StartsAt.After(m.now):
			f.EndsAt, f.Reason = w.EndsAt, w.Reason
		case ok && !f.EndsAt.IsZero() && w.EndsAt.After(f.EndsAt):
			f.EndsAt = w.EndsAt
		default:
			// A started freeze keeps its end when the plan ends it earlier.
		}
	}
}

// add stores each freeze of want that has no row. A freeze that started is
// added only when no freeze of subject in ns overlaps it. The caller holds
// the mutex of the domain.
func (m *memStore) add(ns namespace.Name, subject string, want map[string]job.Freeze) {
	for fid := range want {
		if _, ok := m.freezes[fid]; ok {
			continue
		}

		w := want[fid]

		if !w.StartsAt.After(m.now) && m.overlaps(ns, subject, w) {
			continue
		}

		m.freezes[fid] = &w
	}
}

func (m *memStore) overlaps(ns namespace.Name, subject string, w job.Freeze) bool {
	for _, f := range m.freezes {
		if f.Namespace == ns && f.CreatedBy == subject && f.StartsAt.Before(w.EndsAt) &&
			(f.EndsAt.IsZero() || w.StartsAt.Before(f.EndsAt)) {
			return true
		}
	}

	return false
}
