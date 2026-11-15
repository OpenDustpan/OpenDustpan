package storetest

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalRow returns the row of a batch of update u that holds the given sequence
// IDs. The content is named by the sha byte, so two rows with one sequence ID and
// different sha bytes hold different content.
func journalRow(t *testing.T, s UpdatesStore, u stack.Update, sha byte, seqs ...int64) stack.JournalRow {
	t.Helper()

	return journalRowAttempt(t, s, u, journalWrite{sha: sha, attempt: defaultAttempt, seqs: seqs})
}

// journalWrite is one write attempt of a batch: the content (sha), the ID of
// the attempt and the sequence IDs of the batch.
type journalWrite struct {
	sha     byte
	attempt string
	seqs    []int64
}

// journalRowAttempt returns journalRow with the blob key of the write attempt
// with the ID attempt: journal/<lowest sequence ID>-<attempt ID>.
func journalRowAttempt(t *testing.T, s UpdatesStore, u stack.Update, w journalWrite) stack.JournalRow {
	t.Helper()

	sha, attempt, seqs := w.sha, w.attempt, w.seqs

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
		Ref:    u.Ref,
		Update: u.ID,
		Lease:  u.Lease.Digest,
		BlobKey: blob.Key(
			fmt.Sprintf("%s%s/%s/journal/%d-%s", stackBlobPrefix(t, s, u.Ref.Namespace), st.ID, u.ID, low, attempt),
		),
		SHA256:       sum,
		MD5:          [16]byte{sha},
		Size:         1,
		SequenceIDs:  seqs,
		OperationIDs: ops,
		HasRebuild:   false,
		Verify:       acceptEntries,
		// The attempt starts now, so the store accepts it in its time window.
		AttemptStartedAt: time.Now(),
	}
}

// acceptEntries is the entry check of a journal row that accepts every batch.
func acceptEntries([]int64, bool) (bool, error) { return false, nil }

// runJournaled creates and starts a mutating update with journal version 1, so it
// saves journal batches.
func (f fixture) runJournaled(t *testing.T, ref stack.Ref, id string) stack.Update {
	t.Helper()

	u := f.create(t, ref, id, mutating())
	u.JournalVersion = 1

	return f.start(t, u)
}

