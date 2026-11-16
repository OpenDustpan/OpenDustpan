package backup_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// pointerBytes5 is the one fixed byte form of a pointer that names attempt 5.
// The test pins it: two writers of the same attempt must write these bytes.
const pointerBytes5 = `{"attempt":"5"}`

// replaceStore is a blob.Store whose put replaces an object that exists, as
// a store that ignores conditional writes does.
type replaceStore struct {
	*memStore
}

func (r *replaceStore) Put(ctx context.Context, key blob.Key, rd io.Reader) error {
	if err := r.Delete(ctx, key); err != nil {
		return err
	}

	return r.memStore.Put(ctx, key, rd)
}

// racedReadStore counts the reads of the pointer, so a claim check can fail
// after the reader read the pointer once: the claim moved between the read
// and the put.
type racedReadStore struct {
	*memStore

	pointerReads atomic.Int64
}

func (r *racedReadStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if key.String() == livePointerKeyOf(scopeID) {
		r.pointerReads.Add(1)
	}

	return r.memStore.Get(ctx, key)
}

// lostPutStore simulates a holder that read no pointer and then lost the put
// to a concurrent holder: the first put of the pointer plants the bytes of
// the winner and reports blob.ErrExists.
type lostPutStore struct {
	*memStore

	winner   []byte
	putRaced atomic.Bool
}

func (l *lostPutStore) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if key.String() == livePointerKeyOf(scopeID) && !l.putRaced.Swap(true) {
		if err := l.memStore.Put(ctx, key, bytes.NewReader(l.winner)); err != nil {
			return err
		}

		return fmt.Errorf("put %s: %w", key, blob.ErrExists)
	}

	return l.memStore.Put(ctx, key, r)
}

// pointerStore pairs one target with the in-memory store under it, so a
// table can read and count the objects behind the target.
type pointerStore struct {
	target blob.Store
	base   *memStore
}

// pointerStores returns one store that refuses a second put with ErrExists
// and one that replaces the object, so a table can prove that the writer
// gives the same result on both.
func pointerStores() map[string]pointerStore {
	refusing := newMemStore()
	replacing := newMemStore()

	return map[string]pointerStore{
		"store that refuses a second put": {target: refusing, base: refusing},
		"store that replaces the object":  {target: &replaceStore{memStore: replacing}, base: replacing},
	}
}

// hostilePointer pairs one hostile pointer with the manifest it meets and
// reports whether the pointer alone already fails. A pointer that names a
// valid attempt parses, and only the manifest check of Check refuses it.
type hostilePointer struct {
	name      string
	readFails bool
	manifest  backup.Manifest
	pointer   string
}

// hostilePointers returns the pointers that the reader must refuse: pointers
// that fail alone, pointers that name an attempt with no manifest, and
// manifests that name another attempt or another backup than the pointer.
func hostilePointers(full []byte, oversize string) []hostilePointer {
	good := scopeManifest(scopeID, "5", full)

	return []hostilePointer{
		{name: "no pointer", readFails: true, manifest: good, pointer: ""},
		{name: "pointer is not JSON", readFails: true, manifest: good, pointer: "not json"},
		{name: "truncated pointer", readFails: true, manifest: good, pointer: `{"attempt":"5"`},
		{name: "pointer names dot-dot", readFails: true, manifest: good, pointer: `{"attempt":".."}`},
		{name: "pointer names a path", readFails: true, manifest: good, pointer: `{"attempt":"5/../6"}`},
		{name: "pointer is empty", readFails: true, manifest: good, pointer: `{}`},
		{name: "pointer is too large", readFails: true, manifest: good, pointer: oversize},
		{
			name: "pointer names an overlong attempt", readFails: true, manifest: good,
			pointer: `{"attempt":"` + strings.Repeat("9", backup.MaxAttemptLengthForTest+1) + `"}`,
		},
		{
			name: "pointer names a directory with no manifest", readFails: false, manifest: good,
			pointer: `{"attempt":"6"}`,
		},
		{
			name:      "manifest names another attempt",
			readFails: false,
			manifest:  scopeManifest(scopeID, "6", full),
			pointer:   `{"attempt":"5"}`,
		},
		{
			name:      "manifest names another backup",
			readFails: false,
			manifest:  scopeManifest("bkp-other", "5", full),
			pointer:   `{"attempt":"5"}`,
		},
	}
}

