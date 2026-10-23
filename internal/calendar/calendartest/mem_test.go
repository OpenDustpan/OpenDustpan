package calendartest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/calendar"
	"opendustpan.org/opendustpan/internal/calendar/calendartest"
	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
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
	cal calendar.Calendar
	// feeds maps a feed ID to the holidays of its last good read.
	feeds map[string][]calendar.Holiday
}

// memStore is the reference adapter of calendar.Store in memory.
type memStore struct {
	*opsmem.Domain

	cals    map[calendar.ID]*row
	assigns map[calendar.Scope]calendar.ID
}

// scopeErr translates opsmem.ErrScope into the sentinel of a method.
func scopeErr(err, sentinel error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%w: %w", sentinel, err)
	}

	return err
}

func (m *memStore) Create(_ context.Context, c calendar.Calendar, fence namespace.Fence, e audit.Entry) error {
	if err := c.Validate(); err != nil {
		return err
	}

	err := m.Write(fence, opsmem.Scope(c.Owner.WriteScope()), []audit.Entry{e}, func() error {
		if _, ok := m.cals[c.ID]; ok || c.ID == calendar.DefaultID {
			return fmt.Errorf("create %s: %w", c.ID, calendar.ErrExists)
		}

		c.Version = 1
		c.Feeds, _ = calendar.MergeFeeds(nil, c.Feeds)
		m.cals[c.ID] = &row{cal: manualOnly(c), feeds: map[string][]calendar.Holiday{}}

		return nil
	})

	return scopeErr(err, calendar.ErrInvalid)
}

func (m *memStore) Get(_ context.Context, scope namespace.Name, id calendar.ID) (calendar.Calendar, error) {
	var (
		c  calendar.Calendar
		ok bool
	)

	m.Read(func() { c, ok = m.lookup(id) })

	// A calendar of another scope answers as an unknown ID.
	if !ok || c.Owner.WriteScope() != scope {
		return calendar.Calendar{}, fmt.Errorf("get %s in %s: %w", id, scope, calendar.ErrNotFound)
	}

	return c, nil
}

func (m *memStore) Update(_ context.Context, c calendar.Calendar, fence namespace.Fence, e audit.Entry) error {
	if err := c.Validate(); err != nil {
		return err
	}

	err := m.Write(fence, m.rowScope(c.ID), []audit.Entry{e}, func() error {
		r, ok := m.cals[c.ID]
		if !ok {
			// The default calendar before its first update.
			r = &row{cal: calendar.Default(), feeds: map[string][]calendar.Holiday{}}
		}

		if c.Owner != r.cal.Owner {
			return fmt.Errorf("update %s: %w: the owner of a calendar does not change", c.ID, calendar.ErrInvalid)
		}

		if r.cal.Version != c.Version {
			return fmt.Errorf("update %s: %w", c.ID, calendar.ErrConflict)
		}

		feeds, keep := calendar.MergeFeeds(r.cal.Feeds, c.Feeds)
		for id := range r.feeds {
			if !keep[id] {
				delete(r.feeds, id)
			}
		}

		c.Version++
		c.Feeds = feeds
		r.cal = manualOnly(c)
		m.cals[c.ID] = r

		return nil
	})

	return scopeErr(err, calendar.ErrNotFound)
}

func (m *memStore) Delete(_ context.Context, id calendar.ID, fence namespace.Fence, e audit.Entry) error {
	if id == calendar.DefaultID {
		return fmt.Errorf("delete the default calendar: %w", calendar.ErrInvalid)
	}

	err := m.Write(fence, m.rowScope(id), []audit.Entry{e}, func() error {
		for _, used := range m.assigns {
			if used == id {
				return fmt.Errorf("delete %s: %w", id, calendar.ErrInUse)
			}
		}

		delete(m.cals, id)

		return nil
	})

	return scopeErr(err, calendar.ErrNotFound)
}