// testJournalPut shows the write of a batch row: a new batch gets the next arrival,
// a retry with the same content writes nothing, and a sequence ID that is stored
// with other content is a conflict. A refused batch stores none of its sequences.
func testJournalPut(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

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

	u := f.runJournaled(t, f.ref, "u1")

	forged := journalRow(t, f.s, u, 1, 1)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if _, err := f.s.PutJournalRow(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("batch under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	preview := f.run(
		t,
		f.ref,
		"p1",
		stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
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
	wrongKey.BlobKey = blob.Key(
		stackBlobPrefix(t, f.s, f.ref.Namespace) + "other/" + u.ID.String() + "/journal/1-attempt0000",
	)
	if _, err := f.s.PutJournalRow(t.Context(), wrongKey, f.fence); !errors.Is(err, stack.ErrJournalKey) {
		t.Fatalf("batch with a blob key of another stack: err = %v, want ErrJournalKey", err)
	}

	// The key of a batch of this stack in the blob scope of another namespace,
	// a key without a namespace directory, a key of another directory of the
	// namespace, and a key that is deeper than a batch key are refused too.
	st, err := f.s.GetStack(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	nsID := strings.TrimSuffix(strings.TrimPrefix(stackBlobPrefix(t, f.s, f.ref.Namespace), "namespaces/"), "/pulumi/")

	for name, key := range map[string]string{
		"another namespace":            "namespaces/ns-other/pulumi/" + st.ID.String() + "/" + u.ID.String() + "/journal/1-attempt0000",
		"no namespace directory":       "pulumi/" + st.ID.String() + "/" + u.ID.String() + "/journal/1-attempt0000",
		"a directory of the namespace": "namespaces/" + nsID + "/tofu/" + st.ID.String() + "/" + u.ID.String() + "/journal/1-attempt0000",
		"a nested key":                 stackBlobPrefix(t, f.s, f.ref.Namespace) + st.ID.String() + "/" + u.ID.String() + "/journal/1-attempt0000/2",
		"no attempt ID":                stackBlobPrefix(t, f.s, f.ref.Namespace) + st.ID.String() + "/" + u.ID.String() + "/journal/1",
	} {
		row := journalRow(t, f.s, u, 1, 1)
		row.BlobKey = blob.Key(key)

		if _, err := f.s.PutJournalRow(t.Context(), row, f.fence); !errors.Is(err, stack.ErrJournalKey) {
			t.Fatalf("batch with %s in the key: err = %v, want ErrJournalKey", name, err)
		}
	}
}

// testJournalRefusesVersionZero shows that a batch for an update with journal
// version 0, which saves checkpoints, is refused with ErrNotJournaled and stores
// nothing.
func testJournalRefusesVersionZero(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	row := journalRow(t, f.s, u, 1, 1)

	if _, err := f.s.PutJournalRow(t.Context(), row, f.fence); !errors.Is(err, stack.ErrNotJournaled) {
		t.Fatalf("batch for a version 0 update: err = %v, want ErrNotJournaled", err)
	}

	keys, err := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil || len(keys) != 0 {
		t.Fatalf("journal after a refused batch = %v, %v; want empty", keys, err)
	}
}

// testJournalAfterCancel shows that a batch that arrives after the update is
// canceled is refused, as the events are.
func testJournalAfterCancel(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
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

	u := f.runJournaled(t, f.ref, "u1")
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

	u := f.runJournaled(t, f.ref, "u1")
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

// withOperations sets the operation IDs of a batch row.
func withOperations(row stack.JournalRow, ops ...int64) stack.JournalRow {
	row.OperationIDs = ops

	return row
}

// testJournalVerify shows that the store calls the row's check with the operation IDs
// that the stored rows hold (ADR 43, decision 6.4), and that a refused batch stores
// nothing.
func testJournalVerify(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	first := withOperations(journalRow(t, f.s, u, 1, 1), 10, 11)
	if _, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil {
		t.Fatalf("first batch: %v", err)
	}

	var seen []int64

	refused := withOperations(journalRow(t, f.s, u, 2, 2), 12)
	refused.Verify = func(known []int64, _ bool) (bool, error) {
		seen = append([]int64(nil), known...)

		return false, stack.ErrJournalBatch
	}

	if _, err := f.s.PutJournalRow(t.Context(), refused, f.fence); !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("refused batch: err = %v, want ErrJournalBatch", err)
	}

	slices.Sort(seen)
	if !slices.Equal(seen, []int64{10, 11}) {
		t.Fatalf("the check saw operations %v, want the stored [10 11]", seen)
	}

	// The refused batch stored nothing, so sequence 2 is still free.
	free := withOperations(journalRow(t, f.s, u, 3, 2), 13)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 2 after a refusal: retried %t, err %v; want stored", retried, err)
	}

	accepted := withOperations(journalRow(t, f.s, u, 4, 3), 14)
	accepted.Verify = func([]int64, bool) (bool, error) { return false, nil }

	if retried, err := f.s.PutJournalRow(t.Context(), accepted, f.fence); err != nil || retried {
		t.Fatalf("accepted batch: retried %t, err %v; want stored", retried, err)
	}
}

// testJournalRefusesNilVerify shows that a row with no entry check is refused
// with ErrJournalBatch and stores nothing, so its sequence stays free.
func testJournalRefusesNilVerify(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	unchecked := journalRow(t, f.s, u, 1, 1)
	unchecked.Verify = nil

	if _, err := f.s.PutJournalRow(t.Context(), unchecked, f.fence); !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("row with no entry check: err = %v, want ErrJournalBatch", err)
	}

	// The refused row stored nothing, so sequence 1 is still free.
	free := journalRow(t, f.s, u, 2, 1)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 1 after a refusal: retried %t, err %v; want stored", retried, err)
	}
}

// testJournalRefusesBadSequenceIDs sends rows with hostile sequence IDs straight
// to the store: no sequence ID, a sequence ID of 0, a negative sequence ID and a
// sequence ID that appears twice. Each is refused with ErrJournalBatch and stores
// nothing, so sequence 1 stays free.
func testJournalRefusesBadSequenceIDs(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	for _, tc := range []struct {
		name string
		seqs []int64
	}{
		{name: "nil", seqs: nil},
		{name: "empty", seqs: []int64{}},
		{name: "zero", seqs: []int64{0}},
		{name: "negative", seqs: []int64{-1}},
		{name: "zero after a valid ID", seqs: []int64{1, 0}},
		{name: "repeated", seqs: []int64{1, 1}},
	} {
		row := journalRow(t, f.s, u, 1, 1)
		row.SequenceIDs = tc.seqs
		row.OperationIDs = make([]int64, len(tc.seqs))

		retried, err := f.s.PutJournalRow(t.Context(), row, f.fence)
		if !errors.Is(err, stack.ErrJournalBatch) || retried {
			t.Fatalf("%s sequence IDs: retried %t, err %v; want ErrJournalBatch", tc.name, retried, err)
		}
	}

	// The refused rows stored nothing, so sequence 1 is still free.
	free := journalRow(t, f.s, u, 2, 1)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 1 after the refusals: retried %t, err %v; want stored", retried, err)
	}
}

// journalBeginBody returns a batch body with one begin entry (kind 0) with the
// given sequence and operation IDs. A begin entry changes the replay records.
func journalBeginBody(seq, op int64) []byte {
	return []byte(fmt.Sprintf(
		`{"entries":[{"version":1,"kind":0,"sequenceID":%d,"operationID":%d,`+
			`"removeOld":null,"removeNew":null}]}`,
		seq,
		op,
	))
}

// journalWriteBody returns a batch body with one write entry (kind 5) with the
// given sequence and operation IDs.
func journalWriteBody(seq, op int64) []byte {
	return []byte(fmt.Sprintf(
		`{"entries":[{"version":1,"kind":5,"sequenceID":%d,"operationID":%d,`+
			`"removeOld":null,"removeNew":null,"newSnapshot":{"manifest":`+
			`{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}}]}`,
		seq,
		op,
	))
}

