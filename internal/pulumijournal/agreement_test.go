package pulumijournal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// agreementResource is a minimal resource state that the replay can decode.
const agreementResource = `{"urn":"urn:a","custom":true,"type":"t:mod:T","id":"x"}`

// agreementBase is an empty base deployment for the replay.
const agreementBase = `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}`

// agreementPrior returns one prior entry with the given kind, sequence and
// operation. state is the resource state, or empty for no state.
func agreementPrior(kind, seq, op int64, state string) string {
	member := ""
	if state != "" {
		member = `,"state":` + state
	}

	return `{"version":1,"kind":` + strconv.FormatInt(kind, 10) + `,"sequenceID":` +
		strconv.FormatInt(seq, 10) + `,"operationID":` + strconv.FormatInt(op, 10) +
		`,"removeOld":null,"removeNew":null` + member + `}`
}

// agreementReferrer returns a success entry with a state that names ref through
// field, which is one of removeNew, deleteNew or pendingReplacementNew.
func agreementReferrer(seq, op int64, field string, ref int64) string {
	if field == "removeNew" {
		return `{"version":1,"kind":1,"sequenceID":` + strconv.FormatInt(seq, 10) +
			`,"operationID":` + strconv.FormatInt(op, 10) + `,"removeOld":null,"removeNew":` +
			strconv.FormatInt(ref, 10) + `,"state":` + agreementResource + `}`
	}

	base := `{"version":1,"kind":1,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) + `,"removeOld":null,"removeNew":null` +
		`,"state":` + agreementResource

	switch field {
	case "deleteNew":
		return base + `,"deleteNew":` + strconv.FormatInt(ref, 10) + `}`
	case "pendingReplacementNew":
		return base + `,"pendingReplacementNew":` + strconv.FormatInt(ref, 10) + `}`
	default:
		return base + `}`
	}
}

// agreementReplay runs the replay over prior plus referrer and reports its error.
func agreementReplay(t *testing.T, prior, referrer string) error {
	t.Helper()

	entries := `{"entries":[`
	if prior != "" {
		entries += prior + `,`
	}

	entries += referrer + `]}`

	now := func() time.Time {
		return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	}
	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(agreementBase)), nil
	}

	var out bytes.Buffer

	return pulumijournal.NewReplayer(now, cliVersion).Replay(
		t.Context(),
		open,
		[][]byte{[]byte(entries)},
		&out,
	)
}

// agreementMigrationState is an inserted resource state with a valid URN for
// the migration agreement tests.
const agreementMigrationState = `{"urn":"urn:pulumi:dev::corpus::test:resource::m","custom":true,"type":"test:resource","id":"m"}`

// agreementPatchedState is the replacement state of the new-state patch in
// the migration agreement tests. Its URN differs from the inserted state, so
// the migrated snapshot holds no duplicate.
const agreementPatchedState = `{"urn":"urn:pulumi:dev::corpus::test:resource::p","custom":true,"type":"test:resource","id":"p"}`

// agreementMigration returns a state migration entry with one inserted state
// and no patches.
func agreementMigration(seq int64) string {
	return agreementMigrationEntry(seq, "")
}

// agreementMigrationWithPatch returns a state migration entry with one
// inserted state and a new-state patch for operation 10.
func agreementMigrationWithPatch(seq int64) string {
	return agreementMigrationEntry(
		seq,
		`,"newStatePatches":[{"operationID":10,"state":`+agreementPatchedState+`}]`,
	)
}

// agreementMigrationEntry returns a state migration entry with one inserted
// state and the given patch member, which is empty for no patches.
func agreementMigrationEntry(seq int64, patches string) string {
	return `{"version":2,"kind":10,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":0,"removeOld":null,"removeNew":null,` +
		`"layout":[{"stateIndex":0}],"states":[` + agreementMigrationState + `]` + patches + `}`
}

// agreementVerify runs the verifier over the body with known and reports its error.
func agreementVerify(body string, known []int64) error {
	return agreementVerifyVersion(body, 1, known)
}

// agreementVerifyVersion runs the verifier over the body with known and the
// negotiated version, and reports its error.
func agreementVerifyVersion(body string, version int, known []int64) error {
	_, err := (pulumijournal.Verifier{}).VerifyBatch([]byte(body), version, known, false)

	return err
}

