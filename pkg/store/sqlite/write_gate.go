package sqlite

import (
	"context"
	"fmt"
	"sync"
)

// writeGate orders an enter of maintenance mode against the checked writes of
// a store. It is a reader/writer lock that honors a context: a checked write
// holds it shared, and an enter holds it exclusive. A waiter that its context
// ends stops waiting and leaves nothing behind that blocks the others.
//
// Like sync.RWMutex, a waiting enter blocks the new checked writes, so the
// writes in flight end, the new writes wait, and a steady stream of writes
// cannot starve the enter.
//
// Lock order: Store.mu (shared) before the gate, and nothing takes them in
// the other order. Store.stagingMu comes before Store.mu. The gate is never
// held across a wait for any other lock of the store, except the database
// write lock that the transaction under it takes.
type writeGate struct {
	// turn is the queue: whoever holds the token is the next to decide.
	// A reader holds it for the moment it takes to count itself in. An
	// enter holds it from the start of its wait to its release, so the new
	// readers wait behind it.
	turn chan struct{}
	// mu guards readers and drained.
	mu      sync.Mutex
	readers int
	// drained is closed when readers reaches zero, while an enter waits.
	drained chan struct{}
	// onBlock, when set, runs each time a caller is about to wait: a reader
	// or an enter for the turn token, and an enter for the readers to end.
	// Tests set it to learn, with no sleep, that a caller is parked. Nil in
	// production.
	onBlock func()
}

func newWriteGate() *writeGate {
	return &writeGate{
		turn:    make(chan struct{}, 1),
		mu:      sync.Mutex{},
		readers: 0,
		drained: nil,
		onBlock: nil,
	}
}

// acquireTurn takes the turn token, or returns the error of ctx.
func (g *writeGate) acquireTurn(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("wait for the write gate: %w", err)
	}

	select {
	case g.turn <- struct{}{}:
		return nil
	default:
	}

	if g.onBlock != nil {
		g.onBlock()
	}

	select {
	case g.turn <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for the write gate: %w", ctx.Err())
	}
}

// rlock takes the gate shared and returns the function that releases it. It
// returns the error of ctx when ctx ends first.
func (g *writeGate) rlock(ctx context.Context) (func(), error) {
	if err := g.acquireTurn(ctx); err != nil {
		return nil, err
	}

	g.mu.Lock()
	g.readers++
	g.mu.Unlock()

	<-g.turn

	return g.runlock, nil
}

func (g *writeGate) runlock() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.readers--

	if g.readers == 0 && g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
}

// lock takes the gate exclusive and returns the function that releases it.
// It waits for the shared holds in flight. It returns the error of ctx, and
// holds nothing, when ctx ends first.
func (g *writeGate) lock(ctx context.Context) (func(), error) {
	if err := g.acquireTurn(ctx); err != nil {
		return nil, err
	}

	g.mu.Lock()

	if g.readers == 0 {
		g.mu.Unlock()

		return g.unlock, nil
	}

	drained := make(chan struct{})
	g.drained = drained
	g.mu.Unlock()

	if g.onBlock != nil {
		g.onBlock()
	}

	select {
	case <-drained:
		return g.unlock, nil
	case <-ctx.Done():
		g.mu.Lock()
		if g.drained == drained {
			g.drained = nil
		}
		g.mu.Unlock()

		g.unlock()

		return nil, fmt.Errorf("wait for the write gate: %w", ctx.Err())
	}
}

func (g *writeGate) unlock() {
	<-g.turn
}