// journalBatchDef describes one batch of the records-changed test: the content
// name, the body the verifier checks, and the sequence and operation IDs of the row.
type journalBatchDef struct {
	sha  byte
	body []byte
	seqs []int64
	ops  []int64
}

// journalRowWithBody returns the row of a batch with the given body, checked by
// the replay verifier, as the journal writer builds it.
func journalRowWithBody(t *testing.T, s UpdatesStore, u stack.Update, def journalBatchDef) stack.JournalRow {
	t.Helper()

	row := journalRow(t, s, u, def.sha, def.seqs...)
	row.OperationIDs = def.ops
	row.Verify = func(known []int64, recordsChanged bool) (bool, error) {
		return pulumijournal.Verifier{}.VerifyBatch(def.body, 1, known, recordsChanged)
	}

	return row
}

// testJournalRefusesWriteAfterStoredChange shows that a write entry in a later
// batch than a record change is refused: the first stored batch that holds a record
// change sets the flag of the update, and the verifier refuses a write entry while
// the flag is set. The refused batch stores nothing. A write entry with no earlier
// record change is accepted.
func testJournalRefusesWriteAfterStoredChange(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	change := journalRowWithBody(t, f.s, u, journalBatchDef{
		sha: 1, body: journalBeginBody(1, 10), seqs: []int64{1}, ops: nil,
	})
	if retried, err := f.s.PutJournalRow(t.Context(), change, f.fence); err != nil || retried {
		t.Fatalf("batch with a record change: retried %t, err %v; want stored", retried, err)
	}

	write := journalRowWithBody(t, f.s, u, journalBatchDef{
		sha: 2, body: journalWriteBody(2, 11), seqs: []int64{2}, ops: nil,
	})

	_, err := f.s.PutJournalRow(t.Context(), write, f.fence)
	if !errors.Is(err, pulumijournal.ErrWriteOrder) {
		t.Fatalf("write after a stored record change: err = %v, want ErrWriteOrder", err)
	}

	if !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("write after a stored record change: err = %v, want ErrJournalBatch", err)
	}

	// The refused batch stored nothing, so sequence 2 is still free.
	free := journalRow(t, f.s, u, 3, 2)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 2 after a refusal: retried %t, err %v; want stored", retried, err)
	}

	// A write entry with no earlier record change is accepted, and writes alone do
	// not set the flag: a second write is accepted too.
	clean := f.runJournaled(t, f.other(t), "d1")

	first := journalRowWithBody(t, f.s, clean, journalBatchDef{
		sha: 4, body: journalWriteBody(1, 10), seqs: []int64{1}, ops: nil,
	})
	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || retried {
		t.Fatalf("write with no record change: retried %t, err %v; want stored", retried, err)
	}

	second := journalRowWithBody(t, f.s, clean, journalBatchDef{
		sha: 5, body: journalWriteBody(2, 11), seqs: []int64{2}, ops: nil,
	})
	if retried, err := f.s.PutJournalRow(t.Context(), second, f.fence); err != nil || retried {
		t.Fatalf("second write with no record change: retried %t, err %v; want stored", retried, err)
	}
}

// testJournalBegunRefusal shows that a batch that names a begun-only operation
// is refused with ErrJournalBatch and stores nothing, so its sequences stay
// free. Operation 20 only begins, so the success entry that names it cannot
// replay.
func testJournalBegunRefusal(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	body := []byte(`{"entries":[` +
		`{"version":1,"kind":0,"sequenceID":1,"operationID":20,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":1,"sequenceID":2,"operationID":21,"removeOld":null,"removeNew":20,` +
		`"state":{"urn":"urn:a","custom":true,"type":"t:mod:T","id":"x"}}]}`)

	refused := withOperations(journalRow(t, f.s, u, 2, 1, 2), 21)
	refused.Verify = func(known []int64, recordsChanged bool) (bool, error) {
		return pulumijournal.Verifier{}.VerifyBatch(body, 1, known, recordsChanged)
	}

	if _, err := f.s.PutJournalRow(t.Context(), refused, f.fence); !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("begun reference: err = %v, want ErrJournalBatch", err)
	}

	// The refused batch stored nothing, so sequences 1 and 2 are still free.
	free := withOperations(journalRow(t, f.s, u, 3, 1, 2), 22)
	free.Verify = func([]int64, bool) (bool, error) { return false, nil }

	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequences 1 and 2 after a refusal: retried %t, err %v; want stored", retried, err)
	}
}

// journalRefusal returns a refusal of update u with the given sequences,
// status and reason.
func journalRefusal(u stack.Update, seqs []int64, status int, reason string) stack.JournalRefusal {
	return stack.JournalRefusal{
		Ref:         u.Ref,
		Update:      u.ID,
		SequenceIDs: seqs,
		Status:      status,
		Reason:      reason,
	}
}

