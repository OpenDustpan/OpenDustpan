package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// TestDeleteManualLockMatchesTheLockThatWasRead sends the delete query a lock
// that differs from the stored lock, as if the row lock and the holder check of
// RemoveLock had failed. The query must remove no row for a changed holder or a
// changed time, and must remove the one row when both match.
func TestDeleteManualLockMatchesTheLockThatWasRead(t *testing.T) {
	t.Parallel()

	s, ns, _ := membershipSetup(t)
	ctx := t.Context()
	since := time.UnixMilli(1_700_000_300_000).UTC()
	target := manuallock.CollectionTarget(collection.Ref{Namespace: ns.Name, Name: collection.Name("payments")})
	l := manuallock.Lock{Since: since, Target: target, Holder: "alice", Reason: "freeze"}

	if err := s.PutLock(ctx, l, ns.Fence(), membershipEntry(since, ns.Name, "collection:lock")); err != nil {
		t.Fatalf("PutLock: %v", err)
	}

	id, err := manualLockObject(ctx, s.queries, target)
	if err != nil {
		t.Fatalf("manualLockObject: %v", err)
	}

	for _, tc := range []struct {
		name   string
		holder string
		since  time.Time
		want   int64
	}{
		{name: "another holder", holder: "bob", since: since, want: 0},
		{name: "another time", holder: "alice", since: since.Add(time.Millisecond), want: 0},
		{name: "the lock that was read", holder: "alice", since: since, want: 1},
	} {
		n, err := s.queries.DeleteManualLock(ctx, db.DeleteManualLockParams{
			Kind: string(target.Kind), ObjectID: id, Holder: tc.holder, SinceMs: codec.Millis(tc.since),
		})
		if err != nil {
			t.Fatalf("%s: DeleteManualLock: %v", tc.name, err)
		}

		if n != tc.want {
			t.Errorf("%s: rows deleted = %d, want %d", tc.name, n, tc.want)
		}
	}
}

// TestRemoveLockRefusesALockThatChangedAfterTheRead changes the lock between the
// read and the delete of RemoveLock, as if the row lock had failed: the hook
// ends the lock of alice and takes a new one for bob in the same transaction.
// The delete then matches no row. The removal must return ErrLockChanged, end
// no lock and write no audit entry that ends one.
func TestRemoveLockRefusesALockThatChangedAfterTheRead(t *testing.T) {
	t.Parallel()

	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "holder", true: "forced"}[force], func(t *testing.T) {
			t.Parallel()

			s, ns, _ := membershipSetup(t)
			ctx := t.Context()
			since := time.UnixMilli(1_700_000_300_000).UTC()
			target := manuallock.CollectionTarget(collection.Ref{Namespace: ns.Name, Name: collection.Name("payments")})
			l := manuallock.Lock{Since: since, Target: target, Holder: "alice", Reason: "freeze"}

			if err := s.PutLock(ctx, l, ns.Fence(), membershipEntry(since, ns.Name, "collection:lock")); err != nil {
				t.Fatalf("PutLock: %v", err)
			}

			s.beforeLockDelete = replaceLock(target, ns.Name, since)

			entry := membershipEntry(since, ns.Name, "collection:unlock")

			var err error
			if force {
				_, err = s.ForceRemoveLock(ctx, target, ns.Fence(), entry)
			} else {
				_, err = s.RemoveLock(ctx, target, "alice", ns.Fence(), entry)
			}

			if !errors.Is(err, manuallock.ErrLockChanged) {
				t.Fatalf("err = %v, want ErrLockChanged", err)
			}

			s.beforeLockDelete = nil

			got, err := s.GetLock(ctx, target)
			if err != nil || got.Holder != "alice" {
				t.Errorf("after the refused removal: lock = %+v, err = %v, want the lock of alice", got, err)
			}
		})
	}
}

// replaceLock returns a hook that ends the lock of alice on target and takes a
// new lock for bob in the transaction of the caller.
func replaceLock(
	target manuallock.Target, ns namespace.Name, since time.Time,
) func(context.Context, *db.Queries) error {
	return func(ctx context.Context, q *db.Queries) error {
		id, err := manualLockObject(ctx, q, target)
		if err != nil {
			return err
		}

		kind := string(target.Kind)
		ended := db.DeleteManualLockParams{Kind: kind, ObjectID: id, Holder: "alice", SinceMs: codec.Millis(since)}

		if _, err = q.DeleteManualLock(ctx, ended); err != nil {
			return err
		}

		_, err = q.InsertManualLock(ctx, db.InsertManualLockParams{
			Kind: kind, ObjectID: id, Namespace: ns.String(), Holder: "bob", Reason: "new",
			SinceMs: codec.Millis(since.Add(time.Second)),
		})

		return err
	}
}

// TestDeleteManualLocksOfObjectFiltersByKind stores two locks of different kinds
// under one object ID, which the real IDs never give, and deletes by the kind
// of one. The lock of the other kind must stay. This is a defense in depth
// layer: the IDs of units, collections and namespaces are distinct today.
func TestDeleteManualLocksOfObjectFiltersByKind(t *testing.T) {
	t.Parallel()

	s, ns, _ := membershipSetup(t)
	ctx := t.Context()
	const shared = "id-shared"

	for _, kind := range []manuallock.Kind{manuallock.KindUnit, manuallock.KindCollection, manuallock.KindNamespace} {
		_, err := s.queries.InsertManualLock(ctx, db.InsertManualLockParams{
			Kind: string(kind), ObjectID: shared, Namespace: ns.Name.String(), Holder: "alice", Reason: "freeze",
			SinceMs: 1_700_000_300_000,
		})
		if err != nil {
			t.Fatalf("InsertManualLock %s: %v", kind, err)
		}
	}

	err := s.queries.DeleteManualLocksOfObject(ctx, db.DeleteManualLocksOfObjectParams{
		Kind: string(manuallock.KindCollection), ObjectID: shared,
	})
	if err != nil {
		t.Fatalf("DeleteManualLocksOfObject: %v", err)
	}

	for kind, want := range map[manuallock.Kind]bool{
		manuallock.KindUnit: true, manuallock.KindCollection: false, manuallock.KindNamespace: true,
	} {
		_, err := s.queries.GetManualLock(ctx, db.GetManualLockParams{Kind: string(kind), ObjectID: shared})
		if got := err == nil; got != want {
			t.Errorf("lock of kind %s present = %v (err %v), want %v", kind, got, err, want)
		}
	}
}
