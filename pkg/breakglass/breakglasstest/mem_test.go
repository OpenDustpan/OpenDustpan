package breakglasstest_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/breakglass/breakglasstest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	breakglasstest.Run(t, func(*testing.T) breakglasstest.Harness {
		db := &memStore{Domain: opsmem.New(), reviews: map[string]breakglass.Review{}}

		return breakglasstest.Harness{Store: db, Domain: db}
	})
}

// memStore is the reference adapter of breakglass.Store in memory.
type memStore struct {
	*opsmem.Domain

	reviews map[string]breakglass.Review
}

func (m *memStore) Open(
	_ context.Context, r breakglass.Review, fence namespace.Fence, e audit.Entry,
) (breakglass.Review, error) {
	if err := r.Use.Validate(); err != nil {
		return breakglass.Review{}, err
	}

	var (
		out breakglass.Review
		old breakglass.Review
		ok  bool
	)

	scope := func() (namespace.Name, error) {
		old, ok = m.byUse(r.Use)
		if ok {
			return old.Use.Scope, nil
		}

		return r.Use.Scope, nil
	}

	err := m.WriteEntries(fence, scope, func() ([]audit.Entry, error) {
		if ok {
			out = old

			return nil, nil
		}

		r.Version = 1
		m.reviews[r.ID], out = r, r

		return []audit.Entry{e}, nil
	})
	if errors.Is(err, opsmem.ErrScope) {
		return breakglass.Review{}, fmt.Errorf("open %s: %w: %w", r.ID, breakglass.ErrInvalid, err)
	}

	if err != nil {
		return breakglass.Review{}, err
	}

	return out, nil
}

func (m *memStore) Get(_ context.Context, id string) (breakglass.Review, error) {
	var (
		r  breakglass.Review
		ok bool
	)

	m.Read(func() { r, ok = m.reviews[id] })

	if !ok {
		return r, fmt.Errorf("get %s: %w", id, breakglass.ErrNotFound)
	}

	return r, nil
}

func (m *memStore) List(_ context.Context, f breakglass.Filter) ([]breakglass.Review, error) {
	var out []breakglass.Review

	m.Read(func() {
		for _, id := range slices.Sorted(maps.Keys(m.reviews)) {
			r := m.reviews[id]

			switch {
			case f.Scope != "" && r.Use.Scope != f.Scope,
				f.State != "" && r.State != f.State,
				!f.DueBefore.IsZero() && (r.DueAt.IsZero() || !r.DueAt.Before(f.DueBefore)),
				f.After != "" && id <= f.After:
				continue
			default:
				// The review matches the filter.
			}

			out = append(out, r)
			if f.Limit > 0 && len(out) == f.Limit {
				return
			}
		}
	})

	return out, nil
}

func (m *memStore) Record(_ context.Context, r breakglass.Review, fence namespace.Fence, e audit.Entry) error {
	err := m.Write(fence, m.scope(r.ID), []audit.Entry{e}, func() error {
		old, err := m.current(r)
		if err != nil {
			return err
		}

		if err := breakglass.CheckVerdict(old, r.State, r.Verdict); err != nil {
			return fmt.Errorf("record %s: %w", r.ID, err)
		}

		old.State, old.Verdict, old.NextNoticeAt = r.State, r.Verdict, time.Time{}
		old.Version++
		m.reviews[r.ID] = old

		return nil
	})

	return notFound(r.ID, err)
}

func (m *memStore) Advance(_ context.Context, r breakglass.Review, fence namespace.Fence) error {
	err := m.Write(fence, m.scope(r.ID), nil, func() error {
		old, err := m.current(r)
		if err != nil {
			return err
		}

		old.Step, old.NextNoticeAt = r.Step, r.NextNoticeAt
		old.Version++
		m.reviews[r.ID] = old

		return nil
	})

	return notFound(r.ID, err)
}

// notFound translates a fence of another scope into ErrNotFound, so that
// a write does not tell that a review exists in another namespace.
func notFound(id string, err error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("review %s: %w", id, breakglass.ErrNotFound)
	}

	return err
}

func (m *memStore) Due(_ context.Context, now time.Time, limit int) ([]breakglass.Review, error) {
	var out []breakglass.Review

	m.Read(func() {
		for id := range m.reviews {
			r := m.reviews[id]
			if r.State == breakglass.StatePending && !r.NextNoticeAt.IsZero() && !r.NextNoticeAt.After(now) {
				out = append(out, r)
			}
		}
	})

	slices.SortFunc(out, func(a, b breakglass.Review) int { return a.NextNoticeAt.Compare(b.NextNoticeAt) })

	return out[:min(limit, len(out))], nil
}

// current returns the stored review of r when r names its version. The
// caller holds the mutex of the domain.
func (m *memStore) current(r breakglass.Review) (breakglass.Review, error) {
	old, ok := m.reviews[r.ID]
	if !ok {
		return old, fmt.Errorf("review %s: %w", r.ID, breakglass.ErrNotFound)
	}

	if old.Version != r.Version {
		return old, fmt.Errorf("review %s: %w", r.ID, breakglass.ErrConflict)
	}

	return old, nil
}

// byUse returns the review of the kind and the reference of u. The caller
// holds the mutex of the domain.
func (m *memStore) byUse(u breakglass.Use) (breakglass.Review, bool) {
	for id := range m.reviews {
		if old := m.reviews[id]; old.Use.Kind == u.Kind && old.Use.Ref == u.Ref {
			return old, true
		}
	}

	var none breakglass.Review

	return none, false
}

// scope returns the scope function of a write to the stored review id. It
// returns ErrNotFound for an unknown review. It runs under the mutex of
// the domain.
func (m *memStore) scope(id string) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		old, ok := m.reviews[id]
		if !ok {
			return "", fmt.Errorf("review %s: %w", id, breakglass.ErrNotFound)
		}

		return old.Use.Scope, nil
	}
}
