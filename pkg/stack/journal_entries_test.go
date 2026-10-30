package stack_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
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

func (f *fakeBlobs) list(context.Context, string) ([]blob.Object, error) { return f.listed, nil }

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (f *fakeBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := f.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (f *fakeBlobs) Ping(context.Context) error { return nil }

// fakeRows is a stack.JournalStore that answers the configured error and records
// each row it is given.
type fakeRows struct {
	err      error
	rows     []stack.JournalRow
	keys     []blob.Key
	refusals []stack.JournalRefusal
	retried  bool
	running  bool
	calls    []string
}

// newFakeRows returns an empty fake of the journal rows.
func newFakeRows() *fakeRows {
	return &fakeRows{
		err:      nil,
		rows:     nil,
		keys:     nil,
		refusals: nil,
		retried:  false,
		running:  false,
		calls:    nil,
	}
}

func (f *fakeRows) PutJournalRow(_ context.Context, row stack.JournalRow, _ namespace.Fence) (bool, error) {
	f.calls = append(f.calls, "row")
	f.rows = append(f.rows, row)

	if row.Verify != nil {
		if _, err := row.Verify(nil, false); err != nil {
			return false, fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
		}
	}

	return f.retried, f.err
}

// newEntries returns a writer for a test, and fails the test when it is refused.
func newEntries(t *testing.T, blobs blob.Store, rows stack.JournalStore, v stack.BatchReader) *stack.JournalEntries {
	t.Helper()

	entries, err := stack.NewJournalEntries(blobs, rows, v, 0, stacktest.NewRefusals())
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

// RecordJournalRefusal implements stack.JournalStore. The fake keeps each
// refusal it is given.
func (f *fakeRows) RecordJournalRefusal(
	_ context.Context, refusal stack.JournalRefusal, _ namespace.Fence,
) error {
	f.calls = append(f.calls, "refusal")
	f.refusals = append(f.refusals, refusal)

	return nil
}

// JournalRefusals implements stack.JournalStore.
func (f *fakeRows) JournalRefusals(
	_ context.Context, _ stack.ID, _ stack.UpdateID,
) ([]stack.JournalRefusal, error) {
	return f.refusals, nil
}

// JournalSequences implements stack.JournalStore. The fake derives the stored
// sequence IDs from the rows it kept, in order.
func (f *fakeRows) JournalSequences(
	_ context.Context, _ stack.ID, _ stack.UpdateID,
) ([]int64, error) {
	var out []int64

	for i := range f.rows {
		out = append(out, f.rows[i].SequenceIDs...)
	}

	slices.Sort(out)

	return out, nil
}

// fakeBatch is the part of a batch body that the fake batch readers read.
type fakeBatch struct {
	Entries []fakeEntry `json:"entries"`
}

// fakeEntry is the sequence ID of one entry of a fakeBatch.
type fakeEntry struct {
	SequenceID int64 `json:"sequenceID"`
}

// errFakeBatch is the refusal of the fake batch readers.
var errFakeBatch = errors.New("fake batch reader: the batch is malformed")

// readIDs is the decode of the fake batch readers: the sequence IDs of a JSON
// body, each positive and once, with no check of the entries. The real reader
// has its own tests.
func readIDs(body []byte) (stack.BatchSummary, error) {
	var batch fakeBatch

	summary := stack.BatchSummary{
		Entries:     0,
		SequenceIDs: []int64{},
		Operations:  []int64{},
		HasRebuild:  false,
		Verify:      nil,
	}

	if err := json.Unmarshal(body, &batch); err != nil {
		return summary, errFakeBatch
	}

	seen := make(map[int64]bool)
	refused := false

	for _, entry := range batch.Entries {
		refused = refused || entry.SequenceID <= 0 || seen[entry.SequenceID]
		seen[entry.SequenceID] = true
		summary.SequenceIDs = append(summary.SequenceIDs, entry.SequenceID)
	}

	summary.Entries = len(batch.Entries)

	if refused {
		return summary, errFakeBatch
	}

	return summary, nil
}

// allowAll is a batch reader that reads the sequence IDs and accepts every
// batch against the stored state.
type allowAll struct{}

func (allowAll) ReadBatch(body []byte, _ int) (stack.BatchSummary, error) {
	summary, err := readIDs(body)
	if err != nil {
		return summary, err
	}

	summary.Verify = func([]int64, bool) (bool, error) { return false, nil }

	return summary, nil
}

// recordReader records the arguments of each call, and its Verify answers
// changed and err.
type recordReader struct {
	body           []byte
	known          []int64
	version        int
	recordsChanged bool
	changed        bool
	err            error
}

func (v *recordReader) ReadBatch(body []byte, version int) (stack.BatchSummary, error) {
	v.body = body
	v.version = version

	summary, err := readIDs(body)
	if err != nil {
		return summary, err
	}

	summary.Verify = func(known []int64, recordsChanged bool) (bool, error) {
		v.known = known
		v.recordsChanged = recordsChanged

		return v.changed, v.err
	}

	return summary, nil
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

// journalNS is the namespace of the journal tests, and journalPrefix is the
// prefix of its stack blobs.
const (
	journalNS     = namespace.ID("ns-journal-test")
	journalPrefix = "namespaces/ns-journal-test/pulumi/"
)

// journalBatchFor returns a batch of one update with the given body.
func journalBatchFor(body []byte) stack.JournalBatch {
	return stack.JournalBatch{
		Ref:       stack.Ref{Namespace: "acme", Project: "web", Stack: "dev"},
		Namespace: journalNS,
		StackID:   "stack-1",
		Update:    "upd-1",
		Lease:     token.Digest{},
		Content:   body,
		Version:   0,
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
	rows := &fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})

	if err := entries.Append(t.Context(), journalBatchFor(batchBody(5, 3, 4)), fence()); err != nil {
		t.Fatalf("append: %v", err)
	}

	if strings.Join(blobs.calls, ",") != "put" || strings.Join(rows.calls, ",") != "row" {
		t.Fatalf("calls: blobs %v rows %v; want one put then one row", blobs.calls, rows.calls)
	}

	row := rows.rows[0]
	if want := blob.Key(journalPrefix + "stack-1/upd-1/journal/3"); row.BlobKey != want {
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
		rows := &fakeRows{err: refusal, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil}
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
	rows := &fakeRows{
		err: io.ErrUnexpectedEOF, rows: nil, keys: nil, refusals: nil,
		running: false, retried: false, calls: nil,
	}
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
	rows := &fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: true, calls: nil}
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
// other bytes stops the write before the row, records the refusal, and deletes nothing.
func TestJournalAppendRefusesOtherBlobContent(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	blobs.objects[blob.Key(journalPrefix+"stack-1/upd-1/journal/1")] = []byte("other bytes")
	rows := &fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil}
	entries := newEntries(t, blobs, rows, allowAll{})

	err := entries.Append(t.Context(), journalBatchFor(batchBody(1)), fence())
	if !errors.Is(err, stack.ErrJournalConflict) {
		t.Fatalf("err = %v, want ErrJournalConflict", err)
	}

	if len(rows.rows) != 0 {
		t.Fatal("the row was written for a blob with other content")
	}

	if len(rows.refusals) != 1 {
		t.Fatalf("refusals = %d, want the recorded refusal", len(rows.refusals))
	}

	refusal := rows.refusals[0]

	if len(refusal.SequenceIDs) != 1 || refusal.SequenceIDs[0] != 1 {
		t.Fatalf("refusal sequences = %v, want [1]", refusal.SequenceIDs)
	}

	if refusal.Status != 409 {
		t.Fatalf("refusal status = %d, want 409", refusal.Status)
	}
}

// TestJournalAppendRefusesMalformedBatch checks the batch shape: entries, positive
// sequence IDs, and no sequence ID twice, with the batch reader of the
// server. A malformed batch writes no blob and no row, but its refusal is
// recorded with the sequence IDs the server read.
func TestJournalAppendRefusesMalformedBatch(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body []byte
		seqs []int64
	}{
		"not json":     {body: []byte("{"), seqs: nil},
		"zero seq":     {body: []byte(`{"entries":[{"sequenceID":0,"operationID":1}]}`), seqs: []int64{0}},
		"repeated seq": {body: []byte(`{"entries":[{"sequenceID":2},{"sequenceID":2}]}`), seqs: []int64{2, 2}},
	}

	for name, tc := range cases {
		blobs := newFakeBlobs()
		rows := &fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil}
		entries := newEntries(t, blobs, rows, pulumijournal.Verifier{})

		if err := entries.Append(t.Context(), journalBatchFor(tc.body), fence()); !errors.Is(
			err, stack.ErrJournalBatch,
		) {
			t.Fatalf("%s: err = %v, want ErrJournalBatch", name, err)
		}

		if len(blobs.calls) != 0 || len(rows.rows) != 0 {
			t.Fatalf("%s: wrote a blob or a row before the check", name)
		}

		if len(rows.refusals) != 1 {
			t.Fatalf("%s: refusals = %d, want the recorded refusal", name, len(rows.refusals))
		}

		refusal := rows.refusals[0]

		if !slices.Equal(refusal.SequenceIDs, tc.seqs) {
			t.Fatalf("%s: refusal sequences = %v, want %v", name, refusal.SequenceIDs, tc.seqs)
		}

		if refusal.Status != 400 {
			t.Fatalf("%s: refusal status = %d, want 400", name, refusal.Status)
		}
	}
}

