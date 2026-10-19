package postgres

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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