// recordRefusal stores one refusal of the fixture update.
func recordRefusal(t *testing.T, f fixture, refusal stack.JournalRefusal) {
	t.Helper()

	if err := f.s.RecordJournalRefusal(t.Context(), refusal, f.fence); err != nil {
		t.Fatalf("record refusal: %v", err)
	}
}

// readRefusals returns the recorded refusals of update u.
func readRefusals(t *testing.T, f fixture, u stack.Update) []stack.JournalRefusal {
	t.Helper()

	got, err := f.s.JournalRefusals(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil {
		t.Fatalf("read refusals: %v", err)
	}

	return got
}

// checkRefusals compares recorded refusals in arrival order.
func checkRefusals(t *testing.T, got, want []stack.JournalRefusal) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("refusals = %v, want %v", got, want)
	}

	for i := range want {
		if got[i].Ref != want[i].Ref || got[i].Update != want[i].Update ||
			!slices.Equal(got[i].SequenceIDs, want[i].SequenceIDs) ||
			got[i].Status != want[i].Status || got[i].Reason != want[i].Reason {
			t.Fatalf("refusal %d = %+v, want %+v", i, got[i], want)
		}
	}
}

// testJournalRefusalsRoundTrip shows that each recorded refusal reads back in
// arrival order, with the sequence IDs, the status and the reason it was
// given. A refusal without batches leaves no sequences.
func testJournalRefusalsRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	want := []stack.JournalRefusal{
		journalRefusal(u, []int64{5}, 400, "unknown kind"),
		journalRefusal(u, nil, 500, "put journal blob: store unavailable"),
	}

	for _, r := range want {
		recordRefusal(t, f, r)
	}

	checkRefusals(t, readRefusals(t, f, u), want)

	seqs, err := f.s.JournalSequences(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil || len(seqs) != 0 {
		t.Fatalf("sequences with only refusals = %v, %v; want none", seqs, err)
	}
}

// testJournalRefusalsMissingUpdate shows that a refusal of a missing stack or
// update records nothing and returns nil, and that the reads of a missing
// update are empty.
func testJournalRefusalsMissingUpdate(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	id := mustStackID(t, f.s, f.ref)

	foreign := journalRefusal(u, []int64{1}, 400, "refused batch")
	foreign.Ref = stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "never-created"}

	if err := f.s.RecordJournalRefusal(t.Context(), foreign, f.fence); err != nil {
		t.Fatalf("record refusal of a missing stack: %v", err)
	}

	gone := journalRefusal(u, []int64{1}, 400, "refused batch")
	gone.Update = "upd-missing"

	if err := f.s.RecordJournalRefusal(t.Context(), gone, f.fence); err != nil {
		t.Fatalf("record refusal of a missing update: %v", err)
	}

	if got, err := f.s.JournalRefusals(t.Context(), id, "upd-missing"); err != nil || len(got) != 0 {
		t.Fatalf("refusals of a missing update = %v, %v; want none", got, err)
	}

	if seqs, err := f.s.JournalSequences(t.Context(), id, "upd-missing"); err != nil || len(seqs) != 0 {
		t.Fatalf("sequences of a missing update = %v, %v; want none", seqs, err)
	}
}

// testJournalRefusalsOnlyRunning shows that a refusal of an update that no
// longer runs records nothing: the update ended, so the rows stay empty.
func testJournalRefusalsOnlyRunning(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	f.end(t, u, stack.StatusCanceled)

	refusal := journalRefusal(u, []int64{1}, 400, "refused batch")
	if err := f.s.RecordJournalRefusal(t.Context(), refusal, f.fence); err != nil {
		t.Fatalf("record refusal after cancel: %v", err)
	}

	if got := readRefusals(t, f, u); len(got) != 0 {
		t.Fatalf("refusals after cancel = %v, want none", got)
	}
}

// testJournalEndDropsRefusals shows that ending the update drops its refusal
// rows: cancel drops them, and so does failure with no stored batch.
func testJournalEndDropsRefusals(t *testing.T, f fixture) {
	t.Helper()

	canceled := f.runJournaled(t, f.ref, "u1")
	recordRefusal(t, f, journalRefusal(canceled, []int64{1}, 400, "refused batch"))
	f.end(t, canceled, stack.StatusCanceled)

	if got := readRefusals(t, f, canceled); len(got) != 0 {
		t.Fatalf("refusals after cancel = %v, want none", got)
	}

	failed := f.runJournaled(t, f.ref, "u2")
	recordRefusal(t, f, journalRefusal(failed, []int64{1}, 400, "refused batch"))
	f.end(t, failed, stack.StatusFailed)

	if got := readRefusals(t, f, failed); len(got) != 0 {
		t.Fatalf("refusals after failure = %v, want none", got)
	}
}

// testJournalRefusalStaleFence shows that a refusal with a stale fence is
// refused with the fenced sentinel, like any other fenced write.
func testJournalRefusalStaleFence(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion - 1}

	err := f.s.RecordJournalRefusal(t.Context(), journalRefusal(u, []int64{1}, 400, "refused batch"), stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("refusal with a stale fence: err = %v, want namespace.ErrFenced", err)
	}
}

