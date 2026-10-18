package eventtest_test

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/event"
	"opendustpan.org/opendustpan/pkg/event/eventtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	eventtest.Run(t, func(*testing.T) eventtest.Harness {
		db := &memStore{
			Domain:     opsmem.New(),
			events:     nil,
			cursors:    map[string]int64{},
			sinks:      map[string]event.Sink{},
			subs:       map[string]event.Subscription{},
			deliveries: map[string]event.Delivery{},
		}

		return eventtest.Harness{Outbox: db, Subscriptions: db, Deliveries: db, Domain: db}
	})
}

// memStore is the reference adapter of the event ports in memory.
type memStore struct {
	*opsmem.Domain

	events     []event.Event
	cursors    map[string]int64
	sinks      map[string]event.Sink
	subs       map[string]event.Subscription
	deliveries map[string]event.Delivery
}

func noFence() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

func (m *memStore) Append(_ context.Context, fence namespace.Fence, events []event.Event) error {
	for i := range events {
		if err := events[i].Validate(); err != nil {
			return err
		}
	}

	return m.Write(fence, nil, func() error {
		for i := range events {
			e := events[i]
			e.Sequence = int64(len(m.events) + 1)
			m.events = append(m.events, e)
		}

		return nil
	})
}

func (m *memStore) After(_ context.Context, seq int64, limit int) ([]event.Event, error) {
	var out []event.Event

	m.Read(func() {
		for i := range m.events {
			if e := m.events[i]; e.Sequence > seq && len(out) < limit {
				out = append(out, e)
			}
		}
	})

	return out, nil
}

func (m *memStore) Cursor(_ context.Context, name string) (int64, error) {
	var c int64

	m.Read(func() { c = m.cursors[name] })

	return c, nil
}

func (m *memStore) MoveCursor(_ context.Context, name string, from, to int64) error {
	return m.Write(noFence(), nil, func() error {
		if m.cursors[name] != from {
			return fmt.Errorf("move cursor %s: %w", name, event.ErrConflict)
		}

		m.cursors[name] = to

		return nil
	})
}

func (m *memStore) PutSink(_ context.Context, s event.Sink, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if m.sinks[s.ID].Version != s.Version {
			return fmt.Errorf("put sink %s: %w", s.ID, event.ErrConflict)
		}

		s.Version++
		m.sinks[s.ID] = s

		return nil
	})
}

func (m *memStore) DeleteSink(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.sinks[id]; !ok {
			return fmt.Errorf("delete sink %s: %w", id, event.ErrNotFound)
		}

		for sid := range m.subs {
			if m.subs[sid].Sink == id {
				return fmt.Errorf("delete sink %s: %w", id, event.ErrInUse)
			}
		}

		delete(m.sinks, id)

		return nil
	})
}

func (m *memStore) Sinks(_ context.Context, scope namespace.Name) ([]event.Sink, error) {
	var out []event.Sink

	m.Read(func() {
		for _, id := range slices.Sorted(maps.Keys(m.sinks)) {
			if s := m.sinks[id]; s.Scope == scope {
				out = append(out, s)
			}
		}
	})

	return out, nil
}

func (m *memStore) PutSubscription(
	_ context.Context,
	s event.Subscription,
	fence namespace.Fence,
	e audit.Entry,
) error {
	if err := s.Filter.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if sk, ok := m.sinks[s.Sink]; !ok || sk.Scope != s.Scope {
			return fmt.Errorf("put subscription %s: sink %s: %w", s.ID, s.Sink, event.ErrNotFound)
		}

		if m.subs[s.ID].Version != s.Version {
			return fmt.Errorf("put subscription %s: %w", s.ID, event.ErrConflict)
		}

		s.Version++
		m.subs[s.ID] = s

		return nil
	})
}

func (m *memStore) DeleteSubscription(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.subs[id]; !ok {
			return fmt.Errorf("delete subscription %s: %w", id, event.ErrNotFound)
		}

		delete(m.subs, id)
		maps.DeleteFunc(m.deliveries, func(_ string, d event.Delivery) bool {
			return d.Subscription == id && d.State == event.DeliveryPending
		})

		return nil
	})
}

func (m *memStore) Subscriptions(_ context.Context, scope namespace.Name) ([]event.Subscription, error) {
	var out []event.Subscription

	m.Read(func() {
		for _, id := range slices.Sorted(maps.Keys(m.subs)) {
			if s := m.subs[id]; s.Scope == scope {
				out = append(out, s)
			}
		}
	})

	return out, nil
}

func (m *memStore) Enqueue(_ context.Context, ds []event.Delivery) error {
	return m.Write(noFence(), nil, func() error {
		for i := range ds {
			d, dup := ds[i], false

			for oid := range m.deliveries {
				old := m.deliveries[oid]
				if old.Event == d.Event && old.Subscription == d.Subscription && old.Contact == d.Contact {
					dup = true
				}
			}

			if !dup {
				d.Version = 1
				m.deliveries[d.ID] = d
			}
		}

		return nil
	})
}

func (m *memStore) Due(_ context.Context, now time.Time, limit int) ([]event.Delivery, error) {
	var out []event.Delivery

	m.Read(func() {
		for id := range m.deliveries {
			if d := m.deliveries[id]; d.State == event.DeliveryPending && !d.NextAt.After(now) {
				out = append(out, d)
			}
		}
	})

	slices.SortFunc(out, func(a, b event.Delivery) int { return a.NextAt.Compare(b.NextAt) })

	return out[:min(limit, len(out))], nil
}

func (m *memStore) Record(_ context.Context, d event.Delivery) error {
	return m.Write(noFence(), nil, func() error {
		old, ok := m.deliveries[d.ID]
		if !ok {
			return fmt.Errorf("record %s: %w", d.ID, event.ErrNotFound)
		}

		if old.Version != d.Version {
			return fmt.Errorf("record %s: %w", d.ID, event.ErrConflict)
		}

		d.Version++
		m.deliveries[d.ID] = d

		return nil
	})
}

func (m *memStore) History(_ context.Context, id string, limit int) ([]event.Delivery, error) {
	var out []event.Delivery

	m.Read(func() {
		for did := range m.deliveries {
			if d := m.deliveries[did]; d.Subscription == id {
				out = append(out, d)
			}
		}
	})

	slices.SortFunc(out, func(a, b event.Delivery) int {
		return cmp.Or(b.NextAt.Compare(a.NextAt), strings.Compare(b.ID, a.ID))
	})

	return out[:min(limit, len(out))], nil
}

func (m *memStore) Redeliver(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return m.Write(fence, []audit.Entry{e}, func() error {
		d, ok := m.deliveries[id]
		if !ok {
			return fmt.Errorf("redeliver %s: %w", id, event.ErrNotFound)
		}

		d.State, d.Attempts, d.LastError = event.DeliveryPending, 0, ""
		d.Version++
		m.deliveries[id] = d

		return nil
	})
}
