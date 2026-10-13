package stack_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// fakeBlobs is an in-memory blob.Store that records each call in order.
type fakeBlobs struct {
	objects    map[blob.Key][]byte
	failDelete error
	listed     []blob.Object
	calls      []string
	mu         sync.Mutex
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{objects: make(map[blob.Key][]byte), calls: nil, mu: sync.Mutex{}, failDelete: nil, listed: nil}
}

func (f *fakeBlobs) Put(_ context.Context, key blob.Key, r io.Reader) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "put")

	if _, ok := f.objects[key]; ok {
		return blob.ErrExists
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read test blob: %w", err)
	}

	f.objects[key] = data

	return nil
}

func (f *fakeBlobs) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeBlobs) Delete(_ context.Context, key blob.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "delete")
	if f.failDelete != nil {
		return f.failDelete
	}

	delete(f.objects, key)

	return nil
}

func (f *fakeBlobs) List(context.Context, string) ([]blob.Object, error) { return f.listed, nil }

func (f *fakeBlobs) Ping(context.Context) error { return nil }

// fakeRows is a stack.JournalStore that answers the configured error and records
// each row it is given.
type fakeRows struct {
	err     error
	rows    []stack.JournalRow
	keys    []blob.Key
	retried bool
	running bool
	calls   []string
}

func (f *fakeRows) PutJournalRow(_ context.Context, row stack.JournalRow, _ namespace.Fence) (bool, error) {
	f.calls = append(f.calls, "row")
	f.rows = append(f.rows, row)

	if row.Verify != nil {
		if err := row.Verify(nil); err != nil {
			return false, fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
		}
	}

	return f.retried, f.err
}

// newEntries returns a writer for a test, and fails the test when it is refused.
func newEntries(t *testing.T, blobs blob.Store, rows stack.JournalStore, v stack.BatchVerifier) *stack.JournalEntries {
	t.Helper()

	entries, err := stack.NewJournalEntries(blobs, rows, v)
	if err != nil {
		t.Fatalf("new journal writer: %v", err)
	}

	return entries
}

// JournalKeys implements stack.JournalStore. The fake keeps its keys in the fake rows.
func (f *fakeRows) JournalKeys(context.Context, stack.ID, stack.UpdateID) ([]blob.Key, error) {
	return f.keys, nil
}

// JournalSweepable implements stack.JournalStore. The fake is sweepable unless sweepable
// is false, as the store reports for a running update with an open journal.
func (f *fakeRows) JournalSweepable(context.Context, stack.ID, stack.UpdateID) (bool, error) {
	return !f.running, nil
}

// allowAll is a verifier that accepts every batch.
type allowAll struct{}

func (allowAll) VerifyBatch([]byte, int, []int64) error { return nil }

// recordVerifier records the arguments of each call and answers err.
type recordVerifier struct {
	body    []byte
	known   []int64
	version int
	err     error
}

func (v *recordVerifier) VerifyBatch(body []byte, version int, known []int64) error {
	v.body = body
	v.version = version
	v.known = known

	return v.err
}

// batchBody returns a journal batch body with the given sequence IDs.
func batchBody(seqs ...int64) []byte {
	var b strings.Builder
	b.WriteString(`{"entries":[`)

	for i, seq := range seqs {
		if i > 0 {
			b.WriteString(",")
		}

		b.WriteString(`{"sequenceID":`)
		b.WriteString(strconv.FormatInt(seq, 10))
		b.WriteString(`,"operationID":7}`)
	}

	b.WriteString(`]}`)

	return []byte(b.String())
}

// journalBatchFor returns a batch of one update with the given body.
func journalBatchFor(body []byte) stack.JournalBatch {
	return stack.JournalBatch{
		Ref:     stack.Ref{Namespace: "acme", Project: "web", Stack: "dev"},
		StackID: "stack-1",
		Update:  "upd-1",
		Lease:   token.Digest{},
		Content: body,
		Version: 0,
	}
}

