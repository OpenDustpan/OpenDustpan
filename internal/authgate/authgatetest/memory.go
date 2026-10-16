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

// window names one counter: a source and the start of its window in Unix
// milliseconds, as the SQL stores keep it.
type window struct {
	source string
	start  int64
}

// Memory is an authgate.FailureCounter in memory. It counts the calls of
// AddFailure, so a test can prove that a step writes nothing. The zero
// value is not usable: call NewMemory.
type Memory struct {
	counts map[window]int64
	adds   int
	mu     sync.Mutex
}

var _ authgate.FailureCounter = (*Memory)(nil)

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{counts: map[window]int64{}, adds: 0, mu: sync.Mutex{}}
}

// AddFailure implements authgate.FailureCounter. A done ctx fails the
// add, as it fails a write to a SQL store.
func (m *Memory) AddFailure(ctx context.Context, source string, start time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("add failure: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.adds++
	key := window{source: source, start: start.UnixMilli()}
	m.counts[key]++

	return m.counts[key], nil
}

// Failures implements authgate.FailureCounter.
func (m *Memory) Failures(_ context.Context, source string, start time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.counts[window{source: source, start: start.UnixMilli()}], nil
}

// PruneFailures implements authgate.FailureCounter.
func (m *Memory) PruneFailures(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted int64

	for key := range m.counts {
		if key.start < before.UnixMilli() {
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

// Windows answers the count of stored windows.
func (m *Memory) Windows() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.counts)
}
