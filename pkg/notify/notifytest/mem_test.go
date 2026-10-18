package notifytest_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	notifytest.Run(t, func(*testing.T) notifytest.Harness {
		db := &memStore{
			Domain:        opsmem.New(),
			notifications: nil,
			sequences:     map[namespace.Name]int64{},
			cursors:       map[cursorKey]int64{},
			sinks:         map[string]notify.Sink{},
			subs:          map[string]notify.Subscription{},
			deliveries:    map[string]notify.Delivery{},
		}

		return notifytest.Harness{Outbox: db, Subscriptions: db, Deliveries: db, Domain: db}
	})
}

type cursorKey struct {
	name  string
	scope namespace.Name
}

// memStore is the reference adapter of the notification ports in memory.
type memStore struct {
	*opsmem.Domain

	notifications []notify.Notification
	sequences     map[namespace.Name]int64
	cursors       map[cursorKey]int64
	sinks         map[string]notify.Sink
	subs          map[string]notify.Subscription
	deliveries    map[string]notify.Delivery
}

// scoped translates the error of a fence of another scope into sentinel.
func scoped(err, sentinel error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%w: %w", sentinel, err)
	}

	return err
}

// common returns a scope function for rows that all have one scope, and
// ErrInvalid for rows of two scopes.
func common(scopes []namespace.Name) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		for _, s := range scopes {
			if s != scopes[0] {
				return "", fmt.Errorf("%w: rows of two scopes", notify.ErrInvalid)
			}
		}

		return scopes[0], nil
	}
}

func (m *memStore) Append(_ context.Context, fence namespace.Fence, ns []notify.Notification) error {
	if len(ns) == 0 {
		return nil
	}

	scopes := make([]namespace.Name, 0, len(ns))

	for i := range ns {
		if err := ns[i].Validate(); err != nil {
			return err
		}

		scopes = append(scopes, ns[i].Scope)
	}

	return scoped(m.Write(fence, common(scopes), nil, func() error {
		for i := range ns {
			n := ns[i]
			if slices.ContainsFunc(m.notifications, func(o notify.Notification) bool {
				return o.Scope == n.Scope && o.DedupeKey == n.DedupeKey
			}) {
				continue
			}

			m.sequences[n.Scope]++
			n.Sequence = m.sequences[n.Scope]
			m.notifications = append(m.notifications, n)
		}

		return nil
	}), notify.ErrInvalid)
}

func (m *memStore) Scopes(_ context.Context, name string) ([]namespace.Name, error) {
	var out []namespace.Name

	m.Read(func() {
		for i := range m.notifications {
			n := m.notifications[i]
			if n.Sequence > m.cursors[cursorKey{name, n.Scope}] && !slices.Contains(out, n.Scope) {
				out = append(out, n.Scope)
			}
		}
	})

	slices.Sort(out)

	return out, nil
}

func (m *memStore) After(_ context.Context, scope namespace.Name, seq int64, limit int) ([]notify.Notification, error) {
	var out []notify.Notification

	m.Read(func() {
		for i := range m.notifications {
			if n := m.notifications[i]; n.Scope == scope && n.Sequence > seq && len(out) < limit {
				out = append(out, n)
			}
		}
	})

	return out, nil
}

func (m *memStore) Cursor(_ context.Context, name string, scope namespace.Name) (int64, error) {
	var c int64

	m.Read(func() { c = m.cursors[cursorKey{name, scope}] })

	return c, nil
}

func (m *memStore) MoveCursor(_ context.Context, fence namespace.Fence, mv notify.CursorMove) error {
	return scoped(m.Write(fence, opsmem.Scope(mv.Scope), nil, func() error {
		k := cursorKey{mv.Name, mv.Scope}
		if m.cursors[k] != mv.From {
			return fmt.Errorf("move cursor %s: %w", mv.Name, notify.ErrConflict)
		}

		m.cursors[k] = mv.To

		return nil
	}), notify.ErrInvalid)
}

