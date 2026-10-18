package quotatest_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/quota"
	"opendustpan.org/opendustpan/pkg/quota/quotatest"
)

// memCounter is the reference counter. One mutex stands for the row lock
// of the default adapter.
type memCounter struct {
	mu           sync.Mutex
	fences       map[namespace.Name]int64
	settled      map[quota.Key]int
	reservations map[quota.ReservationID]quota.Reservation
	requests     map[quota.Key][]time.Time
}

func newMemCounter() *memCounter {
	return &memCounter{
		mu:           sync.Mutex{},
		fences:       map[namespace.Name]int64{},
		settled:      map[quota.Key]int{},
		reservations: map[quota.ReservationID]quota.Reservation{},
		requests:     map[quota.Key][]time.Time{},
	}
}

func (m *memCounter) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

func (m *memCounter) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[fence.Namespace]++

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: m.fences[fence.Namespace]}, nil
}

func (m *memCounter) Reserve(_ context.Context, r quota.Reservation, checks []quota.Check,
	fence namespace.Fence,
) (quota.Reservation, error) {
	if err := r.Validate(); err != nil {
		return quota.Reservation{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return quota.Reservation{}, err
	}

	if have, ok := m.reservations[r.ID]; ok {
		if !slices.Equal(have.Keys, r.Keys) {
			return quota.Reservation{}, fmt.Errorf("%w: %s", quota.ErrConflict, r.ID)
		}

		return have, nil
	}

	if x := quota.Evaluate(checks, m.usages(checks, r.At), 1); x != nil {
		return quota.Reservation{}, &quota.ExceededError{Exceeded: x}
	}

	m.reservations[r.ID] = r

	return r, nil
}

func (m *memCounter) Settle(_ context.Context, id quota.ReservationID, fence namespace.Fence) error {
	return m.end(id, fence, quota.ReservationSettled)
}

func (m *memCounter) Release(_ context.Context, id quota.ReservationID, fence namespace.Fence) error {
	return m.end(id, fence, quota.ReservationReleased)
}

func (m *memCounter) Record(_ context.Context, key quota.Key, at time.Time, checks []quota.Check,
	fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if x := quota.Evaluate(checks, m.usages(checks, at), 1); x != nil {
		return &quota.ExceededError{Exceeded: x}
	}

	m.requests[key] = append(m.requests[key], at)

	return nil
}

func (m *memCounter) Usage(_ context.Context, checks []quota.Check, at time.Time) ([]quota.Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.usages(checks, at), nil
}

func (m *memCounter) SetSettled(_ context.Context, key quota.Key, count int, fence namespace.Fence) error {
	if key.Kind != quota.KindUnits || count < 0 {
		return fmt.Errorf("%w: settled count %d of %s", quota.ErrInvalid, count, key)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	m.settled[key] = count

	return nil
}

func (m *memCounter) checkFence(fence namespace.Fence) error {
	if m.fences[fence.Namespace] != fence.FailoverVersion {
		return fmt.Errorf("quota: %w", namespace.ErrFenced)
	}

	return nil
}

func (m *memCounter) usageOf(c quota.Check, at time.Time) quota.Usage {
	u := quota.Usage{Key: c.Key, Settled: 0, Reserved: 0, Requests: 0}

	if c.Key.Kind == quota.KindUnits {
		u.Settled = m.settled[c.Key]

		for _, r := range m.reservations {
			if r.State == quota.ReservationOpen && r.EndsAt.After(at) && slices.Contains(r.Keys, c.Key) {
				u.Reserved++
			}
		}
	}

	if c.Key.Kind == quota.KindRequests {
		for _, t := range m.requests[c.Key] {
			if t.After(at.Add(-c.Window)) && !t.After(at) {
				u.Requests++
			}
		}
	}

	return u
}

func (m *memCounter) usages(checks []quota.Check, at time.Time) []quota.Usage {
	out := make([]quota.Usage, 0, len(checks))
	for _, c := range checks {
		out = append(out, m.usageOf(c, at))
	}

	return out
}

func (m *memCounter) end(id quota.ReservationID, fence namespace.Fence, to quota.ReservationState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	r, ok := m.reservations[id]

	switch {
	case !ok:
		return fmt.Errorf("%w: %s", quota.ErrNotFound, id)
	case r.State == to:
		return nil
	case r.State != quota.ReservationOpen:
		return fmt.Errorf("%w: %s is %s", quota.ErrEnded, id, r.State)
	default:
	}

	r.State = to
	m.reservations[id] = r

	if to == quota.ReservationSettled {
		for _, k := range r.Keys {
			m.settled[k]++
		}
	}

	return nil
}

func TestMemCounterConformance(t *testing.T) {
	t.Parallel()

	quotatest.Run(t, func(_ *testing.T) quotatest.Harness {
		c := newMemCounter()

		return quotatest.Harness{Counter: c, Domain: c}
	})
}
