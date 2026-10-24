package pulumijournal_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// The compile-time check fails the build when the verifier stops satisfying the
// port of the update store.
var _ stack.BatchReader = pulumijournal.Verifier{}

// TestBoundErrorNamesItsBound checks that the update service reads the
// bound, the count and the limit of a replay bound refusal, so its answer
// names them.
func TestBoundErrorNamesItsBound(t *testing.T) {
	t.Parallel()

	// The assignment fails the build when a bound refusal stops satisfying the
	// ReplayBound of the update service, for example after a change of the
	// fields of stack.ReplayLimit.
	var bound stack.ReplayBound = &pulumijournal.BoundError{Bound: "entries", Count: 12, Limit: 10, Cause: nil}

	err := fmt.Errorf("replay: %w", bound)

	message, ok := stack.ReplayBoundMessage(err)
	if !ok {
		t.Fatal("ReplayBoundMessage reports no bound, want the bound of the refusal")
	}

	if !strings.Contains(message, "12 entries") || !strings.Contains(message, "limit of 10") {
		t.Fatalf("message = %q, want the count, the bound and the limit", message)
	}
}

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

// migrationState is an inserted resource state for the state migration tests.
const migrationState = `{"urn":"urn:pulumi:dev::corpus::test:resource::m","custom":true,"type":"test:resource","id":"m"}`

// migrationBatch returns a state migration entry with one new-state patch for
// operation op, or with no patches when op is negative.
func migrationBatch(seq, op int64) string {
	patches := ""
	if op >= 0 {
		patches = `,"newStatePatches":[{"operationID":` + strconv.FormatInt(op, 10) +
			`,"state":` + migrationState + `}]`
	}

	return `{"version":2,"kind":10,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":0,"removeOld":null,"removeNew":null,` +
		`"layout":[{"stateIndex":0}],"states":[` + migrationState + `]` + patches + `}`
}

// completedEntry returns a success entry with a state for operation op.
func completedEntry(seq, op int64) string {
	return `{"version":1,"kind":1,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) +
		`,"removeOld":null,"removeNew":null,"state":` + migrationState + `}`
}

// TestVerifierAcceptsStateMigration checks that a kind 10 entry passes with a
// new-state patch for a held operation, and reports that the batch changes
// the records.
func TestVerifierAcceptsStateMigration(t *testing.T) {
	t.Parallel()

	body := batchOf(completedEntry(1, 5), migrationBatch(2, 5))
	changed, err := (pulumijournal.Verifier{}).VerifyBatch(body, 2, nil, false)
	if err != nil {
		t.Fatalf("migration with a held patch: %v", err)
	}

	if !changed {
		t.Fatal("migration with a held patch: changed = false, want true")
	}
}

// TestVerifierRefusesBadNewStatePatches checks that a new-state patch for an
// operation that is not held, or that an earlier entry of the batch removed,
// is refused with ErrMissingReference.
func TestVerifierRefusesBadNewStatePatches(t *testing.T) {
	t.Parallel()

	unknown := batchOf(migrationBatch(1, 5))
	_, err := (pulumijournal.Verifier{}).VerifyBatch(unknown, 2, nil, false)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("patch of an unknown operation: err = %v, want ErrMissingReference", err)
	}

	removed := batchOf(
		completedEntry(1, 5),
		`{"version":1,"kind":1,"sequenceID":2,"operationID":6,"removeOld":null,"removeNew":5,`+
			`"state":`+migrationState+`}`,
		migrationBatch(3, 5),
	)
	_, err = (pulumijournal.Verifier{}).VerifyBatch(removed, 2, nil, false)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("patch of a removed operation: err = %v, want ErrMissingReference", err)
	}
}

// TestVerifierRefusesWriteAfterMigration checks that a write entry that
// follows a state migration entry is refused with ErrWriteOrder, as the
// migration changes the replay records.
func TestVerifierRefusesWriteAfterMigration(t *testing.T) {
	t.Parallel()

	write := `{"version":1,"kind":5,"sequenceID":2,"operationID":2,"removeOld":null,"removeNew":null,` +
		`"newSnapshot":{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}}`

	_, err := (pulumijournal.Verifier{}).VerifyBatch(batchOf(migrationBatch(1, -1), write), 2, nil, false)
	if !errors.Is(err, pulumijournal.ErrWriteOrder) {
		t.Fatalf("write after a migration: err = %v, want ErrWriteOrder", err)
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
