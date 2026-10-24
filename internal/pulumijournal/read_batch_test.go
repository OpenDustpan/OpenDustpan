package pulumijournal_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// TestReadBatchSummary checks the summary of one decode: the entry count, the
// sequence IDs in the order of the body, and the completed operations.
func TestReadBatchSummary(t *testing.T) {
	t.Parallel()

	begin := `{"version":1,"kind":0,"sequenceID":4,"operationID":9,"removeOld":null,"removeNew":null}`

	summary, err := (pulumijournal.Verifier{}).ReadBatch(batchOf(completedEntry(5, 7), begin, completedEntry(3, 8)), 1)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}

	if summary.Entries != 3 || !slices.Equal(summary.SequenceIDs, []int64{5, 4, 3}) {
		t.Fatalf("entries %d, sequence IDs %v; want 3 and [5 4 3]", summary.Entries, summary.SequenceIDs)
	}

	if !slices.Equal(summary.Operations, []int64{7, 8}) || summary.HasRebuild {
		t.Fatalf("operations %v, rebuild %t; want [7 8] and false", summary.Operations, summary.HasRebuild)
	}

	if summary.Verify == nil {
		t.Fatal("Verify = nil, want the check of the stored state")
	}
}

// TestReadBatchDropsOperationsBeforeRebuild checks that a batch that holds a
// rebuilt base state entry keeps only the completed operations after the last
// such entry, and reports the rebuild, so the store starts the known set again
// from this batch.
func TestReadBatchDropsOperationsBeforeRebuild(t *testing.T) {
	t.Parallel()

	rebuild := `{"version":1,"kind":7,"sequenceID":2,"operationID":6,"removeOld":null,"removeNew":null}`

	summary, err := (pulumijournal.Verifier{}).ReadBatch(
		batchOf(completedEntry(1, 5), rebuild, completedEntry(3, 8)),
		1,
	)
	if err != nil {
		t.Fatalf("read batch: %v", err)
	}

	if !summary.HasRebuild || !slices.Equal(summary.Operations, []int64{8}) {
		t.Fatalf("rebuild %t, operations %v; want true and [8]", summary.HasRebuild, summary.Operations)
	}
}

// TestReadBatchEmpty checks that a batch with no entries reads as empty, with
// empty slices, because the CLI sends one when it has nothing to journal.
func TestReadBatchEmpty(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{}`, `{"entries":[]}`} {
		summary, err := (pulumijournal.Verifier{}).ReadBatch([]byte(body), 1)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}

		if summary.Entries != 0 || summary.SequenceIDs == nil || len(summary.SequenceIDs) != 0 {
			t.Fatalf("%s: entries %d, sequence IDs %v; want none", body, summary.Entries, summary.SequenceIDs)
		}
	}
}

// TestReadBatchRefusesRepeatedSequenceID checks that a sequence ID that the
// batch holds twice is refused with ErrSequenceID, and that the refusal still
// names every sequence ID of the body for its record.
func TestReadBatchRefusesRepeatedSequenceID(t *testing.T) {
	t.Parallel()

	summary, err := (pulumijournal.Verifier{}).ReadBatch(batchOf(completedEntry(2, 1), completedEntry(2, 3)), 1)
	if !errors.Is(err, pulumijournal.ErrSequenceID) {
		t.Fatalf("err = %v, want ErrSequenceID", err)
	}

	if !slices.Equal(summary.SequenceIDs, []int64{2, 2}) || summary.Verify != nil {
		t.Fatalf("sequence IDs %v, Verify set %t; want [2 2] and no Verify", summary.SequenceIDs, summary.Verify != nil)
	}
}

// TestReadBatchRefusesBodiesThatDoNotDecode checks that a body that is not a
// batch is refused, and that only the sequence IDs a plain read finds are
// named.
func TestReadBatchRefusesBodiesThatDoNotDecode(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		seqs []int64
	}{
		"not json":      {body: `{`, seqs: nil},
		"no version":    {body: `{"entries":[{"sequenceID":5,"operationID":7}]}`, seqs: []int64{5}},
		"zero sequence": {body: string(batchOf(completedEntry(0, 1))), seqs: []int64{0}},
	}

	for name, tc := range cases {
		summary, err := (pulumijournal.Verifier{}).ReadBatch([]byte(tc.body), 1)
		if err == nil {
			t.Fatalf("%s: err = nil, want a refusal", name)
		}

		if !slices.Equal(summary.SequenceIDs, tc.seqs) || summary.Verify != nil {
			t.Fatalf("%s: sequence IDs %v, Verify set %t; want %v and no Verify",
				name, summary.SequenceIDs, summary.Verify != nil, tc.seqs)
		}
	}
}

// TestReadBatchVerifyMatchesVerifyBatch checks that Verify, which runs on the
// entries of the one decode, gives the answer of VerifyBatch for the same body
// and stored state.
func TestReadBatchVerifyMatchesVerifyBatch(t *testing.T) {
	t.Parallel()

	write := `{"version":1,"kind":5,"sequenceID":3,"operationID":3,"removeOld":null,"removeNew":null,` +
		`"newSnapshot":{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}}`
	begin := `{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`

	cases := map[string]struct {
		body           []byte
		known          []int64
		recordsChanged bool
	}{
		"held reference":   {body: batchOf(entryJSON(2, 1, 3, "7")), known: []int64{7}, recordsChanged: false},
		"missing ref":      {body: batchOf(entryJSON(2, 1, 3, "7")), known: nil, recordsChanged: false},
		"write then begin": {body: batchOf(write, begin), known: nil, recordsChanged: false},
		"stored change":    {body: batchOf(write), known: nil, recordsChanged: true},
	}

	for name, tc := range cases {
		summary, err := (pulumijournal.Verifier{}).ReadBatch(tc.body, 1)
		if err != nil {
			t.Fatalf("%s: read batch: %v", name, err)
		}

		gotChanged, gotErr := summary.Verify(tc.known, tc.recordsChanged)
		wantChanged, wantErr := (pulumijournal.Verifier{}).VerifyBatch(tc.body, 1, tc.known, tc.recordsChanged)

		if gotChanged != wantChanged || (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("%s: Verify = %t, %v; VerifyBatch = %t, %v", name, gotChanged, gotErr, wantChanged, wantErr)
		}
	}
}
