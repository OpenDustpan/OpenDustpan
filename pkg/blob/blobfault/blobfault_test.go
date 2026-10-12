package blobfault_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
)

// TestTransparency runs the blob conformance suite through the wrapper with
// no faults configured, to prove the wrapper changes nothing.
func TestTransparency(t *testing.T) {
	t.Parallel()

	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()

		wrapped, err := blobfault.New(openBlobs(t))
		if err != nil {
			t.Fatalf("wrap blob store: %v", err)
		}

		return wrapped
	})
}

// TestFailOnce fails the first Put and lets the second through, counting both.
func TestFailOnce(t *testing.T) {
	t.Parallel()

	wrapped, err := blobfault.New(openBlobs(t), blobfault.Fault{
		Method: "Put",
		Call:   1,
		Kind:   blobfault.KindFail,
		Err:    nil,
		Delay:  0,
	})
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	key := mustKey(t, "fault/once")
	ctx := t.Context()

	if err := wrapped.Put(ctx, key, bytes.NewReader([]byte("first"))); !errors.Is(err, blob.ErrExists) {
		t.Fatalf("first put: err = %v, want ErrExists", err)
	}

	if err := wrapped.Put(ctx, key, bytes.NewReader([]byte("first"))); err != nil {
		t.Fatalf("second put: %v", err)
	}

	if n := wrapped.Calls("Put"); n != 2 {
		t.Fatalf("put calls = %d, want 2", n)
	}
}

// TestFailEvery fails each Delete while reads still pass through.
func TestFailEvery(t *testing.T) {
	t.Parallel()

	wrapped, err := blobfault.New(openBlobs(t), blobfault.Fault{
		Method: "Delete",
		Call:   0,
		Kind:   blobfault.KindFail,
		Err:    nil,
		Delay:  0,
	})
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	key := mustKey(t, "fault/always")
	ctx := t.Context()

	if err := wrapped.Put(ctx, key, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("put: %v", err)
	}

	for range 2 {
		if err := wrapped.Delete(ctx, key); err == nil {
			t.Fatal("delete with an every-call fault = nil, want an error")
		}
	}

	if got := readKey(t, wrapped, key); string(got) != "x" {
		t.Fatalf("get after faulted deletes = %q, want %q", got, "x")
	}
}

// TestFailCustomError answers the configured error, so tests model the
// documented outcome they need.
func TestFailCustomError(t *testing.T) {
	t.Parallel()

	wrapped, err := blobfault.New(openBlobs(t), blobfault.Fault{
		Method: "Get",
		Call:   1,
		Kind:   blobfault.KindFail,
		Err:    blob.ErrNotFound,
		Delay:  0,
	})
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	if _, err := wrapped.Get(t.Context(), mustKey(t, "fault/custom")); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get with a custom fault: err = %v, want ErrNotFound", err)
	}
}

// TestStallEndsWithContext proves a stalled call waits for the context and
// then answers its error, instead of touching the inner store.
func TestStallEndsWithContext(t *testing.T) {
	t.Parallel()

	wrapped, err := blobfault.New(openBlobs(t), blobfault.Fault{
		Method: "Get",
		Call:   1,
		Kind:   blobfault.KindStall,
		Err:    nil,
		Delay:  0,
	})
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan error, 1)

	go func() {
		_, err := wrapped.Get(ctx, mustKey(t, "fault/stall"))
		got <- err
	}()

	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()

	select {
	case err := <-got:
		t.Fatalf("stalled get answered %v before the cancel", err)
	case <-timer.C:
	}

	cancel()

	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("stalled get: err = %v, want context.Canceled", err)
	}
}

// TestDelayCallsThroughAfterTheDelay proves a delayed call reaches the inner
// store once the delay passes.
func TestDelayCallsThroughAfterTheDelay(t *testing.T) {
	t.Parallel()

	wrapped, err := blobfault.New(openBlobs(t), blobfault.Fault{
		Method: "Put",
		Call:   1,
		Kind:   blobfault.KindDelay,
		Err:    nil,
		Delay:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	key := mustKey(t, "fault/delayed")
	ctx := t.Context()
	start := time.Now()

	if err := wrapped.Put(ctx, key, bytes.NewReader([]byte("late"))); err != nil {
		t.Fatalf("delayed put: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("delayed put took %s, want at least 50ms", elapsed)
	}

	if got := readKey(t, wrapped, key); string(got) != "late" {
		t.Fatalf("get after delayed put = %q, want %q", got, "late")
	}
}

// TestDelayEndsWithContext proves a delay that outlasts the context answers
// the context error without touching the inner store.
func TestDelayEndsWithContext(t *testing.T) {
	t.Parallel()

	wrapped, err := blobfault.New(openBlobs(t), blobfault.Fault{
		Method: "Put",
		Call:   1,
		Kind:   blobfault.KindDelay,
		Err:    nil,
		Delay:  time.Hour,
	})
	if err != nil {
		t.Fatalf("wrap blob store: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	key := mustKey(t, "fault/timed-out")

	if err := wrapped.Put(ctx, key, bytes.NewReader([]byte("x"))); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("put with an outlasted delay: err = %v, want context.DeadlineExceeded", err)
	}

	if _, err := wrapped.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after timed-out put: err = %v, want ErrNotFound", err)
	}
}

// openBlobs opens a filesystem blob store in a temporary directory.
func openBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Errorf("close blob store: %v", err)
		}
	})

	return blobs
}

func mustKey(t *testing.T, s string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(s)
	if err != nil {
		t.Fatalf("parse key %q: %v", s, err)
	}

	return key
}

func readKey(t *testing.T, s blob.Store, key blob.Key) []byte {
	t.Helper()

	rc, err := s.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	data, err := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close %s: %v", key, closeErr)
	}

	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	return data
}
