package pulumijournal_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// The compile-time check fails the build when the verifier stops satisfying the
// port of the update store.
var _ stack.BatchVerifier = pulumijournal.Verifier{}

// entryJSON returns one journal entry with the given kind, sequence ID and operation
// ID. The reference, when set, is the removeNew member.
func entryJSON(kind, seq, op int64, ref string) string {
	removeNew := "null"
	if ref != "" {
		removeNew = ref
	}

	return `{"version":1,"kind":` + strconv.FormatInt(kind, 10) + `,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) + `,"removeOld":null,"removeNew":` + removeNew + `}`
}

// batchOf wraps entries in a batch body.
func batchOf(entries ...string) []byte {
	return []byte(`{"entries":[` + strings.Join(entries, ",") + `]}`)
}

// TestVerifierAcceptsAReferenceToAHeldOperation checks that a reference to an operation
// that the update holds passes.
func TestVerifierAcceptsAReferenceToAHeldOperation(t *testing.T) {
	t.Parallel()

	body := batchOf(entryJSON(2, 1, 3, "7"))
	if _, err := (pulumijournal.Verifier{}).VerifyBatch(body, 1, []int64{7}, false); err != nil {
		t.Fatalf("held reference: %v", err)
	}
}

// TestVerifierRefusesAReferenceToNothing checks that a reference that no held or earlier
// operation names is refused with ErrMissingReference.
func TestVerifierRefusesAReferenceToNothing(t *testing.T) {
	t.Parallel()

	body := batchOf(entryJSON(2, 1, 3, "7"))
	_, err := (pulumijournal.Verifier{}).VerifyBatch(body, 1, nil, false)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("unknown reference: err = %v, want ErrMissingReference", err)
	}
}

// TestVerifierKnowsEarlierEntriesOfTheBatch checks that a reference to a completed
// earlier entry of the same batch passes, and that a reference to a later one
// is refused. Only a success entry with a state creates a referenceable
// operation.
func TestVerifierKnowsEarlierEntriesOfTheBatch(t *testing.T) {
	t.Parallel()

	state := `{"urn":"urn:a","custom":true,"type":"t:mod:T","id":"x"}`
	completed := `{"version":1,"kind":1,"sequenceID":1,"operationID":5,"removeOld":null,"removeNew":null,` +
		`"state":` + state + `}`
	referrer := `{"version":1,"kind":1,"sequenceID":2,"operationID":6,"removeOld":null,"removeNew":5,` +
		`"state":` + state + `}`

	earlier := batchOf(completed, referrer)
	if _, err := (pulumijournal.Verifier{}).VerifyBatch(earlier, 1, nil, false); err != nil {
		t.Fatalf("reference to an earlier entry: %v", err)
	}

	forward := `{"version":1,"kind":1,"sequenceID":1,"operationID":6,"removeOld":null,"removeNew":5,` +
		`"state":` + state + `}`
	late := `{"version":1,"kind":1,"sequenceID":2,"operationID":5,"removeOld":null,"removeNew":null,` +
		`"state":` + state + `}`
	later := batchOf(forward, late)
	_, err := (pulumijournal.Verifier{}).VerifyBatch(later, 1, nil, false)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("reference to a later entry: err = %v, want ErrMissingReference", err)
	}
}

// TestVerifierRefusesKindsTheReplayerDoesNotApply checks that kind 10 is refused
// with ErrUnsupportedKind, as the replayer refuses it (item 12).
func TestVerifierRefusesKindsTheReplayerDoesNotApply(t *testing.T) {
	t.Parallel()

	body := batchOf(`{"version":2,"kind":10,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`)
	_, err := (pulumijournal.Verifier{}).VerifyBatch(body, 2, nil, false)
	if !errors.Is(err, pulumijournal.ErrUnsupportedKind) {
		t.Fatalf("kind 10: err = %v, want ErrUnsupportedKind", err)
	}
}

// TestVerifierRefusesEntriesThatBreakDecision55 checks the strict rules of decision 5.5:
// an unknown kind, a sequence ID of 0, and a version above the negotiated one.
func TestVerifierRefusesEntriesThatBreakDecision55(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body []byte
		want error
	}{
		{"unknown kind", batchOf(entryJSON(42, 1, 1, "")), pulumijournal.ErrUnknownKind},
		{"sequence ID 0", batchOf(entryJSON(0, 0, 1, "")), pulumijournal.ErrSequenceID},
		{"version above negotiated", batchOf(`{"version":2,"kind":0,"sequenceID":1,"operationID":1,` +
			`"removeOld":null,"removeNew":null}`), pulumijournal.ErrVersion},
	}

	for _, tt := range tests {
		_, err := (pulumijournal.Verifier{}).VerifyBatch(tt.body, 1, nil, false)
		if !errors.Is(err, tt.want) {
			t.Fatalf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
}

// TestVerifierRefusesWriteAfterRecordChange checks that a write entry (kind 5)
// that follows an entry that changes the records is refused with ErrWriteOrder, so
// the route answers 400. A write before the record changes is accepted.
func TestVerifierRefusesWriteAfterRecordChange(t *testing.T) {
	t.Parallel()

	write := `{"version":1,"kind":5,"sequenceID":3,"operationID":3,"removeOld":null,"removeNew":null,` +
		`"newSnapshot":{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}}`
	begin := `{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`
	success := `{"version":1,"kind":1,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null}`

	_, err := (pulumijournal.Verifier{}).VerifyBatch(batchOf(begin, write), 1, nil, false)
	if !errors.Is(err, pulumijournal.ErrWriteOrder) {
		t.Fatalf("write after begin: err = %v, want ErrWriteOrder", err)
	}

	_, err = (pulumijournal.Verifier{}).VerifyBatch(batchOf(success, write), 1, []int64{1}, false)
	if !errors.Is(err, pulumijournal.ErrWriteOrder) {
		t.Fatalf("write after success: err = %v, want ErrWriteOrder", err)
	}

	changed, err := (pulumijournal.Verifier{}).VerifyBatch(batchOf(write, begin), 1, nil, false)
	if err != nil {
		t.Fatalf("write before begin: %v", err)
	}

	if !changed {
		t.Fatal("write before begin: changed = false, want true for the begin entry")
	}
}

// TestVerifierRefusesWriteWhenStoredBatchChangedRecords checks that a write entry
// (kind 5) is refused with ErrWriteOrder when a stored batch of the update holds
// an entry that changes the records, even when the batch itself holds no such
// entry. A batch without a write entry passes, and reports whether it holds a
// record change.
func TestVerifierRefusesWriteWhenStoredBatchChangedRecords(t *testing.T) {
	t.Parallel()

	write := `{"version":1,"kind":5,"sequenceID":3,"operationID":3,"removeOld":null,"removeNew":null,` +
		`"newSnapshot":{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}}`
	begin := `{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`
	secrets := `{"version":1,"kind":6,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,` +
		`"secretsProvider":{"type":"passphrase","state":"{}"}}`

	_, err := (pulumijournal.Verifier{}).VerifyBatch(batchOf(write), 1, nil, true)
	if !errors.Is(err, pulumijournal.ErrWriteOrder) {
		t.Fatalf("write after a stored record change: err = %v, want ErrWriteOrder", err)
	}

	changed, err := (pulumijournal.Verifier{}).VerifyBatch(batchOf(write), 1, nil, false)
	if err != nil {
		t.Fatalf("write with no record change: %v", err)
	}

	if changed {
		t.Fatal("write with no record change: changed = true, want false")
	}

	changed, err = (pulumijournal.Verifier{}).VerifyBatch(batchOf(begin), 1, nil, true)
	if err != nil {
		t.Fatalf("record change after a stored record change: %v", err)
	}

	if !changed {
		t.Fatal("record change after a stored record change: changed = false, want true")
	}

	changed, err = (pulumijournal.Verifier{}).VerifyBatch(batchOf(secrets), 1, nil, false)
	if err != nil {
		t.Fatalf("secrets entry: %v", err)
	}

	if changed {
		t.Fatal("secrets entry: changed = true, want false")
	}
}
