package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// gateWait bounds each wait of the tests below. It is a safety net against a
// hung test, not a synchronization: every step waits for a channel.
const gateWait = 30 * time.Second

func recvWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(gateWait):
		t.Fatalf("timed out waiting for %s", what)
	}

	var zero T

	return zero
}

// gateCall is a call of the store that a test starts in its own goroutine.
type gateCall struct {
	name string
	run  func(ctx context.Context, s *Store, reached func()) error
}

// gateWrites returns the checked write paths: the transaction helper and the
// runner of the job store.
func gateWrites() []gateCall {
	return []gateCall{
		{"inTx", func(ctx context.Context, s *Store, reached func()) error {
			return s.inTx(ctx, func(*db.Queries) error {
				reached()

				return nil
			})
		}},
		{"job write", func(ctx context.Context, s *Store, reached func()) error {
			fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

			return jobRunner{s: s}.Write(ctx, fence, func(jobstore.Tx) error {
				reached()

				return nil
			})
		}},
	}
}

// gateEnters returns the two ways to enter maintenance mode.
func gateEnters() []gateCall {
	return []gateCall{
		{"SetState", func(ctx context.Context, s *Store, _ func()) error {
			return s.SetState(ctx, maintenance.StateMaintenance)
		}},
		{"SetStateIf", func(ctx context.Context, s *Store, _ func()) error {
			return s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance)
		}},
	}
}

// gateRig is a store with a gate that reports each wait.
type gateRig struct {
	t       *testing.T
	s       *Store
	blocked chan struct{}
}

func newGateRig(t *testing.T) *gateRig {
	t.Helper()

	r := &gateRig{t: t, s: openInternalTemplated(t), blocked: make(chan struct{}, 8)}
	r.s.enterGate.onBlock = func() { r.blocked <- struct{}{} }

	return r
}

// waitBlocked returns when a caller of the gate starts to wait.
func (r *gateRig) waitBlocked(what string) {
	r.t.Helper()

	recvWithin(r.t, r.blocked, what)
}

// running is a call that the rig started.
type running struct {
	done    chan error
	reached chan struct{}
}

// start runs c in a goroutine.
func (r *gateRig) start(ctx context.Context, c gateCall) running {
	run := running{done: make(chan error, 1), reached: make(chan struct{}, 1)}

	go func() { run.done <- c.run(ctx, r.s, func() { run.reached <- struct{}{} }) }()

	return run
}

// wait returns the result of the call.
func (r *gateRig) wait(run running, what string) error {
	r.t.Helper()

	return recvWithin(r.t, run.done, what)
}

// assertPending fails when the call has returned or has run its body.
func assertPending(t *testing.T, run running, what string) {
	t.Helper()

	select {
	case err := <-run.done:
		t.Fatalf("%s returned %v, want it to wait", what, err)
	case <-run.reached:
		t.Fatalf("%s ran its body, want it to wait", what)
	default:
	}
}

// assertNotReached fails when the call has run its body.
func assertNotReached(t *testing.T, run running, what string) {
	t.Helper()

	select {
	case <-run.reached:
		t.Fatalf("%s ran its body", what)
	default:
	}
}

// holdCheckedWrite starts a checked write that stays in flight until the
// returned function runs. It returns when the body of the write runs.
func (r *gateRig) holdCheckedWrite() func() {
	r.t.Helper()

	entered := make(chan struct{})
	free := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- r.s.inTx(r.t.Context(), func(*db.Queries) error {
			close(entered)
			<-free

			return nil
		})
	}()

	var (
		endOnce sync.Once
		endErr  error
	)

	// end lets the held write commit and returns its error. Only the first
	// call does the work.
	end := func() error {
		endOnce.Do(func() {
			close(free)

			endErr = recvWithin(r.t, done, "the first write to end")
		})

		return endErr
	}

	// A test that fails while it holds the write must not leave the write
	// held. The context of the test has then ended, so the commit fails too:
	// the cleanup does not report that second failure.
	r.t.Cleanup(func() {
		if err := end(); err != nil && !r.t.Failed() {
			r.t.Errorf("first write = %v, want nil", err)
		}
	})

	recvWithin(r.t, entered, "the first write to run")

	return func() {
		if err := end(); err != nil {
			r.t.Errorf("first write = %v, want nil", err)
		}
	}
}