// testJournalSequences shows that the stored sequence IDs of an update read
// back in order.
func testJournalSequences(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 3), f.fence); err != nil {
		t.Fatalf("batch of sequence 3: %v", err)
	}

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 2, 1, 2), f.fence); err != nil {
		t.Fatalf("batch of sequences 1 and 2: %v", err)
	}

	got, err := f.s.JournalSequences(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil {
		t.Fatalf("read sequences: %v", err)
	}

	if !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("sequences = %v, want [1 2 3] in order", got)
	}
}

// testJournalBuildRemovesRefusals shows that the build removes the refusals
// of the update with its journal rows and sequences.
func testJournalBuildRemovesRefusals(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), f.fence); err != nil {
		t.Fatalf("batch: %v", err)
	}

	recordRefusal(t, f, journalRefusal(u, []int64{2}, 400, "unknown kind"))

	if _, err := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, u, 0, "{}"), f.fence); err != nil {
		t.Fatalf("build: %v", err)
	}

	id := mustStackID(t, f.s, f.ref)

	if got, err := f.s.JournalRefusals(t.Context(), id, u.ID); err != nil || len(got) != 0 {
		t.Fatalf("refusals after the build = %v, %v; want none", got, err)
	}

	if seqs, err := f.s.JournalSequences(t.Context(), id, u.ID); err != nil || len(seqs) != 0 {
		t.Fatalf("sequences after the build = %v, %v; want none", seqs, err)
	}
}

// testJournalRefusesReferenceAfterStoredRebuild shows that a stored rebuild
// starts the known set again: batch A holds a success for operation 5, batch B
// holds a rebuild entry, and batch C that names operation 5 is refused at
// arrival with ErrJournalBatch, as the replay refuses it. The refused batch
// stores nothing, so its sequences stay free.
func testJournalRefusesReferenceAfterStoredRebuild(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	state := `{"urn":"urn:a","custom":true,"type":"t:mod:T","id":"x"}`

	success := []byte(`{"entries":[` +
		`{"version":1,"kind":1,"sequenceID":1,"operationID":5,"removeOld":null,"removeNew":null,` +
		`"state":` + state + `}]}`)

	first := journalRowWithBody(t, f.s, u, journalBatchDef{
		sha: 1, body: success, seqs: []int64{1}, ops: []int64{5},
	})
	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || retried {
		t.Fatalf("batch with operation 5: retried %t, err %v; want stored", retried, err)
	}

	rebuild := []byte(`{"entries":[` +
		`{"version":1,"kind":7,"sequenceID":2,"operationID":6,"removeOld":null,"removeNew":null}]}`)

	second := journalRowWithBody(t, f.s, u, journalBatchDef{
		sha: 2, body: rebuild, seqs: []int64{2}, ops: nil,
	})
	second.HasRebuild = true

	if retried, err := f.s.PutJournalRow(t.Context(), second, f.fence); err != nil || retried {
		t.Fatalf("batch with a rebuild: retried %t, err %v; want stored", retried, err)
	}

	referrer := []byte(`{"entries":[` +
		`{"version":1,"kind":1,"sequenceID":3,"operationID":7,"removeOld":null,"removeNew":5,` +
		`"state":` + state + `}]}`)

	third := journalRowWithBody(t, f.s, u, journalBatchDef{
		sha: 3, body: referrer, seqs: []int64{3}, ops: []int64{7},
	})

	_, err := f.s.PutJournalRow(t.Context(), third, f.fence)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("reference after a stored rebuild: err = %v, want ErrMissingReference", err)
	}

	if !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("reference after a stored rebuild: err = %v, want ErrJournalBatch", err)
	}

	// The refused batch stored nothing, so sequence 3 is still free.
	free := journalRow(t, f.s, u, 4, 3)
	if retried, err := f.s.PutJournalRow(t.Context(), free, f.fence); err != nil || retried {
		t.Fatalf("sequence 3 after a refusal: retried %t, err %v; want stored", retried, err)
	}
}

// mustStackID returns the ID of the stack named by ref.
func mustStackID(t *testing.T, s Store, ref stack.Ref) stack.ID {
	t.Helper()

	st, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	return st.ID
}

// sortedKeys returns the keys in order, for comparison.
func sortedKeys(keys []blob.Key) []blob.Key {
	out := slices.Clone(keys)
	slices.Sort(out)

	return out
}

