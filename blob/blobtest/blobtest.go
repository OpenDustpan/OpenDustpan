// Package blobtest is the conformance suite for blob.Store implementations.
// Every built-in adapter runs it, and custom adapters should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		blobtest.Run(t, func(t *testing.T) blob.Store { return newMyStore(t) })
//	}
package blobtest

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"opendustpan.org/opendustpan/blob"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) blob.Store

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