// assertIdle fails when the gate holds a shared hold, an enter or a waiter.
func (r *gateRig) assertIdle() {
	r.t.Helper()

	g := r.s.enterGate
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.readers != 0 || g.drained != nil || len(g.turn) != 0 {
		r.t.Fatalf("gate holds a lock after all calls ended: readers=%d drained=%v turn=%d",
			g.readers, g.drained != nil, len(g.turn))
	}
}

// White-box: the order of the gate is visible only inside the store. A
// black-box test can only run the writers and hope that they overlap.
//
// TestEnterGateOrdersWrites checks that an enter waits for a write in flight,
// that a second write waits behind the enter, and that the second write then
// fails with maintenance.ErrInMaintenance.
func TestEnterGateOrdersWrites(t *testing.T) {
	t.Parallel()

	for _, enter := range gateEnters() {
		for _, second := range gateWrites() {
			t.Run(enter.name+"/"+second.name, func(t *testing.T) {
				t.Parallel()

				r := newGateRig(t)
				release := r.holdCheckedWrite()

				enterRun := r.start(t.Context(), enter)
				r.waitBlocked("the enter to wait for the write in flight")

				secondRun := r.start(t.Context(), second)
				r.waitBlocked("the second write to wait behind the enter")

				assertPending(t, enterRun, "the enter")
				assertPending(t, secondRun, "the second write")

				release()

				if err := r.wait(enterRun, "the enter to end"); err != nil {
					t.Fatalf("enter = %v, want nil", err)
				}

				err := r.wait(secondRun, "the second write to end")
				if !errors.Is(err, maintenance.ErrInMaintenance) {
					t.Fatalf("second write = %v, want maintenance.ErrInMaintenance", err)
				}

				assertNotReached(t, secondRun, "the second write")
				r.assertIdle()
			})
		}
	}
}

// TestLeaveTakesNoGate checks that a move back to normal does not wait for a
// shared hold, also not while an enter is pending: the leave must work when
// the writes cannot.
func TestLeaveTakesNoGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		pendingEnter bool
	}{
		{"hold only", false},
		{"hold and pending enter", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newGateRig(t)

			releaseHold, err := r.s.holdWrite(t.Context())
			if err != nil {
				t.Fatalf("holdWrite = %v", err)
			}

			var enterRun running

			if tt.pendingEnter {
				enterRun = r.startPendingEnter()
			}

			r.leave()
			releaseHold()

			if tt.pendingEnter {
				if err := r.wait(enterRun, "the enter to end"); err != nil {
					t.Fatalf("enter = %v, want nil", err)
				}
			}

			r.assertIdle()
		})
	}
}

// startPendingEnter starts an enter and waits until it parks.
func (r *gateRig) startPendingEnter() running {
	run := r.start(r.t.Context(), gateEnters()[0])
	r.waitBlocked("the enter to wait")

	return run
}

// leave moves the installation back to normal, and fails when it waits.
func (r *gateRig) leave() {
	r.t.Helper()

	unlock, err := r.s.lockEnter(r.t.Context(), maintenance.StateNormal)
	if err != nil {
		r.t.Fatalf("lockEnter(normal) = %v, want nil with no wait", err)
	}

	unlock()

	if err := r.s.SetState(r.t.Context(), maintenance.StateNormal); err != nil {
		r.t.Fatalf("SetState(normal) = %v, want nil with no wait", err)
	}
}

