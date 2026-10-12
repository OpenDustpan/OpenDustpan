// Package blobfault holds a test-only fault wrapper for the blob port (ADR 40
// decision 1). Tests fail, stall or delay chosen calls to prove the crash
// windows of each write path: a blob written with no SQL row naming it, and a
// row committed with an old blob not yet deleted. Production code must not
// import this package.
//
// A fault names a Store method ("Put", "Get", "Delete", "List" or "Ping") and
// the 1-based call count at which it fires. A non-positive call count fires on
// every call. When several faults match one call, the first one in the list
// wins.
//
// An injected failure keeps the port contract: a Put fault defaults to
// blob.ErrExists and a Get fault to blob.ErrNotFound, the failure sentinels
// the port documents. Delete, List and Ping document no failure sentinel, so
// their faults return a plain error shaped like an adapter I/O failure. A
// test may set Err to any error the port documents to model another outcome.
// A stall or a delay that outlasts the context answers the context error,
// which every caller already handles.
package blobfault

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// ErrNilStore reports a New call without an inner store.
var ErrNilStore = errors.New("blobfault needs a store")

// errInjected is the failure a Delete, List or Ping fault answers. Those
// methods document no failure sentinel, so the fault answers a static error
// shaped like an adapter I/O failure, without inventing a new sentinel.
var errInjected = errors.New("injected blob fault")

// Kind selects what a fault does when it fires.
type Kind int

// Fault kinds.
const (
	// KindFail answers the fault error without calling the inner store.
	KindFail Kind = iota
	// KindStall waits until the context ends, then answers its error.
	KindStall
	// KindDelay waits for the configured delay, then calls the inner store.
	// A context that ends first answers its error instead.
	KindDelay
)

// Fault fires at one call of one Store method. Method names a Store method
// ("Put", "Get", "Delete", "List" or "Ping"). Call is the 1-based invocation
// count of that method at which the fault fires; zero or negative fires on
// every call. Err is the error a KindFail fault answers; nil answers the
// default of the method. Delay is how long a KindDelay fault waits before it
// calls the inner store.
type Fault struct {
	Method string
	Call   int
	Kind   Kind
	Err    error
	Delay  time.Duration
}

// Store wraps a blob.Store and applies faults before it delegates. The zero
// value is not usable: call New. It is safe for concurrent use.
type Store struct {
	inner  blob.Store
	mu     sync.Mutex
	faults []Fault
	calls  map[string]int
}

var _ blob.Store = (*Store)(nil)

// New returns a Store over inner that applies faults in order.
func New(inner blob.Store, faults ...Fault) (*Store, error) {
	if inner == nil {
		return nil, fmt.Errorf("%w", ErrNilStore)
	}

	return &Store{inner: inner, mu: sync.Mutex{}, faults: faults, calls: map[string]int{}}, nil
}

// Calls returns how many times method ran through the wrapper, whether a
// fault fired or not. Tests assert it to prove a fault fired.
func (s *Store) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls[method]
}

// Put stores the contents of r under key, unless a fault fires first.
func (s *Store) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := s.before(ctx, "Put"); err != nil {
		return err
	}

	return s.inner.Put(ctx, key, r)
}

// Get opens the object under key, unless a fault fires first.
func (s *Store) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if err := s.before(ctx, "Get"); err != nil {
		return nil, err
	}

	return s.inner.Get(ctx, key)
}

// Delete removes the object under key, unless a fault fires first.
func (s *Store) Delete(ctx context.Context, key blob.Key) error {
	if err := s.before(ctx, "Delete"); err != nil {
		return err
	}

	return s.inner.Delete(ctx, key)
}

// List returns the objects under prefix, unless a fault fires first.
func (s *Store) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	if err := s.before(ctx, "List"); err != nil {
		return nil, err
	}

	return s.inner.List(ctx, prefix)
}

// Ping checks the inner store, unless a fault fires first.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.before(ctx, "Ping"); err != nil {
		return err
	}

	return s.inner.Ping(ctx)
}

// before counts one call of method and applies the matching fault, if any.
// It returns nil when the call proceeds to the inner store.
func (s *Store) before(ctx context.Context, method string) error {
	fault, ok := s.match(method)
	if !ok {
		return nil
	}

	switch fault.Kind {
	case KindFail:
		if fault.Err != nil {
			return fmt.Errorf("fault %s: %w", method, fault.Err)
		}

		return fmt.Errorf("fault %s: %w", method, defaultErr(method))
	case KindStall:
		return s.stall(ctx, method)
	case KindDelay:
		return s.delay(ctx, method, fault.Delay)
	default:
		return fmt.Errorf("fault %s: %w", method, defaultErr(method))
	}
}

// match counts one call of method and returns the first fault for it, if any.
func (s *Store) match(method string) (Fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls[method]++

	n := s.calls[method]

	for _, fault := range s.faults {
		if fault.Method == method && (fault.Call <= 0 || fault.Call == n) {
			return fault, true
		}
	}

	var none Fault

	return none, false
}

// stall waits until ctx ends, then answers its error.
func (s *Store) stall(ctx context.Context, method string) error {
	<-ctx.Done()

	return fmt.Errorf("fault %s stall: %w", method, ctx.Err())
}

// delay waits for d, then lets the call proceed. A context that ends first
// answers its error instead. A non-positive delay proceeds at once.
func (s *Store) delay(ctx context.Context, method string, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("fault %s delay: %w", method, ctx.Err())
	case <-timer.C:
		return nil
	}
}

// defaultErr returns the failure sentinel of method where the port documents
// one. Delete, List and Ping document none, so they answer a plain error
// shaped like an adapter I/O failure.
func defaultErr(method string) error {
	switch method {
	case "Put":
		return blob.ErrExists
	case "Get":
		return blob.ErrNotFound
	default:
		return errInjected
	}
}