// fence returns a fence for the journal tests.
func fence() namespace.Fence {
	return namespace.Fence{Namespace: "acme", FailoverVersion: 1}
}

// TestJournalAppendWritesBlobBeforeRow checks the order of the write: the blob
// first, then the row, and the blob key holds the lowest sequence ID.
func TestJournalAppendWritesBlobBeforeRow(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	rows := &fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})

	if err := entries.Append(t.Context(), journalBatchFor(batchBody(5, 3, 4)), fence()); err != nil {
		t.Fatalf("append: %v", err)
	}

	if strings.Join(blobs.calls, ",") != "put" || strings.Join(rows.calls, ",") != "row" {
		t.Fatalf("calls: blobs %v rows %v; want one put then one row", blobs.calls, rows.calls)
	}

	row := rows.rows[0]
	if want := blob.Key("pulumi/stack-1/upd-1/journal/3"); row.BlobKey != want {
		t.Fatalf("blob key = %q, want %q", row.BlobKey, want)
	}

	if row.Size != int64(len(batchBody(5, 3, 4))) {
		t.Fatalf("size = %d, want the body length", row.Size)
	}

	if want := sha256.Sum256(batchBody(5, 3, 4)); row.SHA256 != want {
		t.Fatal("sha256 differs from the body")
	}

	if len(row.SequenceIDs) != 3 || row.SequenceIDs[0] != 5 {
		t.Fatalf("sequence IDs = %v, want the batch order", row.SequenceIDs)
	}
}

// TestJournalAppendDeletesBlobOnRefusal checks that a clear refusal removes the blob
// that this call wrote, so no blob is left without a row.
func TestJournalAppendDeletesBlobOnRefusal(t *testing.T) {
	t.Parallel()

	for _, refusal := range []error{
		stack.ErrWrongUpdateState,
		stack.ErrJournalConflict,
		namespace.ErrFenced,
	} {
		blobs := newFakeBlobs()
		rows := &fakeRows{err: refusal, rows: nil, keys: nil, running: false, retried: false, calls: nil}
		entries := newEntries(t, blobs, rows, allowAll{})

		err := entries.Append(t.Context(), journalBatchFor(batchBody(1)), fence())
		if !errors.Is(err, refusal) {
			t.Fatalf("refusal %v: err = %v, want the refusal", refusal, err)
		}

		if strings.Join(blobs.calls, ",") != "put,delete" {
			t.Fatalf("refusal %v: blob calls = %v, want put then delete", refusal, blobs.calls)
		}
	}
}

// TestJournalAppendKeepsBlobOnOtherFailure checks that a failure that is not a
// refusal keeps the blob, for the sweep to find later.
func TestJournalAppendKeepsBlobOnOtherFailure(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	rows := &fakeRows{err: io.ErrUnexpectedEOF, rows: nil, keys: nil, running: false, retried: false, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})

	if err := entries.Append(t.Context(), journalBatchFor(batchBody(1)), fence()); err == nil {
		t.Fatal("append: err = nil, want the store error")
	}

	if strings.Join(blobs.calls, ",") != "put" {
		t.Fatalf("blob calls = %v, want only the put", blobs.calls)
	}
}

// TestJournalAppendRetryWritesNoBlob checks that a retry whose blob is already there
// with the same content makes no new blob, and that the store decides the retry.
func TestJournalAppendRetryWritesNoBlob(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	rows := &fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: true, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})
	body := batchBody(1, 2)

	if err := entries.Append(t.Context(), journalBatchFor(body), fence()); err != nil {
		t.Fatalf("first append: %v", err)
	}

	if err := entries.Append(t.Context(), journalBatchFor(body), fence()); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if strings.Join(blobs.calls, ",") != "put,put" {
		t.Fatalf("blob calls = %v, want a put for each call", blobs.calls)
	}
}

