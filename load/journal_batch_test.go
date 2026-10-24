package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// journalBatchEntry is one entry of a journal batch, as the server reads it.
type journalBatchEntry struct {
	Version     int             `json:"version"`
	Kind        int             `json:"kind"`
	SequenceID  int             `json:"sequenceID"`
	OperationID int             `json:"operationID"`
	State       json.RawMessage `json:"state"`
}

// journalBatchBody is the body of one journal batch.
type journalBatchBody struct {
	Entries []journalBatchEntry `json:"entries"`
}

// journalBatchCounts answers the begin entries and the success entries of one
// operation.
type journalBatchCounts struct {
	begins    int
	successes int
}

// journalBatchTestBatches is the number of batches that one check sends. The
// second batch starts where the first one ends, so the check covers the
// sequence IDs and the operation IDs across batches.
const journalBatchTestBatches = 2

// kindBegin and kindSuccess are the entry kinds of a begin entry and a
// success entry in a journal batch.
const (
	kindBegin   = 0
	kindSuccess = 1
)

func TestJournalBatchShape(t *testing.T) {
	t.Parallel()

	for _, entries := range []int{2, 20, 200} {
		t.Run("entries="+strconv.Itoa(entries), func(t *testing.T) {
			t.Parallel()

			checkJournalBatchShape(t, entries)
		})
	}
}

// checkJournalBatchShape sends journalBatchTestBatches batches of the given
// size, and checks the rules that the server checks on each Append.
func checkJournalBatchShape(t *testing.T, entries int) {
	t.Helper()

	resourcesPerBatch := entries / entriesPerResource
	seqs := make([]int, 0, entries*journalBatchTestBatches)
	ops := make(map[int]journalBatchCounts)

	for b := range journalBatchTestBatches {
		body := journalBatch("journal-0", b*resourcesPerBatch, entries)
		seqs = append(seqs, checkJournalBatch(t, b, entries, body, ops)...)
	}

	for i, seq := range seqs {
		if seq != i+1 {
			t.Fatalf("sequence ID %d at position %d, want %d: IDs must be positive, unique and consecutive",
				seq, i, i+1)
		}
	}
}

// checkJournalBatch checks one batch of the given size. It records each
// operation in ops, so that an operation in two batches fails, and answers the
// sequence IDs of the batch.
func checkJournalBatch(
	t *testing.T, b, entries int, body []byte, ops map[int]journalBatchCounts,
) []int {
	t.Helper()

	var got journalBatchBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("batch %d is not valid JSON: %v", b, err)
	}

	if len(got.Entries) != entries {
		t.Fatalf("batch %d has %d entries, want %d", b, len(got.Entries), entries)
	}

	batchOps := countJournalOps(t, b, got.Entries)
	if len(batchOps) > entries {
		t.Fatalf("batch %d has %d operations for %d entries", b, len(batchOps), entries)
	}

	for op, counts := range batchOps {
		if counts != (journalBatchCounts{begins: 1, successes: 1}) {
			t.Fatalf("batch %d: operation %d has %+v, want one begin and one success", b, op, counts)
		}

		if _, seen := ops[op]; seen {
			t.Fatalf("operation %d is in more than one batch", op)
		}

		ops[op] = counts
	}

	seqs := make([]int, 0, len(got.Entries))
	for _, e := range got.Entries {
		seqs = append(seqs, e.SequenceID)
	}

	return seqs
}

// countJournalOps answers the begin and success entries of each operation in
// a batch. A success entry before its begin entry fails the check.
func countJournalOps(t *testing.T, b int, entries []journalBatchEntry) map[int]journalBatchCounts {
	t.Helper()

	ops := make(map[int]journalBatchCounts)

	for _, e := range entries {
		counts := ops[e.OperationID]

		switch e.Kind {
		case kindBegin:
			counts.begins++
		case kindSuccess:
			if counts.begins == 0 {
				t.Fatalf("batch %d: success of operation %d has no begin entry before it", b, e.OperationID)
			}

			counts.successes++
		default:
			t.Fatalf("batch %d: entry kind %d, want %d or %d", b, e.Kind, kindBegin, kindSuccess)
		}

		ops[e.OperationID] = counts
	}

	return ops
}

func TestJournalGzipRule(t *testing.T) {
	t.Parallel()

	for _, entries := range []int{2, 20, 200} {
		t.Run("entries="+strconv.Itoa(entries), func(t *testing.T) {
			t.Parallel()

			body := journalBatch("journal-0", 0, entries)

			if got, want := journalGzipped(body), len(body) >= journalGzipFrom; got != want {
				t.Fatalf("batch of %d bytes: gzipped = %t, want %t", len(body), got, want)
			}
		})
	}

	boundary := []struct {
		name string
		size int
		want bool
	}{
		{name: "one byte under", size: journalGzipFrom - 1, want: false},
		{name: "at the limit", size: journalGzipFrom, want: true},
		{name: "one byte over", size: journalGzipFrom + 1, want: true},
	}

	for _, tc := range boundary {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := []byte(`{"entries":[` + strings.Repeat(" ", tc.size-len(`{"entries":[]}`)) + `]}`)
			if len(body) != tc.size {
				t.Fatalf("test body is %d bytes, want %d", len(body), tc.size)
			}

			if got := journalGzipped(body); got != tc.want {
				t.Fatalf("batch of %d bytes: gzipped = %t, want %t", tc.size, got, tc.want)
			}
		})
	}
}