// TestVerifierAndReplayerAgreeOnNewStatePatches checks that a new-state patch
// for an operation that no applied entry created is refused by both with
// ErrMissingReference, and that a patch for a completed operation passes both.
func TestVerifierAndReplayerAgreeOnNewStatePatches(t *testing.T) {
	t.Parallel()

	migration := agreementMigrationWithPatch(1)
	if err := agreementVerifyVersion(`{"entries":[`+migration+`]}`, 2, nil); !errors.Is(
		err,
		pulumijournal.ErrMissingReference,
	) {
		t.Fatalf("verifier patch of an unknown operation: err = %v, want ErrMissingReference", err)
	}

	if err := agreementReplay(t, "", migration); !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("replayer patch of an unknown operation: err = %v, want ErrMissingReference", err)
	}

	prior := agreementPrior(1, 1, 10, agreementResource) + `,` + agreementMigrationWithPatch(2)
	next := agreementReferrer(3, 11, "removeNew", 10)
	if err := agreementVerifyVersion(`{"entries":[`+prior+`,`+next+`]}`, 2, nil); err != nil {
		t.Fatalf("verifier patch of a completed operation: %v", err)
	}

	if err := agreementReplay(t, prior, next); err != nil {
		t.Fatalf("replayer patch of a completed operation: %v", err)
	}
}

// TestVerifierAndReplayerAgreeAfterMigration checks that a reference to an
// operation completed before a state migration passes both: the replay keeps
// its operation index across the migration, and the verifier keeps the held
// operation.
func TestVerifierAndReplayerAgreeAfterMigration(t *testing.T) {
	t.Parallel()

	prior := agreementPrior(1, 1, 5, agreementResource) + `,` + agreementMigration(2)
	referrer := agreementReferrer(3, 7, "removeNew", 5)

	if err := agreementVerifyVersion(`{"entries":[`+referrer+`]}`, 2, []int64{5}); err != nil {
		t.Fatalf("verifier reference after a migration: %v", err)
	}

	if err := agreementReplay(t, prior, referrer); err != nil {
		t.Fatalf("replayer reference after a migration: %v", err)
	}
}

// agreementCase is one reference case: a prior entry and a success referrer
// that names operation 10. An intra case runs both in one batch, otherwise the
// prior is stored and only the referrer is verified.
type agreementCase struct {
	name      string
	priorKind int64
	prior     string
	hasPrior  bool
	field     string
	intra     bool
}

// TestVerifierAndReplayerAgreeOnReferences runs every reference case through
// both the verifier and the replayer and checks that they agree: both accept
// or both refuse with ErrMissingReference.
func TestVerifierAndReplayerAgreeOnReferences(t *testing.T) {
	t.Parallel()

	tests := []agreementCase{
		{
			name:      "completed remove",
			priorKind: 1,
			prior:     agreementResource,
			hasPrior:  true,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "completed delete",
			priorKind: 1,
			prior:     agreementResource,
			hasPrior:  true,
			field:     "deleteNew",
			intra:     false,
		},
		{
			name:      "completed pending",
			priorKind: 1,
			prior:     agreementResource,
			hasPrior:  true,
			field:     "pendingReplacementNew",
			intra:     false,
		},
		{
			name:      "begun remove",
			priorKind: 0,
			prior:     "",
			hasPrior:  true,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "begun delete",
			priorKind: 0,
			prior:     "",
			hasPrior:  true,
			field:     "deleteNew",
			intra:     false,
		},
		{
			name:      "begun pending",
			priorKind: 0,
			prior:     "",
			hasPrior:  true,
			field:     "pendingReplacementNew",
			intra:     false,
		},
		{
			name:      "no state",
			priorKind: 1,
			prior:     "",
			hasPrior:  true,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "failure",
			priorKind: 2,
			prior:     "",
			hasPrior:  true,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "refresh with state",
			priorKind: 3,
			prior:     agreementResource,
			hasPrior:  true,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "outputs with state",
			priorKind: 4,
			prior:     agreementResource,
			hasPrior:  true,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "missing remove",
			priorKind: 0,
			prior:     "",
			hasPrior:  false,
			field:     "removeNew",
			intra:     false,
		},
		{
			name:      "missing delete",
			priorKind: 0,
			prior:     "",
			hasPrior:  false,
			field:     "deleteNew",
			intra:     false,
		},
		{
			name:      "intra completed",
			priorKind: 1,
			prior:     agreementResource,
			hasPrior:  true,
			field:     "removeNew",
			intra:     true,
		},
		{
			name:      "intra begun",
			priorKind: 0,
			prior:     "",
			hasPrior:  true,
			field:     "removeNew",
			intra:     true,
		},
		{
			name:      "intra no state",
			priorKind: 1,
			prior:     "",
			hasPrior:  true,
			field:     "removeNew",
			intra:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkAgreement(t, tt)
		})
	}
}