// testJournalBuild shows that the built checkpoint removes the journal of the update
// and returns the blob keys it removed, and that a refused build removes nothing.
func testJournalBuild(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	first := journalRow(t, f.s, u, 1, 1, 2)
	second := journalRow(t, f.s, u, 2, 3)

	rows := []stack.JournalRow{first, second}
	for i := range rows {
		if _, err := f.s.PutJournalRow(t.Context(), rows[i], f.fence); err != nil {
			t.Fatalf("journal batch: %v", err)
		}
	}

	want := []blob.Key{first.BlobKey, second.BlobKey}

	testJournalRefusedBuild(t, f, u, want)

	removed, err := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, u, 0, "{}"), f.fence)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if !slices.Equal(sortedKeys(removed), sortedKeys(want)) {
		t.Fatalf("build removed keys %v, want %v", removed, want)
	}

	after, afterErr := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if afterErr != nil || len(after) != 0 {
		t.Fatalf("journal after the build = %v, %v; want empty", after, afterErr)
	}

	// The journal is closed: a batch after the build is refused, not stored and lost.
	if _, putErr := f.s.PutJournalRow(t.Context(), first, f.fence); !errors.Is(putErr, stack.ErrWrongUpdateState) {
		t.Fatalf("batch after the build: err = %v, want ErrWrongUpdateState", putErr)
	}

	// A retry of the build finds the journal closed and removes nothing more.
	again, err := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, u, 0, "{}"), f.fence)
	if err != nil || len(again) != 0 {
		t.Fatalf("retry of the build: keys %v, err %v; want no keys", again, err)
	}
}

// testJournalRefusedBuild shows that a build refused by a stale fence or by another
// lease removes nothing from the journal of the update.
func testJournalRefusedBuild(t *testing.T, f fixture, u stack.Update, want []blob.Key) {
	t.Helper()

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion - 1}

	_, staleErr := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, u, 0, "{}"), stale)
	if !errors.Is(staleErr, namespace.ErrFenced) {
		t.Fatalf("build with a stale fence: err = %v, want namespace.ErrFenced", staleErr)
	}

	forged := f.checkpoint(t, u, 0, "{}")
	forged.Lease = sha256.Sum256([]byte("another lease"))

	_, forgedErr := f.s.PutBuiltCheckpoint(t.Context(), forged, f.fence)
	if !errors.Is(forgedErr, stack.ErrWrongUpdateState) {
		t.Fatalf("build under another lease: err = %v, want ErrWrongUpdateState", forgedErr)
	}

	got, keysErr := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if keysErr != nil || !slices.Equal(sortedKeys(got), sortedKeys(want)) {
		t.Fatalf("journal after refused builds = %v, %v; want %v", got, keysErr, want)
	}
}

// testJournalBuildRace starts a batch and the build of one update at once. A batch that
// stored before the build has its key among the keys that the build removed. A batch
// that the build beat is refused or stored after it, so its key is not lost.
func testJournalBuildRace(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	row := journalRow(t, f.s, u, 1, 1)

	var (
		wg        sync.WaitGroup
		putErr    error
		removed   []blob.Key
		buildErr  error
		startLine = make(chan struct{})
	)

	wg.Go(func() {
		<-startLine
		_, putErr = f.s.PutJournalRow(t.Context(), row, f.fence)
	})

	wg.Go(func() {
		<-startLine
		removed, buildErr = f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, u, 0, "{}"), f.fence)
	})

	close(startLine)
	wg.Wait()

	if buildErr != nil {
		t.Fatalf("build beside a batch: %v", buildErr)
	}

	// Exactly two outcomes are right. A batch that stored before the build has its key
	// among the removed keys. A batch that the build beat is refused with ErrWrongUpdateState.
	switch {
	case putErr == nil && slices.Contains(removed, row.BlobKey):
	case errors.Is(putErr, stack.ErrWrongUpdateState):
	default:
		t.Fatalf("batch err = %v with removed keys %v: want the batch in the build or ErrWrongUpdateState",
			putErr, removed)
	}
}

// testJournalBuildIsolation shows that a build removes the journal of its own update
// only: the rows of an earlier update of the stack and of another stack survive it.
func testJournalBuildIsolation(t *testing.T, f fixture) {
	t.Helper()

	earlier := f.runJournaled(t, f.ref, "u0")
	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, earlier, 1, 1), f.fence); err != nil {
		t.Fatalf("earlier batch: %v", err)
	}

	f.end(t, earlier, stack.StatusSucceeded)

	current := f.runJournaled(t, f.ref, "u1")
	other := f.runJournaled(t, f.other(t), "d1")

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, other, 2, 2), f.fence); err != nil {
		t.Fatalf("batch of another stack: %v", err)
	}

	if _, err := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, current, 0, "{}"), f.fence); err != nil {
		t.Fatalf("build of the current update: %v", err)
	}

	for _, tc := range []struct {
		ref stack.Ref
		id  stack.UpdateID
	}{{f.ref, earlier.ID}, {other.Ref, other.ID}} {
		keys, err := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, tc.ref), tc.id)
		if err != nil || len(keys) != 1 {
			t.Fatalf("journal of %s after another build = %v, %v; want one batch", tc.id, keys, err)
		}
	}
}

