// Package authgatetest holds an in-memory authgate.FailureCounter for the
// tests of the gate and of the conformance suite.
package authgatetest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
)

// window names one count: a source and the end of its window in Unix
// milliseconds, as the SQL stores keep it.
type window struct {
	source string
	end    int64
}

// Memory is an authgate.FailureCounter in memory. Its clock acts as the
// clock of the store: every handle on one Memory reads it. It counts the
// calls of AddFailure, so a test can prove that a step writes nothing. The
// zero value is not usable: call NewMemory.
type Memory struct {
	now    func() time.Time
	counts map[window]int64
	adds   int
	mu     sync.Mutex
}

var _ authgate.FailureCounter = (*Memory)(nil)

// NewMemory returns an empty Memory with the clock now. A nil now means
// time.Now.
func NewMemory(now func() time.Time) *Memory {
	if now == nil {
		now = time.Now
	}

	return &Memory{now: now, counts: map[window]int64{}, adds: 0, mu: sync.Mutex{}}
}

// AddFailure implements authgate.FailureCounter. A done ctx fails the add,
// as it fails a write to a SQL store.
func (m *Memory) AddFailure(ctx context.Context, source string, length time.Duration) (authgate.FailureCount, error) {
	none := authgate.FailureCount{Count: 0, At: time.Time{}, End: time.Time{}}

	if length < time.Millisecond {
		return none, authgate.ErrInvalidWindow
	}

	if err := ctx.Err(); err != nil {
		return none, fmt.Errorf("add failure: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.adds++
	atMs := m.now().UnixMilli()
	lengthMs := length.Milliseconds()
	key := window{source: source, end: (atMs/lengthMs + 1) * lengthMs}
	m.counts[key]++

	return authgate.FailureCount{
		Count: m.counts[key],
		At:    time.UnixMilli(atMs),
		End:   time.UnixMilli(key.end),
	}, nil
}

// PruneFailures implements authgate.FailureCounter.
func (m *Memory) PruneFailures(ctx context.Context, age time.Duration) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("prune failures: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	before := m.now().UnixMilli() - age.Milliseconds()

	var deleted int64

	for key := range m.counts {
		if key.end <= before {
			delete(m.counts, key)
			deleted++
		}
	}

	return deleted, nil
}

// Adds answers the count of AddFailure calls.
func (m *Memory) Adds() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.adds
}

// Count answers the count of source in the window that ends at end, or 0.
func (m *Memory) Count(source string, end time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.counts[window{source: source, end: end.UnixMilli()}]
}

// Windows answers the count of stored windows.
func (m *Memory) Windows() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.counts)
}
