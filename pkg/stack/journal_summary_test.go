package stack_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// summaryReader is a batch reader that returns its summary for every body,
// with no error. It stands for a batch reader that breaks the rules of
// stack.BatchSummary.
type summaryReader struct {
	summary stack.BatchSummary
}

func (r summaryReader) ReadBatch([]byte, int) (stack.BatchSummary, error) {
	return r.summary, nil
}

// acceptEntries is an entry check that accepts every batch.
func acceptEntries([]int64, bool) (bool, error) { return false, nil }

// TestJournalAppendRefusesABadSummary checks that Append refuses a summary
// that the batch reader returned with no error but that breaks the rules of
// stack.BatchSummary. The refusal is ErrJournalBatch, it writes no blob and
// no row, and it is recorded with the sequence IDs of the summary.
func TestJournalAppendRefusesABadSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		summary stack.BatchSummary
	}{
		{
			name: "no entry check",
			summary: stack.BatchSummary{
				Entries: 2, SequenceIDs: []int64{1, 2}, Operations: []int64{}, HasRebuild: false, Verify: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			refusesSummary(t, tt.summary)
		})
	}
}

// refusesSummary checks that Append refuses a batch whose reader returns
// summary: ErrJournalBatch, no blob, no row, and one recorded refusal that
// names the sequence IDs of the summary with status 400.
func refusesSummary(t *testing.T, summary stack.BatchSummary) {
	t.Helper()

	blobs := newFakeBlobs()
	rows := newFakeRows()
	entries := newEntries(t, blobs, rows, summaryReader{summary: summary})

	err := entries.Append(t.Context(), journalBatchFor(batchBody(1, 2)), fence())
	if !errors.Is(err, stack.ErrJournalBatch) {
		t.Fatalf("append: err = %v, want ErrJournalBatch", err)
	}

	if len(blobs.calls) != 0 {
		t.Fatalf("blob calls = %v, want none", blobs.calls)
	}

	if strings.Join(rows.calls, ",") != "refusal" {
		t.Fatalf("row calls = %v, want one refusal and no row", rows.calls)
	}

	if got := rows.refusals[0].SequenceIDs; !slices.Equal(got, summary.SequenceIDs) {
		t.Fatalf("refusal sequence IDs = %v, want %v", got, summary.SequenceIDs)
	}

	if got := rows.refusals[0].Status; got != 400 {
		t.Fatalf("refusal status = %d, want 400", got)
	}
}

// TestJournalAppendAcceptsAGoodSummary checks that a summary that keeps the
// rules of stack.BatchSummary is stored, so the refusals above come from the
// rule that each case breaks.
func TestJournalAppendAcceptsAGoodSummary(t *testing.T) {
	t.Parallel()

	blobs := newFakeBlobs()
	rows := newFakeRows()
	summary := stack.BatchSummary{
		Entries: 2, SequenceIDs: []int64{1, 2}, Operations: []int64{7}, HasRebuild: false, Verify: acceptEntries,
	}
	entries := newEntries(t, blobs, rows, summaryReader{summary: summary})

	if err := entries.Append(t.Context(), journalBatchFor(batchBody(1, 2)), fence()); err != nil {
		t.Fatalf("append: %v", err)
	}

	if strings.Join(rows.calls, ",") != "row" {
		t.Fatalf("row calls = %v, want one row", rows.calls)
	}
}