// checkAgreement runs one reference case through both the verifier and the
// replayer and checks that they agree.
func checkAgreement(t *testing.T, tc agreementCase) {
	t.Helper()

	var prior string

	var known []int64

	if tc.hasPrior {
		prior = agreementPrior(tc.priorKind, 1, 10, tc.prior)
		if tc.priorKind == 1 && tc.prior != "" {
			known = []int64{10}
		}
	}

	referrer := agreementReferrer(2, 11, tc.field, 10)

	verifierBody := `{"entries":[` + referrer + `]}`
	if tc.intra {
		verifierBody = `{"entries":[` + prior + `,` + referrer + `]}`
	}

	verr := agreementVerify(verifierBody, known)
	rerr := agreementReplay(t, prior, referrer)

	if (verr == nil) != (rerr == nil) {
		t.Fatalf("verifier err = %v, replayer err = %v; want agreement", verr, rerr)
	}

	if verr != nil {
		if !errors.Is(verr, pulumijournal.ErrMissingReference) {
			t.Fatalf("verifier err = %v, want ErrMissingReference", verr)
		}

		if !errors.Is(rerr, pulumijournal.ErrMissingReference) {
			t.Fatalf("replayer err = %v, want ErrMissingReference", rerr)
		}
	}
}

// TestVerifierAndReplayerAgreeOnSelfReference checks that a success entry with
// a state that names its own operation passes both the verifier and the
// replayer, as the replay adds its index before it applies its flags.
func TestVerifierAndReplayerAgreeOnSelfReference(t *testing.T) {
	t.Parallel()

	referrer := agreementReferrer(1, 11, "removeNew", 11)
	verifierBody := `{"entries":[` + referrer + `]}`

	if err := agreementVerify(verifierBody, nil); err != nil {
		t.Fatalf("verifier self reference: %v", err)
	}

	if err := agreementReplay(t, "", referrer); err != nil {
		t.Fatalf("replayer self reference: %v", err)
	}
}

// TestVerifierAndReplayerAgreeAfterRebuild checks that a reference to an
// operation from before a rebuild is refused by both: the replay drops its
// records at the rebuild, and the verifier refuses because the known set
// starts again from the rebuild batch. A reference to an operation completed
// after the rebuild passes both.
func TestVerifierAndReplayerAgreeAfterRebuild(t *testing.T) {
	t.Parallel()

	success := agreementPrior(1, 1, 5, agreementResource)
	rebuild := `{"version":1,"kind":7,"sequenceID":2,"operationID":6,"removeOld":null,"removeNew":null}`
	prior := success + `,` + rebuild
	referrer := agreementReferrer(3, 7, "removeNew", 5)
	verifierBody := `{"entries":[` + referrer + `]}`

	// The stored rebuild clears the known set, so the batch after it is
	// verified with no known operations.
	if err := agreementVerify(verifierBody, nil); !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("verifier reference before a rebuild: err = %v, want ErrMissingReference", err)
	}

	if err := agreementReplay(t, prior, referrer); !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("replayer reference before a rebuild: err = %v, want ErrMissingReference", err)
	}

	completed := agreementPrior(1, 3, 8, agreementResource)
	after := prior + `,` + completed
	next := agreementReferrer(4, 9, "removeNew", 8)
	nextBody := `{"entries":[` + next + `]}`

	if err := agreementVerify(nextBody, []int64{8}); err != nil {
		t.Fatalf("verifier reference after a rebuild: %v", err)
	}

	if err := agreementReplay(t, after, next); err != nil {
		t.Fatalf("replayer reference after a rebuild: %v", err)
	}
}
