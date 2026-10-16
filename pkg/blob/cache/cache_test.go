package cache_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobfault"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/cache"
	"opendustpan.org/opendustpan/pkg/blob/fs"
)

// TestConformance runs the blob conformance suite through the cache. The
// small bound keeps the large object of the suite out of the cache; the large
// bound caches every object of the suite.
func TestConformance(t *testing.T) {
	t.Parallel()

	for _, maxBytes := range []int64{1 << 10, 64 << 20} {
		t.Run(byteSize(maxBytes), func(t *testing.T) {
			t.Parallel()

			blobtest.Run(t, func(t *testing.T) blob.Store {
				t.Helper()

				return newCache(t, openFS(t), maxBytes)
			})
		})
	}
}

func TestNewRefusesBadInput(t *testing.T) {
	t.Parallel()

	if _, err := cache.New(nil, 1); !errors.Is(err, cache.ErrNilStore) {
		t.Errorf("New(nil) err = %v, want ErrNilStore", err)
	}

	for _, size := range []int64{0, -1} {
		if _, err := cache.New(openFS(t), size); !errors.Is(err, cache.ErrInvalidSize) {
			t.Errorf("New(size %d) err = %v, want ErrInvalidSize", size, err)
		}
	}
}

func TestHitServesFromMemory(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCache(t, inner, 1<<10)
	key := mustKey(t, "tofu/ws-1/sv-1/a1")

	put(t, s, key, []byte("state"))

	for range 3 {
		if got := get(t, s, key); string(got) != "state" {
			t.Fatalf("get = %q, want %q", got, "state")
		}
	}

	if n := inner.Calls("Get"); n != 1 {
		t.Fatalf("inner Get calls = %d, want 1", n)
	}
}

func TestMissIsNotCached(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCache(t, inner, 1<<10)
	key := mustKey(t, "missing")

	for range 2 {
		if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("get missing key: err = %v, want ErrNotFound", err)
		}
	}

	put(t, s, key, []byte("now here"))

	if got := get(t, s, key); string(got) != "now here" {
		t.Fatalf("get after put = %q, want %q", got, "now here")
	}

	if n := inner.Calls("Get"); n != 3 {
		t.Fatalf("inner Get calls = %d, want 3", n)
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCache(t, inner, 10)
	a, b, c := mustKey(t, "a"), mustKey(t, "b"), mustKey(t, "c")

	put(t, s, a, []byte("aaaa"))
	put(t, s, b, []byte("bbbb"))
	put(t, s, c, []byte("cccc"))

	get(t, s, a) // miss: a cached, 4 bytes
	get(t, s, b) // miss: b cached, 8 bytes
	get(t, s, a) // hit: a is now the most recent
	get(t, s, c) // miss: 12 bytes is over 10, so b goes

	if n := inner.Calls("Get"); n != 3 {
		t.Fatalf("inner Get calls before eviction check = %d, want 3", n)
	}

	get(t, s, a) // hit
	get(t, s, c) // hit
	get(t, s, b) // miss: evicted

	if n := inner.Calls("Get"); n != 4 {
		t.Fatalf("inner Get calls = %d, want 4", n)
	}
}

func TestObjectOverBoundIsNotCached(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCache(t, inner, 4)
	key := mustKey(t, "big")

	put(t, s, key, []byte("12345"))

	for range 2 {
		if got := get(t, s, key); string(got) != "12345" {
			t.Fatalf("get = %q, want %q", got, "12345")
		}
	}

	if n := inner.Calls("Get"); n != 2 {
		t.Fatalf("inner Get calls = %d, want 2", n)
	}
}

func TestPartialReadIsNotCached(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCache(t, inner, 1<<10)
	key := mustKey(t, "partial")

	put(t, s, key, []byte("0123456789"))

	r, err := s.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if _, err := io.ReadFull(r, make([]byte, 3)); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := get(t, s, key); string(got) != "0123456789" {
		t.Fatalf("get = %q, want %q", got, "0123456789")
	}

	if n := inner.Calls("Get"); n != 2 {
		t.Fatalf("inner Get calls = %d, want 2", n)
	}
}

func TestDeletedKeyIsNotServed(t *testing.T) {
	t.Parallel()

	s := newCache(t, openFS(t), 1<<10)
	key := mustKey(t, "doomed")

	put(t, s, key, []byte("x"))
	get(t, s, key)

	if err := s.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

// TestDeleteDuringReadIsNotCached reads an object to its end after a Delete
// of it. The read began before the Delete, so the cache must not keep it.
func TestDeleteDuringReadIsNotCached(t *testing.T) {
	t.Parallel()

	s := newCache(t, openFS(t), 1<<10)
	key := mustKey(t, "racing")

	put(t, s, key, []byte("payload"))

	r, err := s.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if err := s.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestCloseClosesInner(t *testing.T) {
	t.Parallel()

	inner := &closeCounter{Store: openFS(t), closes: 0}

	s := newCache(t, inner, 1)
	if err := s.Close(); !errors.Is(err, errClosed) {
		t.Fatalf("close: err = %v, want the error of the inner store", err)
	}

	if inner.closes != 1 {
		t.Fatalf("inner closes = %d, want 1", inner.closes)
	}
}

// errClosed is the error closeCounter returns, so a test can see that Close
// returns the error of the inner store.
var errClosed = errors.New("closed")

// closeCounter counts the Close calls on a blob store.
type closeCounter struct {
	blob.Store

	closes int
}

func (c *closeCounter) Close() error {
	c.closes++

	return errClosed
}

func openFS(t *testing.T) *fs.Store {
	t.Helper()

	s, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
}

func countGets(t *testing.T, inner blob.Store) *blobfault.Store {
	t.Helper()

	s, err := blobfault.New(inner)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	return s
}

func newCache(t *testing.T, inner blob.Store, maxBytes int64) *cache.Store {
	t.Helper()

	s, err := cache.New(inner, maxBytes)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	return s
}

func byteSize(n int64) string {
	if n >= 1<<20 {
		return "MiB"
	}

	return "KiB"
}

func mustKey(t *testing.T, s string) blob.Key {
	t.Helper()

	k, err := blob.ParseKey(s)
	if err != nil {
		t.Fatalf("parse key %q: %v", s, err)
	}

	return k
}

func put(t *testing.T, s blob.Store, key blob.Key, data []byte) {
	t.Helper()

	if err := s.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func get(t *testing.T, s blob.Store, key blob.Key) []byte {
	t.Helper()

	r, err := s.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("close %s: %v", key, err)
	}

	return data
}
