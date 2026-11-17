package backup_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
)

// errScopeClaimMoved is the answer of a claim check that the test makes fail.
var errScopeClaimMoved = errors.New("claim moved")

// orphanKeys are the objects of the stale attempts that cleanup removes.
func orphanKeys() []string {
	return []string{scopeKey("1", "manifest.json"), scopeKey("1", "tables/a.ndjson"), scopeKey("2", "tables/a.ndjson")}
}

// seedOrphans stores a finished attempt 3 with its pointer, the files of the
// attempts 1 and 2, a directory that is not an attempt, and the files of other
// backups. It returns the names that cleanup must keep.
func seedOrphans(t *testing.T, store *memStore) []string {
	t.Helper()

	// A directory name that is a valid key segment but is not an attempt.
	notAttempt := scopeKey(strings.Repeat("9", backup.MaxAttemptLengthForTest+1), "x")
	keep := []string{
		scopeKey("3", "manifest.json"), scopeKey("3", "tables/a.ndjson"), livePointerKeyOf(scopeID), notAttempt,
		"backups/bkp-scope0/1/x", "backups/bkp-other/1/x",
	}

	for _, name := range append(orphanKeys(), keep...) {
		if name == livePointerKeyOf(scopeID) {
			continue
		}

		store.storeObject(t, name, []byte("x"))
	}

	if err := backup.SeedLivePointerForTest(t.Context(), store, scopeID, "3"); err != nil {
		t.Fatalf("write the live pointer: %v", err)
	}

	return keep
}

// assertKept fails the test for each name that the store no longer holds.
func assertKept(t *testing.T, store *memStore, names []string) {
	t.Helper()

	for _, name := range names {
		if !store.has(name) {
			t.Errorf("cleanup deleted %q", name)
		}
	}
}

// TestOrphanPrefixCleanup proves that the live holder deletes the orphaned
// attempt directories, keeps the live one, and removes a stale directory that
// a stale writer recreates.
func TestOrphanPrefixCleanup(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	keep := seedOrphans(t, store)
	svc := backup.NewFencedServiceForTest(store, claimHeld)

	if err := svc.CleanupOrphansForTest(t.Context(), scopeID, "3"); err != nil {
		t.Fatalf("cleanup = %v, want nil", err)
	}

	for _, gone := range orphanKeys() {
		if store.has(gone) {
			t.Errorf("cleanup kept the orphan %q", gone)
		}
	}

	assertKept(t, store, keep)

	// A stale writer creates its directory again after the first pass.
	recreated := scopeKey("1", "tables/b.ndjson")
	store.storeObject(t, recreated, []byte("b"))

	if err := svc.CleanupOrphansForTest(t.Context(), scopeID, "3"); err != nil {
		t.Fatalf("second cleanup = %v, want nil", err)
	}

	if store.has(recreated) {
		t.Error("the second pass kept the recreated stale directory")
	}

	assertKept(t, store, keep)
}

// TestOrphanCleanupWithoutAClaimDeletesNothing proves that a holder that lost
// the claim deletes no object.
func TestOrphanCleanupWithoutAClaimDeletesNothing(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	keep := seedOrphans(t, store)
	svc := backup.NewFencedServiceForTest(store, claimGone)

	if err := svc.CleanupOrphansForTest(t.Context(), scopeID, "3"); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("cleanup = %v, want ErrClaimLost", err)
	}

	assertKept(t, store, keep)
	assertKept(t, store, orphanKeys())
}

// TestOrphanCleanupStopsWhenTheClaimMoves proves that a claim lost in the
// middle of the cleanup leaves the rest of the orphans for the live holder.
func TestOrphanCleanupStopsWhenTheClaimMoves(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	keep := seedOrphans(t, store)

	var calls atomic.Int64

	check := func(context.Context) error {
		if calls.Add(1) > 1 {
			return errScopeClaimMoved
		}

		return nil
	}

	svc := backup.NewFencedServiceForTest(store, check)
	if err := svc.CleanupOrphansForTest(t.Context(), scopeID, "3"); !errors.Is(err, errScopeClaimMoved) {
		t.Fatalf("cleanup = %v, want the claim error", err)
	}

	assertKept(t, store, keep)

	left := 0

	for _, name := range orphanKeys() {
		if store.has(name) {
			left++
		}
	}

	if want := len(orphanKeys()) - 1; left != want {
		t.Errorf("%d orphan objects stayed, want %d after one delete", left, want)
	}
}
