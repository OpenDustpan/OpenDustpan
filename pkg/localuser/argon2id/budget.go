package argon2id

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/localuser"
)

// waiter is one Acquire call that waits for memory.
type waiter struct {
	// ready is closed when the budget gives the waiter its memory.
	ready chan struct{}
	// kib is the memory that the waiter asks for, in KiB.
	kib uint32
	// granted is true when the budget has given the waiter its memory.
	granted bool
}

// Budget is the memory that the password hashes of one instance can use at
// the same time, in KiB. Each hash takes its own memory cost from it and
// gives it back when it ends. A hash that finds no room, or finds other
// hashes that wait, joins the end of the list of waiters and waits at most
// the wait of the budget, and then fails with localuser.ErrHashBusy.
type Budget struct {
	// waiters are the Acquire calls that wait, first come first.
	waiters  []*waiter
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
		waiters:  nil,
		mu:       sync.Mutex{},
		wait:     wait,
		capacity: capacityKiB,
		used:     0,
	}, nil
}

// Acquire takes kib from the budget. It serves the waiters in the order that
// they called it: a call that finds waiters joins the end of the list, also
// when the free memory is enough for it. It waits until the budget gives it
// memory, the wait of the budget ends (localuser.ErrHashBusy), or ctx ends
// (the error of ctx). It returns localuser.ErrInvalid for 0 or for more than
// the capacity. The caller calls the returned function once, when the hash
// ends.
func (b *Budget) Acquire(ctx context.Context, kib uint32) (func(), error) {
	if kib == 0 || kib > b.capacity {
		return nil, fmt.Errorf("%w: a hash of %d KiB in a budget of %d KiB", localuser.ErrInvalid, kib, b.capacity)
	}

	b.mu.Lock()

	if len(b.waiters) == 0 && b.capacity-b.used >= kib {
		b.used += kib
		b.mu.Unlock()

		return b.releaser(kib), nil
	}

	w := &waiter{ready: make(chan struct{}), kib: kib, granted: false}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	timer := time.NewTimer(b.wait)
	defer timer.Stop()

	select {
	case <-w.ready:
		return b.releaser(kib), nil
	case <-timer.C:
		b.leave(w)

		return nil, fmt.Errorf("%w: waited %s for %d KiB", localuser.ErrHashBusy, b.wait, kib)
	case <-ctx.Done():
		b.leave(w)

		return nil, fmt.Errorf("wait for the hash memory budget: %w", ctx.Err())
	}
}

// InUse returns the KiB that hashes hold now.
func (b *Budget) InUse() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.used
}

// serve gives memory to the waiters from the head of the list while the head
// fits. The head waits for room, so a small hash never passes a larger hash
// that came first. The caller holds b.mu.
func (b *Budget) serve() {
	for len(b.waiters) > 0 {
		head := b.waiters[0]
		if b.capacity-b.used < head.kib {
			return
		}

		b.used += head.kib
		head.granted = true
		close(head.ready)
		// slices.Delete clears the slot that it frees, so the list keeps no
		// reference to a served waiter.
		b.waiters = slices.Delete(b.waiters, 0, 1)
	}
}

// leave takes w out of the budget after its wait ended. When the budget gave
// w its memory at the same time, leave gives that memory back. Then it serves
// the waiters that fit, so a waiter that leaves never blocks the waiters
// behind it.
func (b *Budget) leave(w *waiter) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if w.granted {
		b.used -= w.kib
	} else {
		b.waiters = slices.DeleteFunc(b.waiters, func(o *waiter) bool { return o == w })
	}

	b.serve()
}

// releaser returns the function that gives kib back once. A second call does
// nothing, so a caller bug cannot give back memory that another hash holds.
func (b *Budget) releaser(kib uint32) func() {
	var once sync.Once

	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.used -= kib
			b.serve()
			b.mu.Unlock()
		})
	}
}
