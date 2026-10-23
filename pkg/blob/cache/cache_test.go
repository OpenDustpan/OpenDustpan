package cache_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/pkg/blob"
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

	valid := cache.Config{MaxBytes: 1, MaxObjectBytes: 0, Cacheable: everyKey}

	if _, err := cache.New(nil, valid); !errors.Is(err, cache.ErrNilStore) {
		t.Errorf("New(nil) err = %v, want ErrNilStore", err)
	}

	noCacheable := cache.Config{MaxBytes: 1, MaxObjectBytes: 0, Cacheable: nil}
	if _, err := cache.New(openFS(t), noCacheable); !errors.Is(err, cache.ErrNoCacheable) {
		t.Errorf("New without Cacheable err = %v, want ErrNoCacheable", err)
	}

	for _, cfg := range []cache.Config{
		{MaxBytes: 0, MaxObjectBytes: 0, Cacheable: everyKey},
		{MaxBytes: -1, MaxObjectBytes: 0, Cacheable: everyKey},
		{MaxBytes: 10, MaxObjectBytes: 11, Cacheable: everyKey},
		{MaxBytes: 10, MaxObjectBytes: -1, Cacheable: everyKey},
	} {
		if _, err := cache.New(openFS(t), cfg); !errors.Is(err, cache.ErrInvalidSize) {
			t.Errorf("New(%d, %d) err = %v, want ErrInvalidSize", cfg.MaxBytes, cfg.MaxObjectBytes, err)
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

// TestRefusedFamilyIsNeverCached runs two caches over one store, as two
// instances do. Each caches only tofu/. A key outside it is deleted and
// written again with other bytes by the second cache. The first cache then
// reads the new bytes, because it never kept the old ones.
func TestRefusedFamilyIsNeverCached(t *testing.T) {
	t.Parallel()

	shared := countGets(t, openFS(t))
	cfg := cache.Config{MaxBytes: 1 << 10, MaxObjectBytes: 0, Cacheable: cache.WriteOncePrefixes("tofu/")}
	first, second := newCacheWith(t, shared, cfg), newCacheWith(t, shared, cfg)
	key := mustKey(t, "pulumi/stk-1/upd-1/journal/1")

	put(t, first, key, []byte("refused batch"))

	if got := get(t, first, key); string(got) != "refused batch" {
		t.Fatalf("first get = %q, want %q", got, "refused batch")
	}

	if err := second.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete through the second cache: %v", err)
	}

	put(t, second, key, []byte("retried batch"))

	if got := get(t, first, key); string(got) != "retried batch" {
		t.Fatalf("first get after the rewrite = %q, want %q", got, "retried batch")
	}

	if n := shared.Calls("Get"); n != 2 {
		t.Fatalf("store Get calls = %d, want 2: a key outside the predicate is never cached", n)
	}

	tofu := mustKey(t, "tofu/ws-1/sv-1/1")
	put(t, first, tofu, []byte("state"))
	get(t, first, tofu)
	get(t, first, tofu)

	if n := shared.Calls("Get"); n != 3 {
		t.Fatalf("store Get calls = %d, want 3: a tofu/ key is read from the store once", n)
	}
}

// TestObjectOverObjectBoundIsNotCached reads an object below the cache bound
// but above the object bound twice. Both reads reach the inner store.
func TestObjectOverObjectBoundIsNotCached(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCacheWith(t, inner, cache.Config{MaxBytes: 100, MaxObjectBytes: 10, Cacheable: everyKey})
	key := mustKey(t, "medium")
	data := []byte("0123456789abcdefghij")

	put(t, s, key, data)

	for range 2 {
		if got := get(t, s, key); !bytes.Equal(got, data) {
			t.Fatalf("get = %q, want %q", got, data)
		}
	}

	if n := inner.Calls("Get"); n != 2 {
		t.Fatalf("inner Get calls = %d, want 2", n)
	}
}

// TestDefaultObjectBoundIsOneEighth proves that an object above one eighth of
// the bound stays out of the cache when MaxObjectBytes is zero.
func TestDefaultObjectBoundIsOneEighth(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCacheWith(t, inner, cache.Config{MaxBytes: 80, MaxObjectBytes: 0, Cacheable: everyKey})
	small, large := mustKey(t, "small"), mustKey(t, "large")

	put(t, s, small, []byte("0123456789"))
	put(t, s, large, []byte("0123456789a"))

	for range 2 {
		get(t, s, small)
		get(t, s, large)
	}

	if n := inner.Calls("Get"); n != 3 {
		t.Fatalf("inner Get calls = %d, want 3: 10 bytes are cached, 11 bytes are not", n)
	}
}

// TestWithoutCacheReadsTheStore proves that a context from WithoutCache reads
// the inner store, even for a cached key, and does not fill the cache.
func TestWithoutCacheReadsTheStore(t *testing.T) {
	t.Parallel()

	inner := countGets(t, openFS(t))
	s := newCache(t, inner, 1<<10)
	cached, fresh := mustKey(t, "cached"), mustKey(t, "fresh")

	put(t, s, cached, []byte("a"))
	put(t, s, fresh, []byte("b"))
	get(t, s, cached)

	bypass := cache.WithoutCache(t.Context())

	for _, key := range []blob.Key{cached, fresh, fresh} {
		r, err := s.Get(bypass, key)
		if err != nil {
			t.Fatalf("get %s without the cache: %v", key, err)
		}

		if _, err := io.ReadAll(r); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}

		if err := r.Close(); err != nil {
			t.Fatalf("close %s: %v", key, err)
		}
	}

	get(t, s, fresh)

	if n := inner.Calls("Get"); n != 5 {
		t.Fatalf("inner Get calls = %d, want 5: reads without the cache never fill it", n)
	}
}

// TestReadDuringInnerDeleteIsNotKept starts a Get while the inner Delete
// runs, after the first forget. The read ends before the inner Delete. The
// cache must still not serve the key after Delete returns.
func TestReadDuringInnerDeleteIsNotKept(t *testing.T) {
	t.Parallel()

	inner := &blockingDelete{Store: openFS(t), started: make(chan struct{}), release: make(chan struct{})}
	s := newCache(t, inner, 1<<10)
	key := mustKey(t, "racing")

	put(t, s, key, []byte("payload"))

	deleted := make(chan error, 1)

	go func() { deleted <- s.Delete(t.Context(), key) }()

	<-inner.started

	if got := get(t, s, key); string(got) != "payload" {
		t.Fatalf("get during the delete = %q, want %q", got, "payload")
	}

	close(inner.release)

	if err := <-deleted; err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := s.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

// blockingDelete holds Delete on the inner store until release closes. It
// closes started when Delete begins.
type blockingDelete struct {
	blob.Store

	started chan struct{}
	release chan struct{}
}

func (b *blockingDelete) Delete(ctx context.Context, key blob.Key) error {
	close(b.started)
	<-b.release

	return b.Store.Delete(ctx, key)
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

// everyKey caches every key, for the tests of the bounds.
func everyKey(blob.Key) bool { return true }

// newCache returns a cache over inner that keeps every key, with one object
// allowed to take the whole bound.
func newCache(t *testing.T, inner blob.Store, maxBytes int64) *cache.Store {
	t.Helper()

	return newCacheWith(t, inner, cache.Config{MaxBytes: maxBytes, MaxObjectBytes: maxBytes, Cacheable: everyKey})
}

func newCacheWith(t *testing.T, inner blob.Store, cfg cache.Config) *cache.Store {
	t.Helper()

	s, err := cache.New(inner, cfg)
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
