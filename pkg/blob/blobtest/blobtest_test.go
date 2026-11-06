// Sensitivity tests for the conformance suite: each test runs blobtest.Run
// in a child process against a fake store that breaks one rule, and checks
// the child fails on the matching new case. A fake that the suite cannot
// catch fails the test. The child re-executes the same test with
// blobtestFake set, so the real case code does the checking.
package blobtest_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/testchild"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
)

// blobtestFake selects the fake store the child process serves to the suite.
const blobtestFake = "OPENDUSTPAN_BLOBTEST_FAKE"

// truncateAt is the largest object truncateStore returns whole. It sits below
// the large-object payload, so small cases pass and only that case fails.
const truncateAt = 1 << 20

func TestParallelPutDetectsOverwrite(t *testing.T) {
	t.Parallel()

	if os.Getenv(blobtestFake) == "overwrite" {
		blobtest.Run(t, func(t *testing.T) blob.Store {
			t.Helper()

			return newOverwriteStore()
		})

		return
	}

	// The subtest pattern below filters the child to the single new case,
	// so a non-zero exit proves that case failed, not another case.
	out := runSingleCaseAgainstFake(t, "overwrite", "parallel_put")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the parallel-put case failing:\n%s", out)
	}
}

func TestLargeObjectDetectsTruncation(t *testing.T) {
	t.Parallel()

	if os.Getenv(blobtestFake) == "truncate" {
		blobtest.Run(t, func(t *testing.T) blob.Store {
			t.Helper()

			return newTruncateStore()
		})

		return
	}

	out := runSingleCaseAgainstFake(t, "truncate", "large_object")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the large-object case failing:\n%s", out)
	}
}

func TestFailedPutDetectsLeak(t *testing.T) {
	t.Parallel()

	if os.Getenv(blobtestFake) == "leak" {
		blobtest.Run(t, func(t *testing.T) blob.Store {
			t.Helper()

			return newLeakStore()
		})

		return
	}

	out := runSingleCaseAgainstFake(t, "leak", "failed_put")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the failed-put case failing:\n%s", out)
	}
}

func TestListDetectsUnordered(t *testing.T) {
	t.Parallel()

	if os.Getenv(blobtestFake) == "unordered" {
		blobtest.Run(t, func(t *testing.T) blob.Store {
			t.Helper()

			return newUnorderedStore()
		})

		return
	}

	out := runSingleCaseAgainstFake(t, "unordered", "ordered_by_key")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the list case failing:\n%s", out)
	}
}

func TestListDetectsBadTime(t *testing.T) {
	t.Parallel()

	if os.Getenv(blobtestFake) == "badtime" {
		blobtest.Run(t, func(t *testing.T) blob.Store {
			t.Helper()

			return newBadTimeStore()
		})

		return
	}

	out := runSingleCaseAgainstFake(t, "badtime", "ordered_by_key")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the list case failing:\n%s", out)
	}
}

func TestListDetectsExtra(t *testing.T) {
	t.Parallel()

	if os.Getenv(blobtestFake) == "extra" {
		blobtest.Run(t, func(t *testing.T) blob.Store {
			t.Helper()

			return newExtraStore()
		})

		return
	}

	out := runSingleCaseAgainstFake(t, "extra", "ordered_by_key")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the list case failing:\n%s", out)
	}
}

// runSingleCaseAgainstFake re-executes the calling test in a child process
// filtered to the single subtest matching subtest, with the fake store
// selected, and returns its output. It fails the test when the child passes,
// since the filtered case must catch the fake.
func runSingleCaseAgainstFake(t *testing.T, fake, subtest string) string {
	t.Helper()

	out, err := testchild.Run(t, "^"+t.Name()+"$/"+subtest, blobtestFake+"="+fake)
	if err == nil {
		t.Fatalf("case matching %q passed against the %s store, want failure", subtest, fake)
	}

	return out
}

// memObjects holds objects by key for the fake stores below. Fakes hold it
// by pointer, since it holds a mutex.
type memObjects struct {
	objects map[string][]byte
	times   map[string]time.Time
	mu      sync.Mutex
}

func newMemObjects() *memObjects {
	return &memObjects{objects: map[string][]byte{}, times: map[string]time.Time{}, mu: sync.Mutex{}}
}

func (m *memObjects) get(key blob.Key) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.objects[key.String()]

	return data, ok
}

func (m *memObjects) ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("ping blob store: %w", err)
	}

	return nil
}

func (m *memObjects) delete(key blob.Key) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key.String())
	delete(m.times, key.String())
}

// list returns the objects under prefix, ordered by key. The caller holds no
// lock.
// listPage answers the page that req selects, after the checks of the port.
func (m *memObjects) listPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	all, err := m.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	return blob.PageOf(all, req), nil
}

func (m *memObjects) list(ctx context.Context, prefix string) ([]blob.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}

	dir := strings.TrimSuffix(prefix, "/")

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []blob.Object

	for name := range m.objects {
		if dir != "" && dir != "." && name != dir && !strings.HasPrefix(name, dir+"/") {
			continue
		}

		key, err := blob.ParseKey(name)
		if err != nil {
			continue
		}

		out = append(out, blob.Object{ModifiedAt: m.times[name], Key: key})
	}

	slices.SortFunc(out, func(a, b blob.Object) int {
		return strings.Compare(a.Key.String(), b.Key.String())
	})

	return out, nil
}

// overwriteStore breaks write-once: a Put on an existing key overwrites it
// instead of failing with ErrExists. The parallel case must report one
// success per goroutine instead of exactly one.
type overwriteStore struct {
	mem *memObjects
}

func newOverwriteStore() *overwriteStore {
	return &overwriteStore{mem: newMemObjects()}
}

