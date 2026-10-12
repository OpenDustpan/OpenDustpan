package storetest

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
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

// putAtOnce sends each row to the store from its own goroutine, all at once, and
// returns the retried flag and the error of each call, in the order of rows.
func putAtOnce(t *testing.T, f fixture, rows []stack.JournalRow) ([]bool, []error) {
	t.Helper()

	retried := make([]bool, len(rows))
	errs := make([]error, len(rows))

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range rows {
		wg.Go(func() {
			<-start
			retried[i], errs[i] = f.s.PutJournalRow(t.Context(), rows[i], f.fence)
		})
	}

	close(start)
	wg.Wait()

	return retried, errs
}

// testJournalConcurrentRows shows that two batches of one update that arrive at
// once both store. The arrival numbers come from one read each, so a collision on
// the arrival would fail one of the calls.
func testJournalConcurrentRows(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	rows := []stack.JournalRow{journalRow(t, f.s, u, 1, 1), journalRow(t, f.s, u, 2, 2)}

	retried, errs := putAtOnce(t, f, rows)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent batch %d: %v, want stored", i, err)
		}

		if retried[i] {
			t.Fatalf("concurrent batch %d reported a retry, want stored", i)
		}
	}

	for i := range rows {
		if again, err := f.s.PutJournalRow(t.Context(), rows[i], f.fence); err != nil || !again {
			t.Fatalf("batch %v after the race: retried %t, err %v; want a retry", rows[i].SequenceIDs, again, err)
		}
	}
}

// testJournalConcurrentRetry sends one batch twice at once. One call stores the
// batch and the other reports a retry. Neither fails on the sequence key.
func testJournalConcurrentRetry(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	row := journalRow(t, f.s, u, 1, 1, 2)

	retried, errs := putAtOnce(t, f, []stack.JournalRow{row, row})

	stored, retries := 0, 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent copy %d: %v, want no error", i, err)
		}

		if retried[i] {
			retries++
		} else {
			stored++
		}
	}

	if stored != 1 || retries != 1 {
		t.Fatalf("two copies at once: %d stored and %d retried, want one of each", stored, retries)
	}
}