// checkHostilePointer sends one hostile pointer and its manifest to the
// reader of the live pointer. Each case reports backup.ErrCorrupt through
// Check, the pointer-level cases also through readLivePointer, and the target
// serves no list of the parent prefix.
func checkHostilePointer(t *testing.T, tc hostilePointer, full []byte, liveName string) {
	t.Helper()

	store := newMemStore()
	putScopeAttempt(t, store, tc.manifest, scopeFiles{data: full, first: full})

	if tc.pointer != "" {
		store.storeObject(t, liveName, []byte(tc.pointer))
	}

	_, readErr := backup.ReadLivePointerForTest(t.Context(), store, scopeID)
	if tc.readFails && !errors.Is(readErr, backup.ErrCorrupt) {
		t.Fatalf("readLivePointer = %v, want ErrCorrupt", readErr)
	}

	if !tc.readFails && readErr != nil {
		t.Fatalf("readLivePointer = %v, want nil: the pointer alone is valid", readErr)
	}

	if err := backup.Check(t.Context(), store, scopeID); !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("Check = %v, want ErrCorrupt", err)
	}

	if got := store.listCount(); got != 0 {
		t.Fatalf("the reader listed the parent prefix %d times, want no list", got)
	}
}

// TestLivePointerReaderFailsClosed sends hostile pointers and manifests to
// the reader of the live pointer.
func TestLivePointerReaderFailsClosed(t *testing.T) {
	t.Parallel()

	full := []byte("a table file of some length")
	liveName := livePointerKeyOf(scopeID)
	oversize := `{"attempt":"5","pad":"` + strings.Repeat("x", backup.MaxLivePointerBytesForTest) + `"}`

	for _, tc := range hostilePointers(full, oversize) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkHostilePointer(t, tc, full, liveName)
		})
	}
}

// TestLivePointerWriterWritesNothingWithoutAClaim proves the two writer
// guards: a stale claim before the check writes no pointer, and a claim that
// moves between the read of the pointer and the put writes no pointer.
func TestLivePointerWriterWritesNothingWithoutAClaim(t *testing.T) {
	t.Parallel()

	t.Run("stale claim before the check", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()

		err := backup.WriteLivePointerForTest(t.Context(), store, scopeID, "5", claimGone)
		if !errors.Is(err, durable.ErrClaimLost) {
			t.Fatalf("write the pointer with a stale claim = %v, want ErrClaimLost", err)
		}

		if store.has(livePointerKeyOf(scopeID)) {
			t.Fatal("a holder without a claim wrote a pointer")
		}

		if got := store.putAttempts(livePointerKeyOf(scopeID)); got != 0 {
			t.Fatalf("the holder without a claim reached the target %d times", got)
		}
	})

	t.Run("claim moves between the read and the put", func(t *testing.T) {
		t.Parallel()

		store := &racedReadStore{memStore: newMemStore(), pointerReads: atomic.Int64{}}

		// The claim check holds until the reader read the pointer once, then
		// the claim moves: the put must reach no object.
		check := func(context.Context) error {
			if store.pointerReads.Load() >= 1 {
				return durable.ErrClaimLost
			}

			return nil
		}

		err := backup.WriteLivePointerForTest(t.Context(), store, scopeID, "5", check)
		if !errors.Is(err, durable.ErrClaimLost) {
			t.Fatalf("write the pointer after the claim moved = %v, want ErrClaimLost", err)
		}

		if store.has(livePointerKeyOf(scopeID)) {
			t.Fatal("a holder whose claim moved between the read and the put wrote a pointer")
		}
	})
}

func checkFirstWrite(t *testing.T, store pointerStore) {
	t.Helper()

	// The first finish writes the pointer.
	if err := backup.WriteLivePointerForTest(t.Context(), store.target, scopeID, "5", claimHeld); err != nil {
		t.Fatalf("first write = %v, want nil", err)
	}

	if got := string(store.base.readObject(t, livePointerKeyOf(scopeID))); got != pointerBytes5 {
		t.Fatalf("pointer bytes = %q, want the fixed form %q", got, pointerBytes5)
	}
}

// checkSecondWriteFindsThePointerEqual runs a second finish of the same
// backup id: it finds the pointer equal, writes no pointer, and the bytes
// stay.
func checkSecondWriteFindsThePointerEqual(t *testing.T, store pointerStore) {
	t.Helper()

	if err := backup.WriteLivePointerForTest(t.Context(), store.target, scopeID, "5", claimHeld); err != nil {
		t.Fatalf("second write = %v, want nil", err)
	}

	if got := store.base.putAttempts(livePointerKeyOf(scopeID)); got != 1 {
		t.Fatalf("puts of the pointer = %d, want 1", got)
	}

	if got := string(store.base.readObject(t, livePointerKeyOf(scopeID))); got != pointerBytes5 {
		t.Fatalf("pointer bytes after the second run = %q, want the fixed form %q", got, pointerBytes5)
	}
}

// rerunPointerOnBothStores runs the pointer write of one backup id twice,
// against a store that refuses a second put with ErrExists and a store that
// replaces the object.
func rerunPointerOnBothStores(t *testing.T) {
	t.Helper()

	for name, store := range pointerStores() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkFirstWrite(t, store)
			checkSecondWriteFindsThePointerEqual(t, store)
		})
	}
}

