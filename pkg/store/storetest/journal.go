package storetest

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalRow returns the row of a batch of update u that holds the given sequence
// IDs. The content is named by the sha byte, so two rows with one sequence ID and
// different sha bytes hold different content.
func journalRow(t *testing.T, s UpdatesStore, u stack.Update, sha byte, seqs ...int64) stack.JournalRow {
	t.Helper()

	st, err := s.GetStack(t.Context(), u.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if len(seqs) == 0 {
		t.Fatal("a journal row needs a sequence ID")
	}

	low := seqs[0]
	for _, seq := range seqs[1:] {
		low = min(low, seq)
	}

	sum := [sha256.Size]byte{sha}
	ops := make([]int64, len(seqs))

	return stack.JournalRow{
		Ref:          u.Ref,
		Update:       u.ID,
		Lease:        u.Lease.Digest,
		BlobKey:      blob.Key(fmt.Sprintf("pulumi/%s/%s/journal/%d", st.ID, u.ID, low)),
		SHA256:       sum,
		Size:         1,
		SequenceIDs:  seqs,
		OperationIDs: ops,
	}
}

// testJournalPut shows the write of a batch row: a new batch gets the next arrival,
// a retry with the same content writes nothing, and a sequence ID that is stored
// with other content is a conflict. A refused batch stores none of its sequences.
func testJournalPut(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	first := journalRow(t, f.s, u, 1, 1, 2)
	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || retried {
		t.Fatalf("first batch: retried %t, err %v; want stored", retried, err)
	}

	second := journalRow(t, f.s, u, 2, 3)
	if retried, err := f.s.PutJournalRow(t.Context(), second, f.fence); err != nil || retried {
		t.Fatalf("second batch: retried %t, err %v; want stored", retried, err)
	}

	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || !retried {
		t.Fatalf("retry of the first batch: retried %t, err %v; want a retry with no error", retried, err)
	}

	conflict := journalRow(t, f.s, u, 9, 2)
	if _, err := f.s.PutJournalRow(t.Context(), conflict, f.fence); !errors.Is(err, stack.ErrJournalConflict) {
		t.Fatalf("sequence 2 with other content: err = %v, want ErrJournalConflict", err)
	}

	partial := journalRow(t, f.s, u, 1, 2, 7)
	if _, err := f.s.PutJournalRow(t.Context(), partial, f.fence); !errors.Is(err, stack.ErrJournalConflict) {
		t.Fatalf("partly stored batch: err = %v, want ErrJournalConflict", err)
	}

	// The refused batch stored nothing, so sequence 7 is still free.
	free := journalRow(t, f.s, u, 4, 7)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 7 after a refusal: retried %t, err %v; want stored", retried, err)
	}
}

// testJournalRefusals shows that the journal refuses a write without a running
// update under the lease, a preview, an unknown stack, a stale fence and a blob key
// that names another stack.
func testJournalRefusals(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	forged := journalRow(t, f.s, u, 1, 1)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if _, err := f.s.PutJournalRow(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	preview := f.run(t, f.ref, "p1", stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0})
	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, preview, 1, 1), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("batch of a preview: err = %v, want ErrWrongUpdateState", err)
	}

	missing := journalRow(t, f.s, u, 1, 1)
	missing.Ref = stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "never-created"}
	if _, err := f.s.PutJournalRow(t.Context(), missing, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch of a missing stack: err = %v, want ErrWrongUpdateState", err)
	}

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion - 1}
	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("batch with a stale fence: err = %v, want namespace.ErrFenced", err)
	}

	wrongKey := journalRow(t, f.s, u, 1, 1)
	wrongKey.BlobKey = blob.Key("pulumi/other/" + u.ID.String() + "/journal/1")
	if _, err := f.s.PutJournalRow(t.Context(), wrongKey, f.fence); err == nil {
		t.Fatal("batch with a blob key of another stack: err = nil, want an error")
	}
}

// testJournalAfterCancel shows that a batch that arrives after the update is
// canceled is refused, as the events are.
func testJournalAfterCancel(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	late := journalRow(t, f.s, u, 1, 1)
	f.end(t, u, stack.StatusCanceled)

	if _, err := f.s.PutJournalRow(t.Context(), late, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch after cancel: err = %v, want ErrWrongUpdateState", err)
	}
}
