// Package blobtest is the conformance suite for blob.Store implementations.
// Every built-in adapter runs it, and custom adapters should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		blobtest.Run(t, func(t *testing.T) blob.Store { return newMyStore(t) })
//	}
//
// The suite includes concurrent cases; see Factory for what a store must
// support.
package blobtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// Factory returns a fresh, empty store for one subtest. Each case gets its
// own store, but the parallel-put case shares that store across goroutines:
// the store must stay correct under concurrent use, with a Put that writes a
// key only once even when puts race on it. An adapter without atomic
// write-once semantics fails that case.
type Factory func(t *testing.T) blob.Store

// largeObjectSize is the payload of the large-object case: 4 MiB. It is
// larger than the copy buffer and one filesystem block, so it exercises the
// streaming path, yet small enough that a run with -race stays fast.
const largeObjectSize = 4 << 20

// parallelPuts is the number of goroutines that race one key in the
// parallel-put case. Eight overlaps on a multicore runner while the case
// stays fast.
const parallelPuts = 8

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("put then get returns the same bytes", func(t *testing.T) {
		t.Parallel()
		testRoundTrip(t, newStore(t))
	})

	t.Run("second put on one key fails with ErrExists", func(t *testing.T) {
		t.Parallel()
		testSecondPutFails(t, newStore(t))
	})

	t.Run("get of a missing key is ErrNotFound", func(t *testing.T) {
		t.Parallel()
		testMissing(t, newStore(t))
	})

	t.Run("delete removes and is idempotent", func(t *testing.T) {
		t.Parallel()
		testDelete(t, newStore(t))
	})

	t.Run("parallel put on one key gives one success", func(t *testing.T) {
		t.Parallel()
		testParallelPut(t, newStore(t))
	})

	t.Run("large object round-trips", func(t *testing.T) {
		t.Parallel()
		testLargeObject(t, newStore(t))
	})

	t.Run("failed put leaves no object", func(t *testing.T) {
		t.Parallel()
		testFailedPut(t, newStore(t))
	})

	t.Run("list returns the objects under a prefix ordered by key", func(t *testing.T) {
		t.Parallel()
		testList(t, newStore(t))
	})

	t.Run("list of a missing prefix is empty", func(t *testing.T) {
		t.Parallel()
		testListMissing(t, newStore(t))
	})

	t.Run("ping answers on a healthy store and fails on a canceled context", func(t *testing.T) {
		t.Parallel()
		testPing(t, newStore(t))
	})
}

func testRoundTrip(t *testing.T, s blob.Store) {
	t.Helper()

	key := mustKey(t, "tofu/ws-1/sv-1/a1")
	want := []byte(`{"version":3}`)
	put(t, s, key, want)

	if got := get(t, s, key); !bytes.Equal(got, want) {
		t.Fatalf("get %s = %q, want %q", key, got, want)
	}
}

func testSecondPutFails(t *testing.T, s blob.Store) {
	t.Helper()

	key := mustKey(t, "a/b")
	put(t, s, key, []byte("first"))

	if err := s.Put(t.Context(), key, bytes.NewReader([]byte("second"))); !errors.Is(err, blob.ErrExists) {
		t.Fatalf("second put: err = %v, want ErrExists", err)
	}

	if got := get(t, s, key); string(got) != "first" {
		t.Fatalf("get after failed put = %q, want %q", got, "first")
	}
}

func testMissing(t *testing.T, s blob.Store) {
	t.Helper()

	_, err := s.Get(t.Context(), mustKey(t, "missing"))
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get missing key: err = %v, want ErrNotFound", err)
	}
}

func testDelete(t *testing.T, s blob.Store) {
	t.Helper()

	key := mustKey(t, "doomed")
	put(t, s, key, []byte("x"))

	for range 2 {
		if err := s.Delete(t.Context(), key); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}

	if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

// testParallelPut races parallelPuts goroutines on one key. Exactly one Put
// succeeds, the rest fail with ErrExists, and Get returns the bytes of the
// winner whole.
func testParallelPut(t *testing.T, s blob.Store) {
	t.Helper()

	key := mustKey(t, "parallel/race")
	payloads := make([][]byte, parallelPuts)

	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("winner-%02d-%s", i, strings.Repeat("x", 1024)))
	}

	ctx := t.Context()
	errs := make([]error, parallelPuts)

	var wg sync.WaitGroup

	for i := range payloads {
		wg.Go(func() {
			errs[i] = s.Put(ctx, key, bytes.NewReader(payloads[i]))
		})
	}

	wg.Wait()

	var winner []byte

	successes := 0

	for i, err := range errs {
		switch {
		case err == nil:
			successes++
			winner = payloads[i]
		case errors.Is(err, blob.ErrExists):
		default:
			t.Fatalf("parallel put %d: err = %v, want nil or ErrExists", i, err)
		}
	}

	if successes != 1 {
		t.Fatalf("parallel put: %d successes, want exactly one", successes)
	}

	if got := get(t, s, key); !bytes.Equal(got, winner) {
		t.Fatalf("get after parallel put = %d bytes, want the %d bytes of the winner whole", len(got), len(winner))
	}
}

