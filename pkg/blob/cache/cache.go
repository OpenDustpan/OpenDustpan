// Package cache holds an in-process cache for the blob port. Store wraps a
// blob.Store and keeps the bytes of objects that Get read to their end, up to
// a bound in bytes. When the cached bytes go over the bound, the cache drops
// the object that was read least recently.
//
// The cache keeps only the keys that its Cacheable function accepts. Give it
// only the key families that are written once: the bytes under such a key
// never change while the key exists, and no caller writes the key again
// after a Delete. Several server instances can then each keep a cache over
// one shared store, because no instance can write other bytes under a cached
// key. A family whose keys are deleted and written again, for example a
// batch that a retry writes again, must stay out of the cache.
//
// Delete on this Store drops the key from the cache. A Delete on another
// instance does not: this cache keeps the bytes of a key that another
// instance purged until it drops them to make room. Callers read only keys
// that a record names, so they do not ask for a purged key.
package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"opendustpan.org/opendustpan/pkg/blob"
)

// Sentinel errors returned by New.
var (
	// ErrNilStore reports a New call without an inner store.
	ErrNilStore = errors.New("blob cache needs a store")
	// ErrInvalidSize reports a bound of zero bytes or less, or a bound for
	// one object above the bound of the cache.
	ErrInvalidSize = errors.New("blob cache size must be more than 0 bytes")
	// ErrNoCacheable reports a Config without a Cacheable function.
	ErrNoCacheable = errors.New("blob cache needs a Cacheable function")
)

// objectShare is the part of the cache that one object gets by default: one
// eighth, so one large object cannot drop every other object.
const objectShare = 8

// Config sets the bounds of a Store and the keys it keeps.
type Config struct {
	// MaxBytes bounds the bytes of all cached objects. It must be more
	// than 0.
	MaxBytes int64
	// MaxObjectBytes bounds the bytes of one cached object. A larger object
	// is read from the inner store each time. Zero means MaxBytes/8. It must
	// not be above MaxBytes.
	MaxObjectBytes int64
	// Cacheable reports whether the cache may keep key. It must accept only
	// keys that are written once. Required.
	Cacheable func(key blob.Key) bool
}

// WriteOncePrefixes returns a Cacheable function that accepts the keys under
// each of prefixes, for example "tofu/".
func WriteOncePrefixes(prefixes ...string) func(blob.Key) bool {
	return func(key blob.Key) bool {
		for _, prefix := range prefixes {
			if strings.HasPrefix(key.String(), prefix) {
				return true
			}
		}

		return false
	}
}

// bypassKey marks a context whose reads skip the cache.
type bypassKey struct{}

// WithoutCache returns a context whose Get calls read the inner store and do
// not fill the cache. A check that must see the stored bytes, such as the
// copy check of a backup, uses it.
func WithoutCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassKey{}, true)
}

// bypassed reports whether ctx came from WithoutCache.
func bypassed(ctx context.Context) bool {
	on, ok := ctx.Value(bypassKey{}).(bool)

	return ok && on
}

// Store is a blob.Store that caches the objects of an inner store in memory.
// The zero value is not usable: call New. It is safe for concurrent use.
type Store struct {
	inner          blob.Store
	maxBytes       int64
	maxObjectBytes int64
	cacheable      func(blob.Key) bool

	mu    sync.Mutex
	size  int64
	byKey map[blob.Key]*entry
	// newest and oldest are the ends of the list of entries in the order of
	// their last read.
	newest *entry
	oldest *entry
	// deletes counts the Delete calls. A read that a Delete overlaps does
	// not go into the cache, because it can hold the bytes of a deleted key.
	deletes uint64
}

// entry is one cached object in the list of entries. newer and older are
// its neighbors in that list.
type entry struct {
	key   blob.Key
	data  []byte
	newer *entry
	older *entry
}

var (
	_ blob.Store = (*Store)(nil)
	_ io.Closer  = (*Store)(nil)
)

// New returns a Store over inner with the bounds and keys of cfg. It returns
// ErrNilStore for a nil inner store, ErrNoCacheable for a nil Cacheable, and
// ErrInvalidSize for a bound of zero or less or an object bound above
// MaxBytes.
func New(inner blob.Store, cfg Config) (*Store, error) {
	if inner == nil {
		return nil, fmt.Errorf("%w", ErrNilStore)
	}

	if cfg.Cacheable == nil {
		return nil, fmt.Errorf("%w", ErrNoCacheable)
	}

	if cfg.MaxBytes <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidSize, cfg.MaxBytes)
	}

	maxObject := cfg.MaxObjectBytes
	if maxObject == 0 {
		maxObject = max(cfg.MaxBytes/objectShare, 1)
	}

	if maxObject < 0 || maxObject > cfg.MaxBytes {
		return nil, fmt.Errorf("%w: object bound %d with cache bound %d", ErrInvalidSize, maxObject, cfg.MaxBytes)
	}

	return &Store{
		inner:          inner,
		maxBytes:       cfg.MaxBytes,
		maxObjectBytes: maxObject,
		cacheable:      cfg.Cacheable,
		mu:             sync.Mutex{},
		size:           0,
		byKey:          map[blob.Key]*entry{},
		newest:         nil,
		oldest:         nil,
		deletes:        0,
	}, nil
}