// TestJournalAppendReadsBackTheSameBytes checks that the digest of a blob that already
// holds the same bytes is the digest of the batch, so the write goes on to the row.
func TestJournalAppendReadsBackTheSameBytes(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	body := batchBody(9)
	blobs.objects[blob.Key(journalPrefix+"stack-1/upd-1/journal/9")] = bytes.Clone(body)
	rows := &fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil}
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
		rows := &fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil}
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

	verifier := &recordReader{err: nil, body: nil, known: nil, version: 0, recordsChanged: false, changed: false}
	entries := newEntries(t,
		newFakeBlobs(),
		&fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil},
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
	verifier := &recordReader{
		err: io.ErrUnexpectedEOF, body: nil, known: nil, version: 0, recordsChanged: false, changed: false,
	}
	entries := newEntries(
		t,
		blobs,
		&fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil},
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
		&fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil},
		nil,
		0,
		stacktest.NewRefusals(),
	)
	if !errors.Is(err, stack.ErrNilVerifier) {
		t.Fatalf("nil verifier: err = %v, want ErrNilVerifier", err)
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
		&fakeRows{err: nil, rows: nil, keys: nil, refusals: nil, running: false, retried: false, calls: nil},
		allowAll{},
	)

	err := entries.RemoveBlobs(
		t.Context(),
		journalNS,
		"stack-1",
		"upd-1",
		[]blob.Key{
			blob.Key(journalPrefix + "stack-1/upd-1/journal/1"),
			blob.Key(journalPrefix + "stack-1/upd-1/journal/2"),
		},
	)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("remove: err = %v, want the delete failure", err)
	}

	if strings.Join(blobs.calls, ",") != "delete,delete" {
		t.Fatalf("blob calls = %v, want both deletes", blobs.calls)
	}
}