// testLargeObject writes largeObjectSize bytes of a fixed pattern and checks
// the read back matches in length and SHA-256 digest.
func testLargeObject(t *testing.T, s blob.Store) {
	t.Helper()

	key := mustKey(t, "large/object")
	data := make([]byte, largeObjectSize)

	for i := range data {
		data[i] = byte(i % 251)
	}

	want := sha256.Sum256(data)

	put(t, s, key, data)

	got := get(t, s, key)

	if len(got) != len(data) {
		t.Fatalf("get large object = %d bytes, want %d", len(got), len(data))
	}

	if digest := sha256.Sum256(got); digest != want {
		t.Fatalf("get large object digest = %x, want %x", digest, want)
	}
}

// failReader returns data until failAfter bytes, then fails, as a reader the
// test closes early would.
type failReader struct {
	data      []byte
	failAfter int
	off       int
}

// errReadFailed is the failure failReader reports once it passes failAfter.
var errReadFailed = errors.New("simulated read failure")

func newFailReader(data []byte, failAfter int) *failReader {
	return &failReader{data: data, failAfter: failAfter, off: 0}
}

func (r *failReader) Read(p []byte) (int, error) {
	if r.off >= r.failAfter {
		return 0, errReadFailed
	}

	if r.off >= len(r.data) {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.off:min(r.failAfter, len(r.data))])
	r.off += n

	return n, nil
}

// testFailedPut stores a reader that fails after some bytes. The Put fails,
// the key stays absent and a second Put of the same key works.
func testFailedPut(t *testing.T, s blob.Store) {
	t.Helper()

	key := mustKey(t, "failed/put")
	data := []byte(strings.Repeat("partial-", 256))

	if err := s.Put(t.Context(), key, newFailReader(data, 16)); err == nil {
		t.Fatal("put with a failing reader = nil, want an error")
	}

	if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after failed put: err = %v, want ErrNotFound", err)
	}

	put(t, s, key, []byte("second"))

	if got := get(t, s, key); string(got) != "second" {
		t.Fatalf("get after second put = %q, want %q", got, "second")
	}
}

// testList puts objects under two prefixes and proves List returns exactly
// the objects under the wanted prefix, ordered by key, with modification
// times between the puts. A prefix without a trailing slash lists the same
// directory.
func testList(t *testing.T, s blob.Store) {
	t.Helper()

	before := time.Now().UTC().Add(-time.Minute)
	put(t, s, mustKey(t, "tofu/ws-1/sv-2/a1"), []byte("newer"))
	put(t, s, mustKey(t, "tofu/ws-1/sv-1/a1"), []byte("older"))
	put(t, s, mustKey(t, "tofu/ws-1/sv-1/a2"), []byte("other"))
	put(t, s, mustKey(t, "tofu/ws-2/sv-1/a1"), []byte("elsewhere"))
	after := time.Now().UTC().Add(time.Minute)

	for _, prefix := range []string{"tofu/ws-1/", "tofu/ws-1"} {
		got, err := s.List(t.Context(), prefix)
		if err != nil {
			t.Fatalf("list %q: %v", prefix, err)
		}

		want := []string{"tofu/ws-1/sv-1/a1", "tofu/ws-1/sv-1/a2", "tofu/ws-1/sv-2/a1"}

		if len(got) != len(want) {
			t.Fatalf("list %q = %v, want %v", prefix, objectKeys(got), want)
		}

		for i := range want {
			if got[i].Key.String() != want[i] {
				t.Fatalf("list %q = %v, want %v", prefix, objectKeys(got), want)
			}

			if got[i].ModifiedAt.Before(before) || got[i].ModifiedAt.After(after) {
				t.Fatalf(
					"modification time of %q = %v, want between %v and %v",
					got[i].Key,
					got[i].ModifiedAt,
					before,
					after,
				)
			}
		}
	}
}

// testListMissing proves a prefix that names nothing lists empty, and a
// canceled context fails the list.
func testListMissing(t *testing.T, s blob.Store) {
	t.Helper()

	put(t, s, mustKey(t, "tofu/ws-1/sv-1/a1"), []byte("x"))

	if got, err := s.List(t.Context(), "tofu/ghost/"); err != nil || len(got) != 0 {
		t.Fatalf("list of a missing prefix = %v, %v; want empty", objectKeys(got), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.List(ctx, "tofu/ws-1/"); err == nil {
		t.Fatal("list with canceled context = nil, want error")
	}
}

// objectKeys returns the key strings of objects in order.
func objectKeys(objects []blob.Object) []string {
	keys := make([]string, 0, len(objects))
	for i := range objects {
		keys = append(keys, objects[i].Key.String())
	}

	return keys
}

func testPing(t *testing.T, s blob.Store) {
	t.Helper()

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping healthy store: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Ping(ctx); err == nil {
		t.Fatal("ping with canceled context = nil, want error")
	}
}

func mustKey(t *testing.T, s string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(s)
	if err != nil {
		t.Fatalf("parse key %q: %v", s, err)
	}

	return key
}

func put(t *testing.T, s blob.Store, key blob.Key, data []byte) {
	t.Helper()

	if err := s.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func get(t *testing.T, s blob.Store, key blob.Key) []byte {
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