// TestJournalAppendRefusesOtherBlobContent checks that a blob under the key that holds
// other bytes stops the write before the row, and that nothing is deleted.
func TestJournalAppendRefusesOtherBlobContent(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	blobs.objects[blob.Key("pulumi/stack-1/upd-1/journal/1")] = []byte("other bytes")
	rows := &fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})

	err := entries.Append(t.Context(), journalBatchFor(batchBody(1)), fence())
	if !errors.Is(err, stack.ErrJournalConflict) {
		t.Fatalf("err = %v, want ErrJournalConflict", err)
	}

	if len(rows.calls) != 0 {
		t.Fatal("the row was written for a blob with other content")
	}
}

// TestJournalAppendRefusesMalformedBatch checks the batch shape: entries, positive
// sequence IDs, and no sequence ID twice.
func TestJournalAppendRefusesMalformedBatch(t *testing.T) {
	t.Parallel()

	for name, body := range map[string][]byte{
		"not json":     []byte("{"),
		"zero seq":     []byte(`{"entries":[{"sequenceID":0,"operationID":1}]}`),
		"repeated seq": []byte(`{"entries":[{"sequenceID":2},{"sequenceID":2}]}`),
	} {
		blobs := newFakeBlobs()
		rows := &fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil}
		entries := newEntries(t, blobs, rows, allowAll{})

		if err := entries.Append(t.Context(), journalBatchFor(body), fence()); !errors.Is(err, stack.ErrJournalBatch) {
			t.Fatalf("%s: err = %v, want ErrJournalBatch", name, err)
		}

		if len(blobs.calls) != 0 || len(rows.calls) != 0 {
			t.Fatalf("%s: wrote to the stores before the check", name)
		}
	}
}

// TestJournalAppendReadsBackTheSameBytes checks that the digest of a blob that already
// holds the same bytes is the digest of the batch, so the write goes on to the row.
func TestJournalAppendReadsBackTheSameBytes(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	body := batchBody(9)
	blobs.objects[blob.Key("pulumi/stack-1/upd-1/journal/9")] = bytes.Clone(body)
	rows := &fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})

	if err := entries.Append(t.Context(), journalBatchFor(body), fence()); err != nil {
		t.Fatalf("append over the same bytes: %v", err)
	}

	if len(rows.calls) != 1 {
		t.Fatalf("rows written = %d, want 1", len(rows.calls))
	}
}

// TestJournalAppendAcceptsEmptyBatch checks that a batch with no entries is accepted
// and writes neither a blob nor a row, as the CLI sends it when it has no entries.
func TestJournalAppendAcceptsEmptyBatch(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{[]byte("{}"), []byte(`{"entries":[]}`)} {
		blobs := newFakeBlobs()
		rows := &fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil}
		entries := newEntries(t, blobs, rows, allowAll{})

		if err := entries.Append(t.Context(), journalBatchFor(body), fence()); err != nil {
			t.Fatalf("empty batch %s: %v", body, err)
		}

		if len(blobs.calls) != 0 || len(rows.calls) != 0 {
			t.Fatalf("empty batch %s wrote to the stores", body)
		}
	}
}

// TestJournalAppendPassesTheBatchToTheVerifier checks that the verifier sees the body,
// the negotiated version and the operation IDs the store passes.
func TestJournalAppendPassesTheBatchToTheVerifier(t *testing.T) {
	t.Parallel()

	verifier := &recordVerifier{err: nil, body: nil, known: nil, version: 0}
	entries := newEntries(t,
		newFakeBlobs(),
		&fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil},
		verifier,
	)
	body := batchBody(4)
	batch := journalBatchFor(body)
	batch.Version = 1

	if err := entries.Append(t.Context(), batch, fence()); err != nil {
		t.Fatalf("append: %v", err)
	}

	if !bytes.Equal(verifier.body, body) || verifier.version != 1 {
		t.Fatalf(
			"verifier saw version %d and body %s; want version 1 and the batch body",
			verifier.version,
			verifier.body,
		)
	}
}