func (m *memStore) PutSink(_ context.Context, s notify.Sink, fence namespace.Fence, e audit.Entry) error {
	if err := s.Validate(); err != nil {
		return err
	}

	return scoped(m.Write(fence, opsmem.Scope(s.Scope), []audit.Entry{e}, func() error {
		old, ok := m.sinks[s.ID]
		if ok && old.Scope != s.Scope {
			return fmt.Errorf("put sink %s: %w", s.ID, notify.ErrNotFound)
		}

		if old.Version != s.Version {
			return fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
		}

		s.Version++
		m.sinks[s.ID] = s

		return nil
	}), notify.ErrInvalid)
}

func (m *memStore) DeleteSink(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return scoped(m.Write(fence, m.sinkScope(id), []audit.Entry{e}, func() error {
		for sid := range m.subs {
			if m.subs[sid].Sink == id {
				return fmt.Errorf("delete sink %s: %w", id, notify.ErrInUse)
			}
		}

		delete(m.sinks, id)

		return nil
	}), notify.ErrNotFound)
}

func (m *memStore) Sinks(_ context.Context, scope namespace.Name) ([]notify.Sink, error) {
	var out []notify.Sink

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
	s notify.Subscription,
	fence namespace.Fence,
	e audit.Entry,
) error {
	if err := s.Filter.Validate(); err != nil {
		return err
	}

	return scoped(m.Write(fence, opsmem.Scope(s.Scope), []audit.Entry{e}, func() error {
		if sk, ok := m.sinks[s.Sink]; !ok || sk.Scope != s.Scope {
			return fmt.Errorf("put subscription %s: sink %s: %w", s.ID, s.Sink, notify.ErrNotFound)
		}

		old, ok := m.subs[s.ID]
		if ok && old.Scope != s.Scope {
			return fmt.Errorf("put subscription %s: %w", s.ID, notify.ErrNotFound)
		}

		if old.Version != s.Version {
			return fmt.Errorf("put subscription %s: %w", s.ID, notify.ErrConflict)
		}

		s.Version++
		m.subs[s.ID] = s

		return nil
	}), notify.ErrInvalid)
}

func (m *memStore) DeleteSubscription(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	scope := func() (namespace.Name, error) {
		s, ok := m.subs[id]
		if !ok {
			return "", fmt.Errorf("subscription %s: %w", id, notify.ErrNotFound)
		}

		return s.Scope, nil
	}

	return scoped(m.Write(fence, scope, []audit.Entry{e}, func() error {
		delete(m.subs, id)
		maps.DeleteFunc(m.deliveries, func(_ string, d notify.Delivery) bool {
			return d.Subscription == id && d.State == notify.DeliveryPending
		})

		return nil
	}), notify.ErrNotFound)
}

func (m *memStore) Subscriptions(_ context.Context, scope namespace.Name) ([]notify.Subscription, error) {
	var out []notify.Subscription

	m.Read(func() {
		for _, id := range slices.Sorted(maps.Keys(m.subs)) {
			if s := m.subs[id]; s.Scope == scope {
				out = append(out, s)
			}
		}
	})

	return out, nil
}

func (m *memStore) Enqueue(_ context.Context, fence namespace.Fence, ds []notify.Delivery) error {
	if len(ds) == 0 {
		return nil
	}

	scopes := make([]namespace.Name, 0, len(ds))
	for i := range ds {
		scopes = append(scopes, ds[i].Scope)
	}

	return scoped(m.Write(fence, common(scopes), nil, func() error {
		for i := range ds {
			d := ds[i]
			if slices.ContainsFunc(slices.Collect(maps.Values(m.deliveries)), func(o notify.Delivery) bool {
				return o.Notification == d.Notification && o.Subscription == d.Subscription && o.Contact == d.Contact
			}) {
				continue
			}

			d.Owner, d.LeaseUntil, d.Version = "", time.Time{}, 1
			m.deliveries[d.ID] = d
		}

		return nil
	}), notify.ErrInvalid)
}