// testJournalBuildRefusals shows that a build refused for a preview or for a stale
// sequence removes nothing from the journal.
func testJournalBuildRefusals(t *testing.T, f fixture) {
	t.Helper()

	preview := f.run(
		t,
		f.other(t),
		"p1",
		stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if _, err := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, preview, 0, "{}"), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("build of a preview: err = %v, want ErrWrongUpdateState", err)
	}

	u := f.runJournaled(t, f.ref, "u1")
	want := []blob.Key{journalRow(t, f.s, u, 1, 1).BlobKey}

	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), f.fence); err != nil {
		t.Fatalf("batch: %v", err)
	}

	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 5, "{}"), f.fence); err != nil {
		t.Fatalf("checkpoint at sequence 5: %v", err)
	}

	if _, err := f.s.PutBuiltCheckpoint(t.Context(), f.checkpoint(t, u, 3, "{}"), f.fence); !errors.Is(
		err, stack.ErrStaleCheckpoint,
	) {
		t.Fatalf("build at a stale sequence: err = %v, want ErrStaleCheckpoint", err)
	}

	keys, err := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil || !slices.Equal(sortedKeys(keys), sortedKeys(want)) {
		t.Fatalf("journal after the refused builds = %v, %v; want %v", keys, err, want)
	}
}

// testJournalSweepable shows when the sweep may delete the journal of an update: only
// when the journal is closed or the update no longer runs. A missing update is an error,
// so a sweep never deletes for a stack it cannot find.
func testJournalSweepable(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	id := mustStackID(t, f.s, f.ref)

	if ok, err := f.s.JournalSweepable(t.Context(), id, u.ID); err != nil || ok {
		t.Fatalf("running update: sweepable %t, err %v; want not sweepable", ok, err)
	}

	if _, err := f.s.JournalSweepable(t.Context(), id, "missing-update"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("missing update: err = %v, want ErrUpdateNotFound", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	if ok, err := f.s.JournalSweepable(t.Context(), id, u.ID); err != nil || !ok {
		t.Fatalf("ended update: sweepable %t, err %v; want sweepable", ok, err)
	}
}

// testJournalKeysFollowRename shows that the journal is keyed by the stack ID, so a rename
// of the stack keeps its journal findable by the ID that its blob keys hold.
func testJournalKeysFollowRename(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	if _, err := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, u, 1, 1), f.fence); err != nil {
		t.Fatalf("batch: %v", err)
	}

	id := mustStackID(t, f.s, f.ref)

	// A rename waits until the update ends.
	f.end(t, u, stack.StatusSucceeded)

	if err := f.s.RenameStack(t.Context(), f.ref, "renamed", f.fence, renameEntry(f.ref.Namespace, f.ref)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	keys, err := f.s.JournalKeys(t.Context(), id, u.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("journal after a rename = %v, %v; want one batch", keys, err)
	}
}

// testReferencedBlobKeysJournal shows that the keys a sweep must keep hold the blobs
// of the journal batches next to the blobs of the checkpoints: a running journaled
// update names its batches, an ended update that still holds journal rows names them
// too, and the keys of another stack stay out.
func testReferencedBlobKeysJournal(t *testing.T, f fixture) {
	t.Helper()

	journaled := f.runJournaled(t, f.ref, "u1")
	want := make([]blob.Key, 0, 3)

	for i := range byte(2) {
		row := journalRow(t, f.s, journaled, i+1, int64(i)+1)
		if _, err := f.s.PutJournalRow(t.Context(), row, f.fence); err != nil {
			t.Fatalf("batch %s: %v", row.BlobKey, err)
		}

		want = append(want, row.BlobKey)
	}

	got, err := f.s.ReferencedBlobKeys(t.Context(), f.stackIDOf(t, f.ref))
	if err != nil {
		t.Fatalf("referenced keys of a running journaled update: %v", err)
	}

	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("referenced keys of a running journaled update = %v, want %v", got, want)
	}

	// An ended update keeps its journal rows until a build removes them.
	f.end(t, journaled, stack.StatusFailed)

	plain := f.run(t, f.ref, "u2", mutating())
	plainKey := f.blobKey(t, plain, 0)

	cp := newBlobCheckpoint(plain, plainKey, 0, `{"v":1}`)
	if _, putErr := f.s.PutCheckpoint(t.Context(), cp, f.fence); putErr != nil {
		t.Fatalf("put checkpoint of u2: %v", putErr)
	}

	// A batch of another stack must not appear.
	otherRef := f.other(t)
	otherUpdate := f.runJournaled(t, otherRef, "u3")

	if _, putErr := f.s.PutJournalRow(t.Context(), journalRow(t, f.s, otherUpdate, 3, 1), f.fence); putErr != nil {
		t.Fatalf("batch of the other stack: %v", putErr)
	}

	got, err = f.s.ReferencedBlobKeys(t.Context(), f.stackIDOf(t, f.ref))
	if err != nil {
		t.Fatalf("referenced keys with checkpoint and journal: %v", err)
	}

	want = append(want, plainKey)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("referenced keys with checkpoint and journal = %v, want %v", got, want)
	}
}