// TestJournalAppendDeletesBlobOnVerifierRefusal checks that a batch the verifier refuses
// is refused with ErrJournalBatch, and that its blob is deleted.
func TestJournalAppendDeletesBlobOnVerifierRefusal(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	verifier := &recordVerifier{err: io.ErrUnexpectedEOF, body: nil, known: nil, version: 0}
	entries := newEntries(
		t,
		blobs,
		&fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil},
		verifier,
	)

	err := entries.Append(t.Context(), journalBatchFor(batchBody(1)), fence())
	if !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("err = %v, want ErrJournalBatch", err)
	}

	if strings.Join(blobs.calls, ",") != "put,delete" {
		t.Fatalf("blob calls = %v, want put then delete", blobs.calls)
	}
}

// TestKnownOperationIDs checks that the stored arrays are read in order, and that a
// value that is not an array is an error.
func TestKnownOperationIDs(t *testing.T) {
	t.Parallel()

	got, err := stack.KnownOperationIDs([]string{"[1,2]", "[3]", "[]"})
	if err != nil {
		t.Fatalf("known: %v", err)
	}

	if fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("known = %v, want [1 2 3]", got)
	}

	if _, err := stack.KnownOperationIDs([]string{"not json"}); err == nil {
		t.Fatal("a value that is not an array: err = nil, want an error")
	}
}

// TestNewJournalEntriesRefusesNilVerifier checks that a writer without a verifier is
// never built, so that a batch cannot be stored unchecked.
func TestNewJournalEntriesRefusesNilVerifier(t *testing.T) {
	t.Parallel()

	_, err := stack.NewJournalEntries(
		newFakeBlobs(),
		&fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil},
		nil,
	)
	if !errors.Is(err, stack.ErrNilVerifier) {
		t.Fatalf("nil verifier: err = %v, want ErrNilVerifier", err)
	}
}

// TestJournalSweepKeepsNamedAndYoungBlobs checks that the sweep deletes only the old
// journal blobs that no row names.
func TestJournalSweepKeepsNamedAndYoungBlobs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	named := blob.Key("pulumi/stack-1/upd-1/journal/1")
	orphan := blob.Key("pulumi/stack-1/upd-1/journal/2")
	young := blob.Key("pulumi/stack-1/upd-1/journal/3")

	blobs := newFakeBlobs()
	blobs.listed = []blob.Object{
		{Key: named, ModifiedAt: old},
		{Key: orphan, ModifiedAt: old},
		{Key: young, ModifiedAt: now.Add(-time.Minute)},
	}
	entries := newEntries(
		t,
		blobs,
		&fakeRows{err: nil, rows: nil, keys: []blob.Key{named}, running: false, retried: false, calls: nil},
		allowAll{},
	)

	err := entries.SweepUpdate(t.Context(), "stack-1", "upd-1", now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if strings.Join(blobs.calls, ",") != "delete" {
		t.Fatalf("blob calls = %v, want one delete", blobs.calls)
	}

	if _, ok := blobs.objects[orphan]; ok {
		t.Fatal("the old orphan blob survived the sweep")
	}
}

// TestJournalRemoveBlobsJoinsFailures checks that a failed delete is returned, joined,
// and that the other deletes still run.
func TestJournalRemoveBlobsJoinsFailures(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	blobs.failDelete = io.ErrUnexpectedEOF
	entries := newEntries(
		t,
		blobs,
		&fakeRows{err: nil, rows: nil, keys: nil, running: false, retried: false, calls: nil},
		allowAll{},
	)

	err := entries.RemoveBlobs(t.Context(), []blob.Key{"a", "b"})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("remove: err = %v, want the delete failure", err)
	}

	if strings.Join(blobs.calls, ",") != "delete,delete" {
		t.Fatalf("blob calls = %v, want both deletes", blobs.calls)
	}
}
