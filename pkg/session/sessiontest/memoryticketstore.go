package sessiontest

import (
	"context"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// MemoryTicketStore is the reference session.TicketStore for tests. It keeps
// the records in memory, so it is not shared across processes. Production
// code uses the SQL stores.
type MemoryTicketStore struct {
	used map[memoryTicketKey]time.Time
	mu   sync.Mutex
}

var _ session.TicketStore = (*MemoryTicketStore)(nil)

// memoryTicketKey is the key of a record of MemoryTicketStore.
type memoryTicketKey struct {
	providerID string
	digest     session.TicketDigest
}

// NewMemoryTicketStore returns an empty MemoryTicketStore.
func NewMemoryTicketStore() *MemoryTicketStore {
	return &MemoryTicketStore{used: map[memoryTicketKey]time.Time{}, mu: sync.Mutex{}}
}

// UseTicket implements session.TicketStore.
func (m *MemoryTicketStore) UseTicket(
	_ context.Context, providerID string, d session.TicketDigest, at, until time.Time,
) error {
	if err := session.ValidateTicketUse(providerID, d, at, until); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := memoryTicketKey{providerID: providerID, digest: d}
	if end, ok := m.used[key]; ok && end.After(at) {
		return session.ErrReplay
	}

	m.used[key] = until

	return nil
}

// PruneTickets implements session.TicketStore.
func (m *MemoryTicketStore) PruneTickets(_ context.Context, at time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var n int64

	for k, end := range m.used {
		if !end.After(at) {
			delete(m.used, k)

			n++
		}
	}

	return n, nil
}
