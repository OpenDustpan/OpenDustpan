package storetest

import (
	"crypto/md5" //nolint:gosec // The journal row names the batch by its MD5, as the manifest does.
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/stack"
)

// testNoCollectionRefusesStateWrites sends each state write of a running update
// to a stack that no collection holds. Each store method must refuse with
// collection.ErrNoCollection in its own transaction, and must write nothing.
// The refusal comes before the check of the update and of the lease, so the
// update of the case needs no row.
func testNoCollectionRefusesStateWrites(t *testing.T, f fixture) {
	t.Helper()

	st := newStack(f.ref.Namespace, "web", "unplaced")
	if err := f.s.CreateStack(t.Context(), st, f.fence); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	u := newUpdate(st.Ref, "u-unplaced", mutating(), "", 0)
	running := started(t, u)
	cp := f.checkpoint(t, running, 1, `{"version":3,"deployment":{}}`)
	row := stack.JournalRow{
		Ref: st.Ref, Update: running.ID, Lease: running.Lease.Digest, BlobKey: cp.BlobKey,
		SHA256: [sha256.Size]byte{}, MD5: [md5.Size]byte{}, Size: 0, SequenceIDs: []int64{1},
		OperationIDs: nil, HasRebuild: false, Verify: nil, AttemptStartedAt: time.Time{},
	}

	cases := []struct {
		write func() error
		name  string
	}{
		{name: "create update", write: func() error {
			return f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), f.fence)
		}},
		{name: "start update", write: func() error {
			_, err := f.s.StartUpdate(t.Context(), running, f.fence)

			return err
		}},
		{name: "put checkpoint", write: func() error {
			_, err := f.s.PutCheckpoint(t.Context(), cp, f.fence)

			return err
		}},
		{name: "put journal row", write: func() error {
			_, err := f.s.PutJournalRow(t.Context(), row, f.fence)

			return err
		}},
		{name: "put built checkpoint", write: func() error {
			_, err := f.s.PutBuiltCheckpoint(t.Context(), cp, f.fence)

			return err
		}},
	}

	for _, tc := range cases {
		if err := tc.write(); !errors.Is(err, collection.ErrNoCollection) {
			t.Errorf("%s on a stack in no collection: err = %v, want collection.ErrNoCollection", tc.name, err)
		}
	}

	if _, err := f.s.GetUpdate(t.Context(), st.Ref, u.ID); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("GetUpdate after the refused writes: err = %v, want ErrUpdateNotFound", err)
	}

	// The same writes pass once a collection holds the stack, so the refusal
	// is the placement and not another check.
	PlaceStack(t, f.s, st.Ref)

	if err := f.s.CreateUpdate(t.Context(), u, UnrecordedKey(), f.fence); err != nil {
		t.Fatalf("create update after the stack is in a collection: %v", err)
	}

	if _, err := f.s.StartUpdate(t.Context(), running, f.fence); err != nil {
		t.Fatalf("start update after the stack is in a collection: %v", err)
	}
}