// claimable reports whether Claim can take d at now.
func claimable(d notify.Delivery, now time.Time) bool {
	return d.State == notify.DeliveryPending && !d.NextAt.After(now) && !d.LeaseUntil.After(now)
}

func (m *memStore) DueScopes(_ context.Context, now time.Time) ([]namespace.Name, error) {
	var out []namespace.Name

	m.Read(func() {
		for id := range m.deliveries {
			if d := m.deliveries[id]; claimable(d, now) && !slices.Contains(out, d.Scope) {
				out = append(out, d.Scope)
			}
		}
	})

	slices.Sort(out)

	return out, nil
}

func (m *memStore) Claim(_ context.Context, fence namespace.Fence, req notify.ClaimRequest) ([]notify.Delivery, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	var out []notify.Delivery

	err := m.Write(fence, opsmem.Scope(req.Scope), nil, func() error {
		for id := range m.deliveries {
			if d := m.deliveries[id]; d.Scope == req.Scope && claimable(d, req.Now) {
				out = append(out, d)
			}
		}

		slices.SortFunc(out, func(a, b notify.Delivery) int {
			return cmp.Or(a.NextAt.Compare(b.NextAt), strings.Compare(a.ID, b.ID))
		})
		out = out[:min(req.Limit, len(out))]

		for i := range out {
			out[i].Owner, out[i].LeaseUntil = req.Owner, req.Now.Add(req.Lease)
			out[i].Version++
			m.deliveries[out[i].ID] = out[i]
		}

		return nil
	})
	if err != nil {
		return nil, scoped(err, notify.ErrInvalid)
	}

	return out, nil
}

func (m *memStore) Record(_ context.Context, fence namespace.Fence, d notify.Delivery) error {
	return scoped(m.Write(fence, m.deliveryScope(d.ID), nil, func() error {
		old := m.deliveries[d.ID]
		if old.Version != d.Version {
			return fmt.Errorf("record %s: %w", d.ID, notify.ErrConflict)
		}

		d.Scope, d.Owner, d.LeaseUntil = old.Scope, "", time.Time{}
		d.Version++
		m.deliveries[d.ID] = d

		return nil
	}), notify.ErrNotFound)
}

func (m *memStore) History(_ context.Context, id string, limit int) ([]notify.Delivery, error) {
	var out []notify.Delivery

	m.Read(func() {
		for did := range m.deliveries {
			if d := m.deliveries[did]; d.Subscription == id {
				out = append(out, d)
			}
		}
	})

	slices.SortFunc(out, func(a, b notify.Delivery) int {
		return cmp.Or(b.NextAt.Compare(a.NextAt), strings.Compare(b.ID, a.ID))
	})

	return out[:min(limit, len(out))], nil
}

func (m *memStore) Redeliver(_ context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	return scoped(m.Write(fence, m.deliveryScope(id), []audit.Entry{e}, func() error {
		d := m.deliveries[id]
		d.State, d.Attempts, d.LastError = notify.DeliveryPending, 0, ""
		d.Owner, d.LeaseUntil = "", time.Time{}
		d.Version++
		m.deliveries[id] = d

		return nil
	}), notify.ErrNotFound)
}

// sinkScope returns the scope function of the stored sink id.
func (m *memStore) sinkScope(id string) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		s, ok := m.sinks[id]
		if !ok {
			return "", fmt.Errorf("sink %s: %w", id, notify.ErrNotFound)
		}

		return s.Scope, nil
	}
}

// deliveryScope returns the scope function of the stored delivery id.
func (m *memStore) deliveryScope(id string) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		d, ok := m.deliveries[id]
		if !ok {
			return "", fmt.Errorf("delivery %s: %w", id, notify.ErrNotFound)
		}

		return d.Scope, nil
	}
}