func (m *memStore) Assign(_ context.Context, a calendar.Assignment, fence namespace.Fence, e audit.Entry) error {
	if err := a.Scope.Validate(); err != nil {
		return err
	}

	err := m.Write(fence, opsmem.Scope(a.Scope.WriteScope()), []audit.Entry{e}, func() error {
		if a.Calendar == "" {
			delete(m.assigns, a.Scope)

			return nil
		}

		r, ok := m.cals[a.Calendar]
		if !ok && a.Calendar != calendar.DefaultID {
			return fmt.Errorf("assign %s: %w", a.Calendar, calendar.ErrNotFound)
		}

		if ok && r.cal.Owner.Namespace != "" && r.cal.Owner.Namespace != a.Scope.Namespace {
			return fmt.Errorf(
				"assign %s: %w: the calendar belongs to another namespace",
				a.Calendar,
				calendar.ErrInvalid,
			)
		}

		m.assigns[a.Scope] = a.Calendar

		return nil
	})

	return scopeErr(err, calendar.ErrInvalid)
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
	_ context.Context, id calendar.ID, read calendar.FeedRead, fence namespace.Fence,
) error {
	feedID, holidays, status := read.Feed, read.Holidays, read.Status

	if len(holidays) > calendar.MaxHolidays {
		return fmt.Errorf("replace feed holidays: %w: more than %d holidays", calendar.ErrInvalid, calendar.MaxHolidays)
	}

	if status.Error != "" {
		return fmt.Errorf("replace feed holidays: %w: a good read has no error", calendar.ErrInvalid)
	}

	for _, h := range holidays {
		if err := h.Validate(); err != nil {
			return err
		}

		if h.Source != calendar.SourceFeed || h.Feed != feedID {
			return fmt.Errorf("replace feed holidays: %w: a holiday of another source", calendar.ErrInvalid)
		}
	}

	err := m.Write(fence, m.rowScope(id), nil, func() error {
		r, i, err := m.feed(id, feedID, read.URL)
		if err != nil {
			return err
		}

		status.Holidays = len(holidays)
		r.feeds[feedID] = slices.Clone(holidays)
		r.cal.Feeds[i].Status = status

		return nil
	})

	return scopeErr(err, calendar.ErrNotFound)
}

func (m *memStore) RecordFeedStatus(
	_ context.Context, id calendar.ID, read calendar.FeedRead, fence namespace.Fence,
) error {
	status := read.Status

	if !status.Error.Valid() {
		return fmt.Errorf("record feed status: %w: unknown error class", calendar.ErrInvalid)
	}

	if len(read.Holidays) > 0 {
		return fmt.Errorf("record feed status: %w: a read with holidays", calendar.ErrInvalid)
	}

	err := m.Write(fence, m.rowScope(id), nil, func() error {
		r, i, err := m.feed(id, read.Feed, read.URL)
		if err != nil {
			return err
		}

		kept := r.cal.Feeds[i].Status
		kept.ReadAt, kept.Error = status.ReadAt, status.Error
		r.cal.Feeds[i].Status = kept

		return nil
	})

	return scopeErr(err, calendar.ErrNotFound)
}

// rowScope returns a scope function that looks up the calendar id under
// the mutex and gives the scope of its owner, or ErrNotFound. The default
// calendar is in the server scope also before its first update.
func (m *memStore) rowScope(id calendar.ID) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		r, ok := m.cals[id]

		switch {
		case ok:
			return r.cal.Owner.WriteScope(), nil
		case id == calendar.DefaultID:
			return audit.ServerScope, nil
		default:
			return "", fmt.Errorf("calendar %s: %w", id, calendar.ErrNotFound)
		}
	}
}

// feed returns the row of the calendar id and the index of its feed
// feedID, ErrNotFound, or ErrConflict when the feed has a URL other than
// url. The caller holds the mutex of the domain.
func (m *memStore) feed(id calendar.ID, feedID, url string) (*row, int, error) {
	r, ok := m.cals[id]
	if !ok {
		return nil, 0, fmt.Errorf("calendar %s: %w", id, calendar.ErrNotFound)
	}

	i := slices.IndexFunc(r.cal.Feeds, func(f calendar.Feed) bool { return f.ID == feedID })
	if i < 0 {
		return nil, 0, fmt.Errorf("feed %s of %s: %w", feedID, id, calendar.ErrNotFound)
	}

	if r.cal.Feeds[i].URL != url {
		return nil, 0, fmt.Errorf(
			"feed %s of %s: %w: the URL changed during the read", feedID, id, calendar.ErrConflict,
		)
	}

	return r, i, nil
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

	slices.SortStableFunc(c.Holidays, func(a, b calendar.Holiday) int {
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
