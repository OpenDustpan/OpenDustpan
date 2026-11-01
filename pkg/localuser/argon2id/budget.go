package argon2id

import (
	"context"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/localuser"
)

// Budget is the memory that the password hashes of one instance can use at
// the same time, in KiB. Each hash takes its own memory cost from it and
// gives it back when it ends. A hash that finds no room waits at most the
// wait of the budget, and then fails with localuser.ErrHashBusy.
type Budget struct {
	// changed is closed and replaced at each release, so that each waiter
	// looks again.
	changed  chan struct{}
	mu       sync.Mutex
	wait     time.Duration
	capacity uint32
	used     uint32
}

// NewBudget returns a budget of capacityKiB with the wait
// localuser.HashWait. It returns localuser.ErrInvalid below
// localuser.MinHashMemoryBudgetKiB, so one hash of any valid hash always
// fits.
func NewBudget(capacityKiB uint32) (*Budget, error) {
	return NewBudgetWithWait(capacityKiB, localuser.HashWait)
}

// NewBudgetWithWait is NewBudget with another wait. A wait of zero or less
// fails at once when the budget has no room. Tests use it to make the wait
// short.
func NewBudgetWithWait(capacityKiB uint32, wait time.Duration) (*Budget, error) {
	if capacityKiB < localuser.MinHashMemoryBudgetKiB {
		return nil, fmt.Errorf(
			"%w: hash memory budget %d KiB, want at least %d",
			localuser.ErrInvalid, capacityKiB, localuser.MinHashMemoryBudgetKiB,
		)
	}

	return &Budget{
		changed:  make(chan struct{}),
		mu:       sync.Mutex{},
		wait:     wait,
		capacity: capacityKiB,
		used:     0,
	}, nil
}

// Acquire takes kib from the budget. It waits until the budget has room, the
// wait of the budget ends (localuser.ErrHashBusy), or ctx ends (the error of
// ctx). It returns localuser.ErrInvalid for 0 or for more than the capacity.
// The caller calls the returned function once, when the hash ends.
func (b *Budget) Acquire(ctx context.Context, kib uint32) (func(), error) {
	if kib == 0 || kib > b.capacity {
		return nil, fmt.Errorf("%w: a hash of %d KiB in a budget of %d KiB", localuser.ErrInvalid, kib, b.capacity)
	}

	timer := time.NewTimer(b.wait)
	defer timer.Stop()

	for {
		b.mu.Lock()

		if b.capacity-b.used >= kib {
			b.used += kib
			b.mu.Unlock()

			return b.releaser(kib), nil
		}

		changed := b.changed
		b.mu.Unlock()

		select {
		case <-changed:
		case <-timer.C:
			return nil, fmt.Errorf("%w: waited %s for %d KiB", localuser.ErrHashBusy, b.wait, kib)
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for the hash memory budget: %w", ctx.Err())
		}
	}
}

// InUse returns the KiB that hashes hold now.
func (b *Budget) InUse() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.used
}

// releaser returns the function that gives kib back once. A second call does
// nothing, so a caller bug cannot give back memory that another hash holds.
func (b *Budget) releaser(kib uint32) func() {
	var once sync.Once

	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.used -= kib
			close(b.changed)
			b.changed = make(chan struct{})
			b.mu.Unlock()
		})
	}
}
