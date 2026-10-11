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
	"os/exec"
	"strings"
	"sync"
	"testing"

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

// runSingleCaseAgainstFake re-executes the calling test in a child process
// filtered to the single subtest matching subtest, with the fake store
// selected, and returns its output. It fails the test when the child passes,
// since the filtered case must catch the fake.
func runSingleCaseAgainstFake(t *testing.T, fake, subtest string) string {
	t.Helper()

	//nolint:gosec // The child is this test binary with a fixed filter; both come from the test itself.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$/"+subtest, "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), blobtestFake+"="+fake)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("case matching %q passed against the %s store, want failure", subtest, fake)
	}

	return string(out)
}

// memObjects holds objects by key for the fake stores below. Fakes hold it
// by pointer, since it holds a mutex.
type memObjects struct {
	objects map[string][]byte
	mu      sync.Mutex
}

func newMemObjects() *memObjects {
	return &memObjects{objects: map[string][]byte{}, mu: sync.Mutex{}}
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

func (s *leakStore) Ping(ctx context.Context) error {
	return s.mem.ping(ctx)
}
