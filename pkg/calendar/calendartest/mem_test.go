package calendartest_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/calendar/calendartest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	calendartest.Run(t, func(*testing.T) calendartest.Harness {
		db := &memStore{
			Domain:  opsmem.New(),
			cals:    map[calendar.ID]*row{},
			assigns: map[calendar.Scope]calendar.ID{},
		}

		return calendartest.Harness{Store: db, Domain: db}
	})
}

type row struct {
	owner calendar.Scope
	cal   calendar.Calendar
	// feeds maps a feed ID to the holidays of its last good read.
	feeds map[string][]calendar.Holiday
}

// memStore is the reference adapter of calendar.Store in memory.
type memStore struct {
	*opsmem.Domain

	cals    map[calendar.ID]*row
	assigns map[calendar.Scope]calendar.ID
}

func (m *memStore) Create(
	_ context.Context, owner calendar.Scope, c calendar.Calendar, fence namespace.Fence, e audit.Entry,
) error {
	if err := owner.Validate(); err != nil {
		return err
	}

	if err := c.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.cals[c.ID]; ok || c.ID == calendar.DefaultID {
			return fmt.Errorf("create %s: %w", c.ID, calendar.ErrExists)
		}

		c.Version = 1
		m.cals[c.ID] = &row{owner: owner, cal: manualOnly(c), feeds: map[string][]calendar.Holiday{}}

		return nil
	})
}

func (m *memStore) Get(_ context.Context, id calendar.ID) (calendar.Calendar, error) {
	var (
		c  calendar.Calendar
		ok bool
	)

	m.Read(func() { c, ok = m.lookup(id) })

	if !ok {
		return calendar.Calendar{}, fmt.Errorf("get %s: %w", id, calendar.ErrNotFound)
	}

	return c, nil
}

func (m *memStore) Update(_ context.Context, c calendar.Calendar, fence namespace.Fence, e audit.Entry) error {
	if err := c.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		r, ok := m.cals[c.ID]
		if !ok {
			return fmt.Errorf("update %s: %w", c.ID, calendar.ErrNotFound)
		}

		if r.cal.Version != c.Version {
			return fmt.Errorf("update %s: %w", c.ID, calendar.ErrConflict)
		}

		c.Version++
		r.cal = manualOnly(c)

		return nil
	})
}

func (m *memStore) Delete(_ context.Context, id calendar.ID, fence namespace.Fence, e audit.Entry) error {
	if id == calendar.DefaultID {
		return fmt.Errorf("delete the default calendar: %w", calendar.ErrInvalid)
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.cals[id]; !ok {
			return fmt.Errorf("delete %s: %w", id, calendar.ErrNotFound)
		}

		for _, used := range m.assigns {
			if used == id {
				return fmt.Errorf("delete %s: %w", id, calendar.ErrInUse)
			}
		}

		delete(m.cals, id)

		return nil
	})
}

func (m *memStore) Assign(_ context.Context, a calendar.Assignment, fence namespace.Fence, e audit.Entry) error {
	if err := a.Scope.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if a.Calendar == "" {
			delete(m.assigns, a.Scope)

			return nil
		}

		r, ok := m.cals[a.Calendar]
		if !ok && a.Calendar != calendar.DefaultID {
			return fmt.Errorf("assign %s: %w", a.Calendar, calendar.ErrNotFound)
		}

		if ok && r.owner.Namespace != "" && r.owner.Namespace != a.Scope.Namespace {
			return fmt.Errorf(
				"assign %s: %w: the calendar belongs to another namespace",
				a.Calendar,
				calendar.ErrInvalid,
			)
		}

		m.assigns[a.Scope] = a.Calendar

		return nil
	})
}

func (m *memStore) Resolve(_ context.Context, chain []calendar.Scope) (calendar.Resolved, error) {
	res := calendar.Resolved{Calendar: calendar.Default(), From: calendar.Installation()}

	m.Read(func() {
		for _, s := range chain {
			id, ok := m.assigns[s]
			if !ok {
				continue
			}

			if c, found := m.lookup(id); found {
				res = calendar.Resolved{Calendar: c, From: s}

				return
			}
		}
	})

	return res, nil
}

func (m *memStore) ReplaceFeedHolidays(
	_ context.Context, id calendar.ID, feedID string, holidays []calendar.Holiday, status calendar.FeedStatus,
) error {
	if len(holidays) > calendar.MaxHolidays {
		return fmt.Errorf("replace feed holidays: %w", calendar.ErrInvalid)
	}

	return m.Write(namespace.Fence{Namespace: "", FailoverVersion: 0}, nil, func() error {
		r, ok := m.cals[id]
		if !ok {
			return fmt.Errorf("replace feed holidays of %s: %w", id, calendar.ErrNotFound)
		}

		i := slices.IndexFunc(r.cal.Feeds, func(f calendar.Feed) bool { return f.ID == feedID })
		if i < 0 {
			return fmt.Errorf("replace holidays of feed %s: %w", feedID, calendar.ErrNotFound)
		}

		if status.Error == "" {
			r.feeds[feedID] = slices.Clone(holidays)
			r.cal.Feeds[i].Status = status

			return nil
		}

		kept := r.cal.Feeds[i].Status
		kept.ReadAt, kept.Error = status.ReadAt, status.Error
		r.cal.Feeds[i].Status = kept

		return nil
	})
}

// lookup returns the calendar id with the holidays of its feeds. The caller
// holds the mutex of the domain.
func (m *memStore) lookup(id calendar.ID) (calendar.Calendar, bool) {
	r, ok := m.cals[id]
	if !ok {
		if id == calendar.DefaultID {
			return calendar.Default(), true
		}

		var none calendar.Calendar

		return none, false
	}

	c := r.cal
	c.Feeds = slices.Clone(c.Feeds)
	c.Holidays = slices.Clone(c.Holidays)

	for _, hs := range r.feeds {
		c.Holidays = append(c.Holidays, hs...)
	}

	slices.SortFunc(c.Holidays, func(a, b calendar.Holiday) int {
		switch {
		case a.Date.Before(b.Date):
			return -1
		case b.Date.Before(a.Date):
			return 1
		default:
			return 0
		}
	})

	return c, true
}

// manualOnly returns c with its manual holidays only: the store keeps the
// holidays of each feed apart.
func manualOnly(c calendar.Calendar) calendar.Calendar {
	c.Holidays = slices.DeleteFunc(slices.Clone(c.Holidays), func(h calendar.Holiday) bool {
		return h.Source != calendar.SourceManual
	})
	c.Feeds = slices.Clone(c.Feeds)

	return c
}
