package oncalltest_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/oncall"
	"opendustpan.org/opendustpan/pkg/oncall/oncalltest"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	oncalltest.Run(t, func(*testing.T) oncalltest.Harness {
		db := &memStore{Domain: opsmem.New(), schedules: map[string]oncall.Schedule{}, handoffs: nil, now: time.Time{}}

		return oncalltest.Harness{Schedules: db, Handoffs: db, Domain: db, SetClock: db.setClock}
	})
}

// memStore is the reference adapter of oncall.Store and oncall.Handoffs in
// memory.
type memStore struct {
	*opsmem.Domain

	schedules map[string]oncall.Schedule
	handoffs  []oncall.Handoff
	// now is the time of each transaction, as the clock of a database
	// gives it.
	now time.Time
}

// entryIn returns scope with a check that e belongs to the namespace of
// the row: an entry of another namespace is ErrInvalid.
func entryIn(e audit.Entry, scope func() (namespace.Name, error)) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		ns, err := scope()
		if err != nil {
			return ns, err
		}

		if e.Namespace != ns {
			return "", fmt.Errorf("%w: the audit entry belongs to %s, not to %s", oncall.ErrInvalid, e.Namespace, ns)
		}

		return ns, nil
	}
}

// translate returns err with opsmem.ErrScope replaced by scoped, the
// sentinel that the port gives for a fence that does not belong to the
// row.
func translate(err, scoped error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%w: %w", scoped, err)
	}

	return err
}

func (m *memStore) Create(_ context.Context, s oncall.Schedule, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	if len(s.Copied) > 0 || !s.Synced.IsZero() {
		return fmt.Errorf("create %s: %w: only a copy sets the copied shifts", s.ID, oncall.ErrInvalid)
	}

	return translate(m.Write(fence, entryIn(e, opsmem.Scope(s.Namespace)), []audit.Entry{e}, func() error {
		if _, ok := m.schedules[s.ID]; ok {
			return fmt.Errorf("create %s: %w", s.ID, oncall.ErrExists)
		}

		if _, ok := m.byRef(oncall.Ref{Namespace: s.Namespace, Name: s.Name}); ok {
			return fmt.Errorf("create %s: %w", s.Name, oncall.ErrExists)
		}

		s.Version = 1
		m.schedules[s.ID] = s

		return nil
	}), oncall.ErrInvalid)
}

func (m *memStore) Get(_ context.Context, ref oncall.Ref) (oncall.Schedule, error) {
	var (
		s  oncall.Schedule
		ok bool
	)

	m.Read(func() { s, ok = m.byRef(ref) })

	if !ok {
		return s, fmt.Errorf("get %s/%s: %w", ref.Namespace, ref.Name, oncall.ErrNotFound)
	}

	return s, nil
}

func (m *memStore) Update(_ context.Context, s oncall.Schedule, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	scope := func() (namespace.Name, error) {
		old, ok := m.schedules[s.ID]
		if !ok {
			return "", fmt.Errorf("update %s: %w", s.ID, oncall.ErrNotFound)
		}

		return old.Namespace, nil
	}

	return translate(m.Write(fence, entryIn(e, scope), []audit.Entry{e}, func() error {
		old := m.schedules[s.ID]

		if s.Namespace != old.Namespace {
			return fmt.Errorf("update %s: %w: a schedule cannot move to another namespace", s.ID, oncall.ErrInvalid)
		}

		if old.Version != s.Version {
			return fmt.Errorf("update %s: %w", s.ID, oncall.ErrConflict)
		}

		if other, used := m.byRef(oncall.Ref{Namespace: s.Namespace, Name: s.Name}); used && other.ID != s.ID {
			return fmt.Errorf("update %s: %w", s.Name, oncall.ErrExists)
		}

		s.Copied, s.Synced = old.Copied, old.Synced
		s.Version++
		m.schedules[s.ID] = s

		return nil
	}), oncall.ErrNotFound)
}

func (m *memStore) Delete(_ context.Context, ref oncall.Ref, fence namespace.Fence, e audit.Entry) error {
	return translate(m.Write(fence, entryIn(e, m.refScope("delete", ref)), []audit.Entry{e}, func() error {
		s, _ := m.byRef(ref)
		delete(m.schedules, s.ID)

		return nil
	}), oncall.ErrNotFound)
}