func (s *overwriteStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()

	s.mem.objects[key.String()] = data
	s.mem.times[key.String()] = time.Now().UTC()

	return nil
}

func (s *overwriteStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	if data, ok := s.mem.get(key); ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
}

func (s *overwriteStore) Delete(_ context.Context, key blob.Key) error {
	s.mem.delete(key)

	return nil
}

func (s *overwriteStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	return s.mem.listPage(ctx, req)
}

func (s *overwriteStore) Ping(ctx context.Context) error {
	return s.mem.ping(ctx)
}

// truncateStore returns only the first truncateAt bytes of larger objects.
// Small cases pass, and the large-object case must fail on length and digest.
type truncateStore struct {
	mem *memObjects
}

func newTruncateStore() *truncateStore {
	return &truncateStore{mem: newMemObjects()}
}

func (s *truncateStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()

	if _, exists := s.mem.objects[key.String()]; exists {
		return fmt.Errorf("put %s: %w", key, blob.ErrExists)
	}

	s.mem.objects[key.String()] = data
	s.mem.times[key.String()] = time.Now().UTC()

	return nil
}

func (s *truncateStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	data, ok := s.mem.get(key)
	if !ok {
		return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
	}

	return io.NopCloser(bytes.NewReader(data[:min(len(data), truncateAt)])), nil
}

func (s *truncateStore) Delete(_ context.Context, key blob.Key) error {
	s.mem.delete(key)

	return nil
}

func (s *truncateStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	return s.mem.listPage(ctx, req)
}

func (s *truncateStore) Ping(ctx context.Context) error {
	return s.mem.ping(ctx)
}

// leakStore keeps the partial bytes of a Put whose reader fails, though it
// still reports the read error. The failed-put case must find the key
// present instead of absent.
type leakStore struct {
	mem *memObjects
}

func newLeakStore() *leakStore {
	return &leakStore{mem: newMemObjects()}
}

func (s *leakStore) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, readErr := io.ReadAll(r)

	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()

	if _, exists := s.mem.objects[key.String()]; exists {
		return fmt.Errorf("put %s: %w", key, blob.ErrExists)
	}

	s.mem.objects[key.String()] = data
	s.mem.times[key.String()] = time.Now().UTC()

	if readErr != nil {
		return fmt.Errorf("put %s: %w", key, readErr)
	}

	return nil
}

func (s *leakStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	if data, ok := s.mem.get(key); ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
}

func (s *leakStore) Delete(_ context.Context, key blob.Key) error {
	s.mem.delete(key)

	return nil
}

func (s *leakStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	return s.mem.listPage(ctx, req)
}

func (s *leakStore) Ping(ctx context.Context) error {
	return s.mem.ping(ctx)
}

// listBase is the correct behavior the List fakes corrupt: memObjects with
// write-once Put, so only List differs between the fakes below.
type listBase struct {
	mem *memObjects
}

func newListBase() *listBase {
	return &listBase{mem: newMemObjects()}
}

func (s *listBase) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}

	s.mem.mu.Lock()
	defer s.mem.mu.Unlock()

	if _, exists := s.mem.objects[key.String()]; exists {
		return fmt.Errorf("put %s: %w", key, blob.ErrExists)
	}

	s.mem.objects[key.String()] = data
	s.mem.times[key.String()] = time.Now().UTC()

	return nil
}

func (s *listBase) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	if data, ok := s.mem.get(key); ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	return nil, fmt.Errorf("get %s: %w", key, blob.ErrNotFound)
}

func (s *listBase) Ping(ctx context.Context) error {
	return s.mem.ping(ctx)
}

// unorderedStore returns the objects under a prefix in reverse key order.
// The ordered-by-key case must fail on the wrong order.
type unorderedStore struct {
	*listBase
}

func newUnorderedStore() *unorderedStore {
	return &unorderedStore{listBase: newListBase()}
}

func (s *unorderedStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	got, err := s.mem.listPage(ctx, req)
	if err != nil {
		return blob.Page{}, err
	}

	slices.Reverse(got.Objects)

	return got, nil
}

func (s *unorderedStore) Delete(_ context.Context, key blob.Key) error {
	s.mem.delete(key)

	return nil
}

// badTimeStore reports zero modification times. The ordered-by-key case
// checks each time against the puts, so it must fail.
type badTimeStore struct {
	*listBase
}

func newBadTimeStore() *badTimeStore {
	return &badTimeStore{listBase: newListBase()}
}

func (s *badTimeStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	got, err := s.mem.listPage(ctx, req)
	if err != nil {
		return blob.Page{}, err
	}

	for i := range got.Objects {
		got.Objects[i].ModifiedAt = time.Time{}
	}

	return got, nil
}

func (s *badTimeStore) Delete(_ context.Context, key blob.Key) error {
	s.mem.delete(key)

	return nil
}

// extraStore leaks one object that was never stored, as a store that lists
// temporary or stray files would. The ordered-by-key case expects the exact
// set, so it must fail on the extra key.
type extraStore struct {
	*listBase
}

func newExtraStore() *extraStore {
	return &extraStore{listBase: newListBase()}
}

func (s *extraStore) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	got, err := s.mem.listPage(ctx, req)
	if err != nil {
		return blob.Page{}, err
	}

	key, err := blob.ParseKey("tofu/ws-1/sv-9/a9")
	if err != nil {
		return blob.Page{}, err
	}

	got.Objects = append(got.Objects, blob.Object{ModifiedAt: time.Now().UTC(), Key: key})

	return got, nil
}

func (s *extraStore) Delete(_ context.Context, key blob.Key) error {
	s.mem.delete(key)

	return nil
}
