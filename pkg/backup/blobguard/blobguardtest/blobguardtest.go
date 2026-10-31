// Package blobguardtest holds fakes of the guard of the blob deletes for the
// tests of deleters.
package blobguardtest

import (
	"context"
	"sync"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
)

// None is a guard that finds nothing running.
type None struct{}

var _ blobguard.Watch = None{}

// Running implements blobguard.Watch.
func (None) Running(context.Context) (blobguard.Work, bool, error) {
	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// Guard is a guard that a test controls. The zero value finds nothing
// running. It is safe for concurrent use.
type Guard struct {
	mu    sync.Mutex
	work  blobguard.Work
	on    bool
	err   error
	calls int
	hook  func()
}

// NewGuard returns a guard that finds nothing running.
func NewGuard() *Guard {
	return &Guard{mu: sync.Mutex{}, work: blobguard.Work{Kind: "", ID: ""}, on: false, err: nil, calls: 0, hook: nil}
}

var _ blobguard.Watch = (*Guard)(nil)

// Start makes the guard report work as running.
func (g *Guard) Start(work blobguard.Work) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.work, g.on = work, true
}

// End makes the guard report nothing running.
func (g *Guard) End() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.work, g.on = blobguard.Work{Kind: "", ID: ""}, false
}

// Fail makes every read fail with err. A nil err ends the failure.
func (g *Guard) Fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.err = err
}

// OnRead sets a function that runs at the start of each read, before the
// answer. A test uses it to record the order of calls or to start a backup
// at a chosen point.
func (g *Guard) OnRead(f func()) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.hook = f
}

// Calls returns the number of reads.
func (g *Guard) Calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.calls
}

// Running implements blobguard.Watch.
func (g *Guard) Running(context.Context) (blobguard.Work, bool, error) {
	g.mu.Lock()
	g.calls++
	hook := g.hook
	g.mu.Unlock()

	if hook != nil {
		hook()
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.err != nil {
		return blobguard.Work{Kind: "", ID: ""}, false, g.err
	}

	return g.work, g.on, nil
}