// testJournalReplayOtherKey shows that a batch that arrives again under the key
// of another write attempt is a retry: the store reports retried, writes nothing
// and keeps the key of the first write, so the caller deletes only its own blob.
// A batch with other content is a conflict and keeps the first key too.
func testJournalReplayOtherKey(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	stackID := mustStackID(t, f.s, f.ref)
	first := journalRowAttempt(t, f.s, u, journalWrite{sha: 1, attempt: "attempt0001", seqs: []int64{1, 2}})

	if retried, err := f.s.PutJournalRow(t.Context(), first, f.fence); err != nil || retried {
		t.Fatalf("first write: retried = %v, err = %v, want false, nil", retried, err)
	}

	same := journalRowAttempt(t, f.s, u, journalWrite{sha: 1, attempt: "attempt0002", seqs: []int64{1, 2}})
	if retried, err := f.s.PutJournalRow(t.Context(), same, f.fence); err != nil || !retried {
		t.Fatalf("replay under another key: retried = %v, err = %v, want true, nil", retried, err)
	}

	other := journalRowAttempt(t, f.s, u, journalWrite{sha: 2, attempt: "attempt0003", seqs: []int64{1, 2}})

	retried, err := f.s.PutJournalRow(t.Context(), other, f.fence)
	if !errors.Is(err, stack.ErrJournalConflict) || retried {
		t.Fatalf("other content under another key: retried = %v, err = %v, want false, conflict", retried, err)
	}

	keys, err := f.s.JournalKeys(t.Context(), stackID, u.ID)
	if err != nil || len(keys) != 1 || keys[0] != first.BlobKey {
		t.Fatalf("journal keys = %v, %v; want only the key of the first write", keys, err)
	}
}

// testJournalBatches shows that the store gives the key, the SHA-256 and the size
// of each batch in arrival order, and nothing for a missing update.
func testJournalBatches(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")
	stackID := mustStackID(t, f.s, f.ref)

	rows := []stack.JournalRow{
		journalRowAttempt(t, f.s, u, journalWrite{sha: 1, attempt: "attempt0001", seqs: []int64{3, 4}}),
		journalRowAttempt(t, f.s, u, journalWrite{sha: 2, attempt: "attempt0002", seqs: []int64{1, 2}}),
	}
	rows[1].Size = 7

	for i := range rows {
		if _, err := f.s.PutJournalRow(t.Context(), rows[i], f.fence); err != nil {
			t.Fatalf("put batch %d: %v", i, err)
		}
	}

	got, err := f.s.JournalBatches(t.Context(), stackID, u.ID)
	if err != nil || len(got) != len(rows) {
		t.Fatalf("journal batches = %v, %v; want %d", got, err, len(rows))
	}

	for i := range rows {
		want := stack.JournalBatchRef{Key: rows[i].BlobKey, SHA256: rows[i].SHA256, Size: rows[i].Size}
		if got[i] != want {
			t.Fatalf("batch %d = %+v, want %+v", i, got[i], want)
		}
	}

	none, err := f.s.JournalBatches(t.Context(), stackID, "no-such-update")
	if err != nil || len(none) != 0 {
		t.Fatalf("journal batches of a missing update = %v, %v; want empty", none, err)
	}
}

// testJournalConcurrentWinner sends several writes of one batch at once, each
// under the key of its own attempt. With the same content, exactly one write
// stores the batch and each other write reports a retry. With different
// content, exactly one write stores it and each other write fails with
// ErrJournalConflict. The rows keep the key of the winner in both cases.
func testJournalConcurrentWinner(t *testing.T, f fixture) {
	t.Helper()

	for round := range checkpointRounds {
		for _, same := range []bool{false, true} {
			u := f.runJournaled(t, f.ref, "u"+strconv.Itoa(round)+strconv.FormatBool(same))
			rows := make([]stack.JournalRow, checkpointWriters)

			for i := range rows {
				sha := byte(1)
				if !same {
					sha = byte(i + 1)
				}

				write := journalWrite{sha: sha, attempt: "attempt" + strconv.Itoa(1000+i), seqs: []int64{1, 2}}
				rows[i] = journalRowAttempt(t, f.s, u, write)
			}

			retried, errs := putAtOnce(t, f, rows)
			winner := wantOneJournalWinner(t, retried, errs, same)

			keys, err := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
			if err != nil || len(keys) != 1 || keys[0] != rows[winner].BlobKey {
				t.Fatalf("round %d: journal keys = %v, %v; want only the key of the winner", round, keys, err)
			}

			f.end(t, u, stack.StatusSucceeded)
		}
	}
}

// wantOneJournalWinner fails the test unless exactly one write stored the batch
// and each other write has the answer of its content. It returns the index of
// the winner.
func wantOneJournalWinner(t *testing.T, retried []bool, errs []error, same bool) int {
	t.Helper()

	winner, won := -1, 0

	for i := range errs {
		switch {
		case errs[i] == nil && !retried[i]:
			winner = i
			won++
		case same && errs[i] == nil && retried[i]:
		case !same && errors.Is(errs[i], stack.ErrJournalConflict) && !retried[i]:
		default:
			t.Fatalf("write %d: retried = %v, err = %v, same content = %v", i, retried[i], errs[i], same)
		}
	}

	if won != 1 {
		t.Fatalf("%d writes stored the batch, want exactly one (same content = %v)", won, same)
	}

	return winner
}