// lostPutTo writes the pointer with a store whose first put plants winner
// and reports blob.ErrExists, then checks the pointer bytes.
func lostPutTo(t *testing.T, winner, want string, wantErr error) {
	t.Helper()

	base := newMemStore()
	store := &lostPutStore{memStore: base, winner: []byte(winner), putRaced: atomic.Bool{}}

	err := backup.WriteLivePointerForTest(t.Context(), store, scopeID, "5", claimHeld)
	if wantErr == nil && err != nil {
		t.Fatalf("write after a lost put = %v, want nil: ErrExists alone never decides", err)
	}

	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("write after a lost put of another attempt = %v, want ErrCorrupt", err)
	}

	if got := string(base.readObject(t, livePointerKeyOf(scopeID))); got != want {
		t.Fatalf("pointer bytes = %q, want %q", got, want)
	}
}

// overwritePointerOnBothStores seeds a pointer of another attempt on both
// stores and proves that a write over it fails closed and changes nothing.
func overwritePointerOnBothStores(t *testing.T) {
	t.Helper()

	for name, store := range pointerStores() {
		store.base.storeObject(t, livePointerKeyOf(scopeID), []byte(`{"attempt":"6"}`))

		err := backup.WriteLivePointerForTest(t.Context(), store.target, scopeID, "5", claimHeld)
		if !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("%s: write over a pointer of another attempt = %v, want ErrCorrupt", name, err)
		}

		if got := string(store.base.readObject(t, livePointerKeyOf(scopeID))); got != `{"attempt":"6"}` {
			t.Fatalf("%s: pointer bytes = %q, want the untouched pointer of attempt 6", name, got)
		}

		if got := store.base.putAttempts(livePointerKeyOf(scopeID)); got != 1 {
			t.Fatalf("%s: the store saw %d puts of the pointer, want the seed only", name, got)
		}
	}
}

// TestLivePointerOneResultOnEveryStore runs the pointer write of one backup
// id twice, against a store that refuses a second put with ErrExists and a
// store that replaces the object.
func TestLivePointerOneResultOnEveryStore(t *testing.T) {
	t.Parallel()

	rerunPointerOnBothStores(t)

	t.Run("a put that loses to an equal pointer succeeds", func(t *testing.T) {
		t.Parallel()

		lostPutTo(t, pointerBytes5, pointerBytes5, nil)
	})

	t.Run("a put that loses to another attempt fails closed", func(t *testing.T) {
		t.Parallel()

		lostPutTo(t, `{"attempt":"6"}`, `{"attempt":"6"}`, backup.ErrCorrupt)
	})

	t.Run("a seeded pointer of another attempt stays", func(t *testing.T) {
		t.Parallel()

		overwritePointerOnBothStores(t)
	})
}

// TestLivePointerCleanupCoversThePointer proves the cleanup guards of the
// pointer: no delete before the pointer write, no delete of the prefix that
// the pointer names, no delete of the pointer, and no delete when the
// pointer and the row differ.
func TestLivePointerCleanupCoversThePointer(t *testing.T) {
	t.Parallel()

	t.Run("no pointer, no delete", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		keep := seedOrphans(t, store)

		// Remove the pointer: the cleanup must delete nothing, so the keep
		// list drops it too.
		if err := store.Delete(t.Context(), mustKey(t, livePointerKeyOf(scopeID))); err != nil {
			t.Fatalf("delete the pointer: %v", err)
		}

		keep = slices.DeleteFunc(keep, func(name string) bool {
			return name == livePointerKeyOf(scopeID)
		})

		svc := backup.NewFencedServiceForTest(store, claimHeld)

		if err := svc.CleanupOrphansForTest(t.Context(), scopeID, "3"); !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("cleanup with no pointer = %v, want ErrCorrupt", err)
		}

		assertKept(t, store, keep)
		assertKept(t, store, orphanKeys())
	})

	t.Run("pointer and row differ, no delete", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		keep := seedOrphans(t, store)

		// The pointer names attempt 2 while the row records 3.
		if err := store.Delete(t.Context(), mustKey(t, livePointerKeyOf(scopeID))); err != nil {
			t.Fatalf("delete the seeded pointer: %v", err)
		}

		store.storeObject(t, livePointerKeyOf(scopeID), []byte(`{"attempt":"2"}`))

		svc := backup.NewFencedServiceForTest(store, claimHeld)

		if err := svc.CleanupOrphansForTest(t.Context(), scopeID, "3"); !errors.Is(err, backup.ErrCorrupt) {
			t.Fatalf("cleanup with a differing pointer = %v, want ErrCorrupt", err)
		}

		// The pointed prefix stays too: the cleanup deletes nothing.
		assertKept(t, store, keep)
		assertKept(t, store, orphanKeys())
	})

	t.Run("the pointer and the live prefix stay", func(t *testing.T) {
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

		// The pointer names 3, so the prefix of 3 and the pointer itself stay.
		assertKept(t, store, keep)
	})
}
