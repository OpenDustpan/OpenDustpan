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
		strings.NewReader(entries),
		&out,
	)
}

// agreementVerify runs the verifier over the body with known and reports its error.
func agreementVerify(body string, known []int64) error {
	return (pulumijournal.Verifier{}).VerifyBatch([]byte(body), 1, known)
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
