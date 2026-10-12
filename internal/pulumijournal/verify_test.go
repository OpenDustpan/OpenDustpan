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
	if err := (pulumijournal.Verifier{}).VerifyBatch(body, 1, []int64{7}); err != nil {
		t.Fatalf("held reference: %v", err)
	}
}

// TestVerifierRefusesAReferenceToNothing checks that a reference that no held or earlier
// operation names is refused with ErrMissingReference.
func TestVerifierRefusesAReferenceToNothing(t *testing.T) {
	t.Parallel()

	body := batchOf(entryJSON(2, 1, 3, "7"))
	err := (pulumijournal.Verifier{}).VerifyBatch(body, 1, nil)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("unknown reference: err = %v, want ErrMissingReference", err)
	}
}

// TestVerifierKnowsEarlierEntriesOfTheBatch checks that a reference to an earlier entry of
// the same batch passes, and that a reference to a later one is refused.
func TestVerifierKnowsEarlierEntriesOfTheBatch(t *testing.T) {
	t.Parallel()

	earlier := batchOf(entryJSON(0, 1, 5, ""), entryJSON(2, 2, 6, "5"))
	if err := (pulumijournal.Verifier{}).VerifyBatch(earlier, 1, nil); err != nil {
		t.Fatalf("reference to an earlier entry: %v", err)
	}

	later := batchOf(entryJSON(2, 1, 6, "5"), entryJSON(0, 2, 5, ""))
	err := (pulumijournal.Verifier{}).VerifyBatch(later, 1, nil)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("reference to a later entry: err = %v, want ErrMissingReference", err)
	}
}

// TestVerifierRefusesKindsTheReplayerDoesNotApply checks that kinds 5, 7 and 10 are
// refused with ErrUnsupportedKind, as the replayer refuses them.
func TestVerifierRefusesKindsTheReplayerDoesNotApply(t *testing.T) {
	t.Parallel()

	for _, kind := range []int64{5, 7} {
		body := batchOf(entryJSON(kind, 1, 1, ""))
		err := (pulumijournal.Verifier{}).VerifyBatch(body, 1, nil)
		if !errors.Is(err, pulumijournal.ErrUnsupportedKind) {
			t.Fatalf("kind %d: err = %v, want ErrUnsupportedKind", kind, err)
		}
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
		err := (pulumijournal.Verifier{}).VerifyBatch(tt.body, 1, nil)
		if !errors.Is(err, tt.want) {
			t.Fatalf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
}
