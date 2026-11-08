// Package blobguardtest holds fakes of the guard of the blob deletes for the
// tests of deleters.
package blobguardtest

import (
	"context"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
)

// None is a guard that finds nothing running and holds no lease.
type None struct{}

var (
	_ blobguard.Watch  = None{}
	_ blobguard.Leases = None{}
)

// Running implements blobguard.Watch.
func (None) Running(context.Context) (blobguard.Work, bool, error) {
	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// TakeLease implements blobguard.Leases: it writes nothing and reports a
// written lease, as a guard that finds nothing running does.
func (None) TakeLease(context.Context, string, time.Duration) (blobguard.Work, bool, error) {
	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// ReleaseLease implements blobguard.Leases: it holds no row, so it has none
// to delete.
func (None) ReleaseLease(context.Context, string) error { return nil }

// LiveLeases implements blobguard.Leases: it holds no lease.
func (None) LiveLeases(context.Context) (int, error) { return 0, nil }

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

// Leases is a guard that also holds the leases of the blob deleters, for the
// tests of a deleter that takes a lease before each delete. It answers
// blobguard.Watch as its Guard does, and it keeps one lease row for each
// holder on an injected clock. It is safe for concurrent use.
type Leases struct {
	*Guard

	leaseMu sync.Mutex
	now     func() time.Time
	rows    map[string]time.Time
	takes   int
}

// NewLeases returns a lease guard that finds nothing running and reads the
// time from now.
func NewLeases(now func() time.Time) *Leases {
	return &Leases{
		Guard:   NewGuard(),
		leaseMu: sync.Mutex{},
		now:     now,
		rows:    map[string]time.Time{},
		takes:   0,
	}
}

var _ blobguard.Leases = (*Leases)(nil)

// Takes returns the number of takes that a deleter asked for, including the
// takes that the running work refused.
func (l *Leases) Takes() int {
	l.leaseMu.Lock()
	defer l.leaseMu.Unlock()

	return l.takes
}

// TakeLease implements blobguard.Leases: it counts the take, writes the lease
// of holder, and it writes nothing and answers the work of the Guard while the
// Guard reports work.
func (l *Leases) TakeLease(
	ctx context.Context,
	holder string,
	ttl time.Duration,
) (blobguard.Work, bool, error) {
	l.leaseMu.Lock()
	l.takes++
	l.leaseMu.Unlock()

	work, ok, err := l.Running(ctx)
	if err != nil || ok {
		return work, ok, err
	}

	l.leaseMu.Lock()
	defer l.leaseMu.Unlock()

	l.rows[holder] = l.now().Add(ttl)

	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// ReleaseLease implements blobguard.Leases: a holder with no row is not an
// error.
func (l *Leases) ReleaseLease(_ context.Context, holder string) error {
	l.leaseMu.Lock()
	defer l.leaseMu.Unlock()

	delete(l.rows, holder)

	return nil
}

// LiveLeases implements blobguard.Leases: the number of rows that end after
// the current time.
func (l *Leases) LiveLeases(context.Context) (int, error) {
	l.leaseMu.Lock()
	defer l.leaseMu.Unlock()

	now := l.now()
	live := 0

	for _, expires := range l.rows {
		if expires.After(now) {
			live++
		}
	}

	return live, nil
}
