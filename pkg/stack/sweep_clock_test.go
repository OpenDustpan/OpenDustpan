package stack_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errClockRead is the failure of a read of the clock of the database.
var errClockRead = errors.New("read of the database clock failed")

// TestSweepTrustsOnlyTheDatabaseClock runs the sweep of an update with an old
// orphan blob under three clocks. The sweep takes now from the database. When
// the clock of the instance runs fast or slow by more than the skew, the run
// deletes nothing, also an orphan that is old by both clocks.
func TestSweepTrustsOnlyTheDatabaseClock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		dbAhead  time.Duration
		failRead bool
		deleted  bool
	}{
		{name: "the clocks agree", dbAhead: 0, failRead: false, deleted: true},
		{name: "the instance runs fast", dbAhead: -blob.AttemptSkew - time.Minute, failRead: false, deleted: false},
		{name: "the instance runs slow", dbAhead: blob.AttemptSkew + time.Minute, failRead: false, deleted: false},
		{name: "a failed read of the database clock", dbAhead: 0, failRead: true, deleted: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			f.store.dbNow = func() time.Time { return f.clock.Now().Add(tc.dbAhead) }
			if tc.failRead {
				f.store.dbErr = errClockRead
			}

			orphan, mem := sweepWithOrphan(t, f, func(m *memBlob) { m.ageAll(2 * time.Hour) })

			_, err := mem.Get(t.Context(), orphan)

			switch {
			case tc.deleted && !errors.Is(err, blob.ErrNotFound):
				t.Fatalf("orphan blob: %v, want it deleted", err)
			case !tc.deleted && err != nil:
				t.Fatalf("orphan blob: %v, want it kept", err)
			default:
			}
		})
	}
}

// TestSweepKeepsABlobWithNoModificationTime shows that a blob whose store
// gives no modification time is never old: the sweep keeps it.
func TestSweepKeepsABlobWithNoModificationTime(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	orphan, mem := sweepWithOrphan(t, f, func(m *memBlob) {
		m.ageAll(2 * time.Hour)
		m.clearModified()
	})

	if _, err := mem.Get(t.Context(), orphan); err != nil {
		t.Fatalf("orphan blob with no modification time: %v, want it kept", err)
	}
}

// sweepWithOrphan saves a checkpoint, writes an orphan blob of the update,
// changes the blobs with age, and saves again, which sweeps the update. It
// returns the key of the orphan and the blob store.
func sweepWithOrphan(t *testing.T, f updatesFixture, age func(*memBlob)) (blob.Key, *memBlob) {
	t.Helper()

	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	orphan, err := blob.ParseKey(f.prefix + "stk-test/" + s.Update.ID.String() + "/99")
	if err != nil {
		t.Fatalf("parse orphan key: %v", err)
	}

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	if err := mem.Put(ctx, orphan, strings.NewReader("orphan")); err != nil {
		t.Fatalf("write orphan blob: %v", err)
	}

	age(mem)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(2, envelope(`{"a":2}`))); err != nil {
		t.Fatalf("second save: %v", err)
	}

	return orphan, mem
}
