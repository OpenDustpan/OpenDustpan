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

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// fakeBlobs is an in-memory blob.Store that records each call in order.
type fakeBlobs struct {
	objects map[blob.Key][]byte
	calls   []string
	mu      sync.Mutex
}

func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{objects: make(map[blob.Key][]byte), calls: nil, mu: sync.Mutex{}}
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
	delete(f.objects, key)

	return nil
}

func (f *fakeBlobs) List(context.Context, string) ([]blob.Object, error) { return nil, nil }

func (f *fakeBlobs) Ping(context.Context) error { return nil }

// fakeRows is a stack.JournalStore that answers the configured error and records
// each row it is given.
type fakeRows struct {
	err     error
	rows    []stack.JournalRow
	retried bool
	calls   []string
}

func (f *fakeRows) PutJournalRow(_ context.Context, row stack.JournalRow, _ namespace.Fence) (bool, error) {
	f.calls = append(f.calls, "row")
	f.rows = append(f.rows, row)

	return f.retried, f.err
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
	rows := &fakeRows{err: nil, rows: nil, retried: false, calls: nil}
	entries := stack.NewJournalEntries(blobs, rows)

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
		rows := &fakeRows{err: refusal, rows: nil, retried: false, calls: nil}
		entries := stack.NewJournalEntries(blobs, rows)

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
	rows := &fakeRows{err: io.ErrUnexpectedEOF, rows: nil, retried: false, calls: nil}
	entries := stack.NewJournalEntries(blobs, rows)

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
	rows := &fakeRows{err: nil, rows: nil, retried: true, calls: nil}
	entries := stack.NewJournalEntries(blobs, rows)
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
	rows := &fakeRows{err: nil, rows: nil, retried: false, calls: nil}
	entries := stack.NewJournalEntries(blobs, rows)

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
		rows := &fakeRows{err: nil, rows: nil, retried: false, calls: nil}
		entries := stack.NewJournalEntries(blobs, rows)

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
	rows := &fakeRows{err: nil, rows: nil, retried: false, calls: nil}
	entries := stack.NewJournalEntries(blobs, rows)

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
		rows := &fakeRows{err: nil, rows: nil, retried: false, calls: nil}
		entries := stack.NewJournalEntries(blobs, rows)

		if err := entries.Append(t.Context(), journalBatchFor(body), fence()); err != nil {
			t.Fatalf("empty batch %s: %v", body, err)
		}

		if len(blobs.calls) != 0 || len(rows.calls) != 0 {
			t.Fatalf("empty batch %s wrote to the stores", body)
		}
	}
}