// Put stores the contents of r in the inner store. It does not fill the
// cache.
func (s *Store) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	return s.inner.Put(ctx, key, r)
}

// Get returns the cached bytes of key, or opens the object in the inner store.
// An object that the caller reads to its end goes into the cache, if
// Cacheable accepts its key and it fits the object bound. A key that
// Cacheable refuses and a context from WithoutCache always read the inner
// store.
func (s *Store) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if !s.cacheable(key) || bypassed(ctx) {
		return s.inner.Get(ctx, key)
	}

	s.mu.Lock()

	if e, ok := s.byKey[key]; ok {
		s.touch(e)

		data := e.data
		s.mu.Unlock()

		return io.NopCloser(bytes.NewReader(data)), nil
	}

	deletes := s.deletes
	s.mu.Unlock()

	r, err := s.inner.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	return &fillReader{store: s, key: key, inner: r, deletes: deletes, buf: nil, done: false}, nil
}

// Delete drops key from the cache and removes the object from the inner
// store.
func (s *Store) Delete(ctx context.Context, key blob.Key) error {
	s.forget(key)

	err := s.inner.Delete(ctx, key)

	// A read that began before the inner Delete can end after it. The second
	// forget counts the Delete again, so that read does not fill the cache.
	s.forget(key)

	return err
}

// ListPage returns one page of the objects under the prefix from the inner
// store.
func (s *Store) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	return s.inner.ListPage(ctx, req)
}

// Ping checks the inner store.
func (s *Store) Ping(ctx context.Context) error {
	return s.inner.Ping(ctx)
}

// Close closes the inner store when it is an io.Closer.
func (s *Store) Close() error {
	closer, ok := s.inner.(io.Closer)
	if !ok {
		return nil
	}

	if err := closer.Close(); err != nil {
		return fmt.Errorf("close blob store: %w", err)
	}

	return nil
}

// forget drops key from the cache and counts one Delete.
func (s *Store) forget(key blob.Key) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deletes++

	if e, ok := s.byKey[key]; ok {
		s.remove(e)
	}
}

// add puts data under key, unless a Delete came after the read began. It
// drops the least recently read objects until the cache fits the bound.
func (s *Store) add(key blob.Key, data []byte, deletes uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deletes != deletes {
		return
	}

	if e, ok := s.byKey[key]; ok {
		s.touch(e)

		return
	}

	e := &entry{key: key, data: data, newer: nil, older: nil}
	s.byKey[key] = e
	s.size += int64(len(data))
	s.pushNewest(e)

	for s.size > s.maxBytes && s.oldest != nil {
		s.remove(s.oldest)
	}
}

// touch makes e the most recently read entry. The caller holds mu.
func (s *Store) touch(e *entry) {
	s.unlink(e)
	s.pushNewest(e)
}

// remove drops e from the cache. The caller holds mu.
func (s *Store) remove(e *entry) {
	s.unlink(e)
	delete(s.byKey, e.key)
	s.size -= int64(len(e.data))
}

// pushNewest puts e at the newest end of the list. The caller holds mu.
func (s *Store) pushNewest(e *entry) {
	e.older = s.newest
	e.newer = nil

	if s.newest != nil {
		s.newest.newer = e
	}

	s.newest = e

	if s.oldest == nil {
		s.oldest = e
	}
}

// unlink takes e out of the list. The caller holds mu.
func (s *Store) unlink(e *entry) {
	if e.newer != nil {
		e.newer.older = e.older
	} else {
		s.newest = e.older
	}

	if e.older != nil {
		e.older.newer = e.newer
	} else {
		s.oldest = e.newer
	}

	e.newer, e.older = nil, nil
}

// fillReader reads an object from the inner store and keeps a copy of its
// bytes while they fit the object bound. At the end of the object, the copy goes
// into the cache. A copy over the bound, a read error or a Close before the
// end drops the copy.
type fillReader struct {
	store   *Store
	key     blob.Key
	inner   io.ReadCloser
	deletes uint64
	buf     []byte
	done    bool
}

// Read reads from the inner object and copies the bytes it returns.
func (r *fillReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)

	if !r.done && n > 0 {
		if int64(len(r.buf)+n) > r.store.maxObjectBytes {
			r.done, r.buf = true, nil
		} else {
			r.buf = append(r.buf, p[:n]...)
		}
	}

	switch {
	case errors.Is(err, io.EOF):
		if !r.done {
			r.done = true
			r.store.add(r.key, r.take(), r.deletes)
		}
	case err != nil:
		r.done, r.buf = true, nil
	default:
	}

	//nolint:wrapcheck // Callers compare io.EOF with ==, so Read returns it unwrapped.
	return n, err
}

// Close closes the inner object. A copy not read to the end stays out of
// the cache.
func (r *fillReader) Close() error {
	r.done, r.buf = true, nil

	if err := r.inner.Close(); err != nil {
		return fmt.Errorf("close blob %s: %w", r.key, err)
	}

	return nil
}

// take returns the bytes read, with no spare capacity, so the cache holds
// only the length it counts.
func (r *fillReader) take() []byte {
	data := make([]byte, len(r.buf))
	copy(data, r.buf)
	r.buf = nil

	return data
}
