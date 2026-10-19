package idempotencytest

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/secret"
)

// keyBytes is the size of a key-encryption key of TestWrapper.
const keyBytes = 32

// The seeds of the key-encryption keys of the sealed-key cases.
const (
	seedFirst  byte = 1
	seedSecond byte = 2
)

// TestWrapper returns a key-encryption key for tests. Two calls with the same
// seed give the same key, and two seeds give two keys with two IDs.
//
//nolint:ireturn // The caller keeps the wrapper as a secret.Wrapper port.
func TestWrapper(t *testing.T, seed byte) secret.Wrapper {
	t.Helper()

	w, err := secret.NewBuiltinWrapper(bytes.Repeat([]byte{seed}, keyBytes))
	if err != nil {
		t.Fatalf("make key-encryption key: %v", err)
	}

	return w
}

// sameIDWrapper has the ID of another wrapper and the key of its own: it can
// not open what the other wrapper sealed, and its ID does not tell that.
type sameIDWrapper struct {
	secret.Wrapper

	id string
}

// ID implements secret.Wrapper.
func (w sameIDWrapper) ID() string { return w.id }

// SealedDB is the part of a store test that the cases of a sealed server key
// need: stores on one database, and the raw rows of that database.
type SealedDB interface {
	// Open returns a new store value on the same database that seals with w.
	Open(t *testing.T, w secret.Wrapper) idempotency.Store
	// OpenMany returns one store value for each wrapper, all open at the same
	// time on the same database. Each value keeps its own server key, as the
	// processes of a deployment do.
	OpenMany(t *testing.T, ws ...secret.Wrapper) []idempotency.Store
	// Wipe deletes every row of the server key table and of the record table,
	// as an online restore of an empty backup does.
	Wipe(t *testing.T)
	// RawServerKey returns the bytes that the server key row holds.
	RawServerKey(t *testing.T) []byte
	// RawFingerprint returns the fingerprint of the only record.
	RawFingerprint(t *testing.T) []byte
	// RecordCount returns the number of records.
	RecordCount(t *testing.T) int
}

// SealedFactory returns a fresh, empty database for one subtest.
type SealedFactory func(t *testing.T) SealedDB

// RunSealed runs the cases of the server key that the key-encryption key
// seals. A store that keeps its key in a database runs them, in addition to
// Run.
func RunSealed(t *testing.T, newDB SealedFactory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, d SealedDB)
	}{
		{"the stored row is not the key of the fingerprints", testSealedRow},
		{"a new store value on the same database still replays", testSealedReplay},
		{"another key-encryption key resets the key and the records", testSealedOtherKey},
		{"a key-encryption key with the same ID and another key resets too", testSealedSameID},
		{"a process whose server key row changed replays the records of the new row", testSealedStaleKey},
		{"two processes with a new key-encryption key reset the key to one value", testSealedConcurrentReset},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newDB(t))
		})
	}
}

// completed makes a record with the final answer on s.
func completed(t *testing.T, s idempotency.Store) {
	t.Helper()

	first := started(t, s, ref("k1"), epoch())

	if err := s.Complete(t.Context(), ref("k1"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func testSealedRow(t *testing.T, d SealedDB) {
	t.Helper()

	completed(t, d.Open(t, TestWrapper(t, seedFirst)))

	raw := d.RawServerKey(t)
	if len(raw) == 0 {
		t.Fatal("the server key row is empty")
	}

	if bytes.Equal(idempotency.Fingerprint(raw, payload()), d.RawFingerprint(t)) {
		t.Fatal("the fingerprint matches the key that the row holds: the row is not sealed")
	}
}

func testSealedReplay(t *testing.T, d SealedDB) {
	t.Helper()

	completed(t, d.Open(t, TestWrapper(t, seedFirst)))

	got := begin(t, d.Open(t, TestWrapper(t, seedFirst)), ref("k1"), payload(), epoch().Add(time.Minute))
	wantOutcome(t, got, idempotency.Replayed)
}

func testSealedOtherKey(t *testing.T, d SealedDB) {
	t.Helper()

	completed(t, d.Open(t, TestWrapper(t, seedFirst)))

	before := d.RawServerKey(t)

	got := begin(t, d.Open(t, TestWrapper(t, seedSecond)), ref("k1"), payload(), epoch().Add(time.Minute))
	wantOutcome(t, got, idempotency.Started)

	if bytes.Equal(before, d.RawServerKey(t)) {
		t.Fatal("the server key row did not change")
	}

	if n := d.RecordCount(t); n != 1 {
		t.Fatalf("records = %d, want 1: only the new pending record", n)
	}
}

func testSealedSameID(t *testing.T, d SealedDB) {
	t.Helper()

	first := TestWrapper(t, seedFirst)
	completed(t, d.Open(t, first))

	other := sameIDWrapper{Wrapper: TestWrapper(t, seedSecond), id: first.ID()}

	got := begin(t, d.Open(t, other), ref("k1"), payload(), epoch().Add(time.Minute))
	wantOutcome(t, got, idempotency.Started)
}

func testSealedStaleKey(t *testing.T, d SealedDB) {
	t.Helper()

	w := TestWrapper(t, seedFirst)
	stores := d.OpenMany(t, w, w)
	a, b := stores[0], stores[1]
	at := epoch().Add(time.Minute)

	wantOutcome(t, begin(t, a, ref("k0"), payload(), at), idempotency.Started)

	d.Wipe(t)

	wantOutcome(t, begin(t, b, ref("k0"), payload(), at), idempotency.Started)

	first := started(t, a, ref("k1"), at)

	if err := a.Complete(t.Context(), ref("k1"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	wantOutcome(t, begin(t, b, ref("k1"), payload(), at), idempotency.Replayed)
}

func testSealedConcurrentReset(t *testing.T, d SealedDB) {
	t.Helper()

	completed(t, d.Open(t, TestWrapper(t, seedFirst)))

	w := TestWrapper(t, seedSecond)
	stores := d.OpenMany(t, w, w)
	a, b := stores[0], stores[1]
	at := epoch().Add(time.Minute)

	var wg sync.WaitGroup

	for i, s := range stores {
		wg.Go(func() {
			if _, err := s.Begin(t.Context(), ref(fmt.Sprintf("c%d", i)), payload(), at); err != nil {
				t.Errorf("begin on store %d: %v", i, err)
			}
		})
	}

	wg.Wait()

	if t.Failed() {
		return
	}

	first := started(t, a, ref("k2"), at)

	if err := a.Complete(t.Context(), ref("k2"), first.Token, answer()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	wantOutcome(t, begin(t, b, ref("k2"), payload(), at), idempotency.Replayed)
}