func (m *memStore) AddOverride(
	_ context.Context, ref oncall.Ref, o oncall.Override, fence namespace.Fence, e audit.Entry,
) error {
	return translate(m.Write(fence, entryIn(e, m.refScope("add override to", ref)), []audit.Entry{e}, func() error {
		// The store sets the time of the add from its own transaction, so
		// a time in the request cannot decide which overrides ended.
		o.CreatedAt = m.now
		if err := o.Validate(); err != nil {
			return err
		}

		s, _ := m.byRef(ref)

		live := slices.DeleteFunc(slices.Clone(s.Overrides), func(x oncall.Override) bool {
			return !x.End.After(o.CreatedAt)
		})
		if len(live) >= oncall.MaxOverrides {
			return fmt.Errorf("add override: %w: at most %d", oncall.ErrInvalid, oncall.MaxOverrides)
		}

		live = append(live, o)
		s.Overrides = live
		s.Version++
		m.schedules[s.ID] = s

		return nil
	}), oncall.ErrNotFound)
}

func (m *memStore) ReplaceCopy(
	_ context.Context, ref oncall.Ref, shifts []oncall.Shift, syncedAt time.Time, fence namespace.Fence,
) error {
	return translate(m.Write(fence, m.refScope("copy", ref), nil, func() error {
		s, _ := m.byRef(ref)

		if s.Source != oncall.SourceExternal {
			return fmt.Errorf("copy %s/%s: %w: the schedule is local", ref.Namespace, ref.Name, oncall.ErrInvalid)
		}

		s.Copied, s.Synced = slices.Clone(shifts), syncedAt
		m.schedules[s.ID] = s

		return nil
	}), oncall.ErrNotFound)
}

func (m *memStore) ForPrincipal(_ context.Context, principal string) ([]oncall.Schedule, error) {
	var out []oncall.Schedule

	m.Read(func() {
		for id := range m.schedules {
			if s := m.schedules[id]; names(s, principal) {
				out = append(out, s)
			}
		}
	})

	slices.SortFunc(out, func(a, b oncall.Schedule) int {
		return cmp.Or(strings.Compare(a.Namespace.String(), b.Namespace.String()), strings.Compare(a.Name, b.Name))
	})

	return out, nil
}

func (m *memStore) AddHandoff(_ context.Context, h oncall.Handoff, fence namespace.Fence, e audit.Entry) error {
	if strings.TrimSpace(h.Note) == "" || len(h.Note) > oncall.MaxHandoffNoteLen {
		return fmt.Errorf("add handoff: %w: a note has 1 to %d bytes", oncall.ErrInvalid, oncall.MaxHandoffNoteLen)
	}

	scope := func() (namespace.Name, error) {
		s, ok := m.schedules[h.Schedule]
		if !ok {
			return "", fmt.Errorf("add handoff to %s: %w", h.Schedule, oncall.ErrNotFound)
		}

		return s.Namespace, nil
	}

	return translate(m.Write(fence, entryIn(e, scope), []audit.Entry{e}, func() error {
		m.handoffs = append(m.handoffs, h)

		return nil
	}), oncall.ErrNotFound)
}

func (m *memStore) Handoffs(_ context.Context, schedule string, from, to time.Time) ([]oncall.Handoff, error) {
	var out []oncall.Handoff

	m.Read(func() {
		for _, h := range m.handoffs {
			if h.Schedule == schedule && !h.At.Before(from) && h.At.Before(to) {
				out = append(out, h)
			}
		}
	})

	slices.SortFunc(out, func(a, b oncall.Handoff) int { return b.At.Compare(a.At) })

	return out, nil
}

// byRef returns the schedule that ref names. The caller holds the mutex
// of the domain.
func (m *memStore) byRef(ref oncall.Ref) (oncall.Schedule, bool) {
	for id := range m.schedules {
		if s := m.schedules[id]; s.Namespace == ref.Namespace && s.Name == ref.Name {
			return s, true
		}
	}

	var none oncall.Schedule

	return none, false
}

// names reports whether s names principal in a layer, an override or a
// copied shift.
func names(s oncall.Schedule, principal string) bool {
	for _, l := range s.Layers {
		if slices.Contains(l.Members, principal) {
			return true
		}
	}

	for i := range s.Overrides {
		if s.Overrides[i].Principal == principal {
			return true
		}
	}

	for _, c := range s.Copied {
		if c.Principal == principal {
			return true
		}
	}

	return false
}

// refScope returns the scope function of a write to the schedule ref
// names: its namespace, or ErrNotFound for an unknown schedule.
func (m *memStore) refScope(op string, ref oncall.Ref) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		s, ok := m.byRef(ref)
		if !ok {
			return "", fmt.Errorf("%s %s/%s: %w", op, ref.Namespace, ref.Name, oncall.ErrNotFound)
		}

		return s.Namespace, nil
	}
}

func (m *memStore) setClock(t time.Time) { m.Read(func() { m.now = t }) }
