package stack_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestLatestStateVersionReadsNoBlob proves that the version of the latest
// state comes from the rows: the read makes no call to the blob store, also
// when the deployment is large.
func TestLatestStateVersionReadsNoBlob(t *testing.T) {
	t.Parallel()

	blobs := newCountingBlob()
	f := newUpdatesWith(t, newClock(), blobs, nil)

	_, err := f.svc.LatestStateVersion(t.Context(), f.ref)
	if !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("LatestStateVersion of a stack with no state = %v, want ErrNoState", err)
	}

	for want := int64(1); want <= 2; want++ {
		s := f.started(t, stack.KindUpdate)

		in := checkpoint(1, envelope(`{"a":1}`))

		if err = f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, in); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		if _, err = f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
			t.Fatalf("complete: %v", err)
		}

		before := blobs.calls()

		got, err := f.svc.LatestStateVersion(t.Context(), f.ref)
		if err != nil || got != want {
			t.Fatalf("LatestStateVersion = %d, %v; want %d", got, err, want)
		}

		if calls := blobs.calls() - before; calls != 0 {
			t.Errorf("LatestStateVersion made %d blob calls, want 0", calls)
		}
	}
}