// TestEnterCanceledInFlight checks that an enter that its context ends, while
// it waits for a write in flight, returns the error of the context and leaves
// no turn behind.
func TestEnterCanceledInFlight(t *testing.T) {
	t.Parallel()

	r := newGateRig(t)
	release := r.holdCheckedWrite()

	ctx, cancel := context.WithTimeout(t.Context(), gateWait)
	enterRun := r.start(ctx, gateEnters()[0])

	r.waitBlocked("the enter to wait")
	cancel()

	err := r.wait(enterRun, "the enter to end")
	if !errors.Is(err, context.Canceled) || !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("enter = %v, want context.Canceled and maintenance.ErrUnavailable", err)
	}

	// The canceled enter left no turn: a new shared hold starts at once. The
	// probe takes the gate only, because the write in flight holds the
	// database write lock. The time limit keeps a failed release from
	// hanging the test.
	probeCtx, probeCancel := context.WithTimeout(t.Context(), gateWait)
	defer probeCancel()

	probe, err := r.s.holdWrite(probeCtx)
	if err != nil {
		t.Fatalf("hold after the canceled enter = %v, want nil", err)
	}

	probe()
	release()
	r.assertIdle()
}

// TestWriteCanceledBehindEnter checks that a write that its context ends,
// while it waits behind a pending enter, returns the error of the context,
// does not run its body and leaves no queued hold.
func TestWriteCanceledBehindEnter(t *testing.T) {
	t.Parallel()

	for _, w := range gateWrites() {
		t.Run(w.name, func(t *testing.T) {
			t.Parallel()

			r := newGateRig(t)
			release := r.holdCheckedWrite()

			enterRun := r.start(t.Context(), gateEnters()[0])
			r.waitBlocked("the enter to wait")

			ctx, cancel := context.WithCancel(t.Context())
			writeRun := r.start(ctx, w)

			r.waitBlocked("the write to wait behind the enter")
			cancel()

			if err := r.wait(writeRun, "the write to end"); !errors.Is(err, context.Canceled) {
				t.Fatalf("write = %v, want context.Canceled", err)
			}

			assertNotReached(t, writeRun, "the canceled write")
			release()

			if err := r.wait(enterRun, "the enter to end"); err != nil {
				t.Fatalf("enter = %v, want nil", err)
			}

			r.assertIdle()
		})
	}
}

// TestEndedContextTakesNoGate checks that a call whose context has ended
// before it starts returns the context error and takes nothing.
func TestEndedContextTakesNoGate(t *testing.T) {
	t.Parallel()

	calls := append(gateWrites(), gateEnters()...)

	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r := newGateRig(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := c.run(ctx, r.s, func() {}); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s = %v, want context.Canceled", c.name, err)
			}

			r.assertIdle()
		})
	}
}

// TestNestedCheckedWriteEndsWithContext shows what a nested checked
// transaction does while an enter is pending: the inner write waits behind the
// enter, and the enter waits for the outer write. The gate cannot detect the
// nesting, because the body of a transaction gets no context of the gate. The
// wait ends only with the context of the inner call, which then returns its
// error. The call does not fail at once.
func TestNestedCheckedWriteEndsWithContext(t *testing.T) {
	t.Parallel()

	r := newGateRig(t)
	inner := gateWrites()[0]
	release := r.holdCheckedWrite()

	enterRun := r.start(t.Context(), gateEnters()[0])
	r.waitBlocked("the enter to wait")

	ctx, cancel := context.WithCancel(t.Context())
	innerRun := r.start(ctx, inner)

	r.waitBlocked("the nested write to wait")
	cancel()

	if err := r.wait(innerRun, "the nested write to end"); !errors.Is(err, context.Canceled) {
		t.Fatalf("nested write = %v, want context.Canceled", err)
	}

	release()

	if err := r.wait(enterRun, "the enter to end"); err != nil {
		t.Fatalf("enter = %v, want nil", err)
	}

	r.assertIdle()
}
