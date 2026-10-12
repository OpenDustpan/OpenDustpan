package stack_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// deltaOf builds a delta input for result with the given edits JSON. The
// hash is the SHA-256 of result, as the CLI computes it.
func deltaOf(seq int64, result, edits string) stack.DeltaInput {
	sum := sha256.Sum256([]byte(result))

	return stack.DeltaInput{
		SchemaVersion: 3,
		Sequence:      seq,
		Hash:          hex.EncodeToString(sum[:]),
		Delta:         []byte(edits),
	}
}

// editJSON renders one text edit in the shape the CLI sends: a gotextdiff
// TextEdit with byte offsets into the saved deployment.
func editJSON(start, end int, newText string) string {
	return fmt.Sprintf(
		`{"Span":{"uri":"","start":{"line":1,"column":1,"offset":%d},"end":{"line":1,"column":1,"offset":%d}},"NewText":%q}`,
		start,
		end,
		newText,
	)
}

// deltaBaseText is the saved deployment every delta rule case starts from.
const deltaBaseText = `abcdef`

// savedDelta starts a mutating update and saves the base deployment, so a
// delta case applies to known bytes.
func savedDelta(t *testing.T) (updatesFixture, stack.Started) {
	t.Helper()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(1, deltaBaseText)); err != nil {
		t.Fatalf("save base: %v", err)
	}

	return f, s
}

// expectInvalidDelta proves a delta save fails with ErrInvalidDelta.
func expectInvalidDelta(t *testing.T, f updatesFixture, s stack.Started, in stack.DeltaInput, name string) {
	t.Helper()

	err := f.svc.SaveCheckpointDelta(t.Context(), f.ref, s.Update.ID, s.Token, in)
	if !errors.Is(err, stack.ErrInvalidDelta) {
		t.Fatalf("%s: %v, want ErrInvalidDelta", name, err)
	}
}

// TestCheckpointDeltaValid proves a valid delta replaces and stores the
// result under the next sequence number.
func TestCheckpointDeltaValid(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	in := deltaOf(2, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err != nil {
		t.Fatalf("save delta: %v", err)
	}

	cp, err := f.svc.Export(ctx, f.ref)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if got := string(cp.Deployment); got != `abXYef` {
		t.Errorf("export = %q, want %q", got, `abXYef`)
	}
}

// TestCheckpointDeltaOverlapping proves overlapping edits fail and save
// nothing.
func TestCheckpointDeltaOverlapping(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)

	edits := "[" + editJSON(1, 3, "X") + "," + editJSON(2, 4, "Y") + "]"
	expectInvalidDelta(t, f, s, deltaOf(2, `aXYdef`, edits), "overlapping edits")

	assertExport(t, f, deltaBaseText)
}

// TestCheckpointDeltaOutside proves an edit outside the text fails and
// saves nothing.
func TestCheckpointDeltaOutside(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	for _, edits := range []string{
		"[" + editJSON(4, 9, "X") + "]",
		"[" + editJSON(-1, 2, "X") + "]",
		"[" + editJSON(3, 2, "X") + "]",
	} {
		err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, deltaOf(2, deltaBaseText, edits))
		if !errors.Is(err, stack.ErrInvalidDelta) {
			t.Fatalf("edits %s: %v, want ErrInvalidDelta", edits, err)
		}
	}

	assertExport(t, f, deltaBaseText)
}

// TestCheckpointDeltaWrongHash proves a wrong hash fails and saves nothing.
func TestCheckpointDeltaWrongHash(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	in := deltaOf(2, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	in.Hash = hex.EncodeToString(make([]byte, sha256.Size))

	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err == nil ||
		!errors.Is(err, stack.ErrInvalidDelta) {
		t.Fatalf("wrong hash: %v, want ErrInvalidDelta", err)
	}

	assertExport(t, f, deltaBaseText)
}

// TestCheckpointDeltaOutOfOrder proves a skipped sequence fails and saves
// nothing.
func TestCheckpointDeltaOutOfOrder(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	in := deltaOf(3, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err == nil ||
		!errors.Is(err, stack.ErrInvalidDelta) {
		t.Fatalf("skipped sequence: %v, want ErrInvalidDelta", err)
	}

	assertExport(t, f, deltaBaseText)
}

// TestCheckpointDeltaRetry proves a retry with the same hash succeeds
// without writing.
func TestCheckpointDeltaRetry(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	in := deltaOf(2, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err != nil {
		t.Fatalf("save delta: %v", err)
	}

	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err != nil {
		t.Fatalf("retry delta: %v", err)
	}

	assertExport(t, f, `abXYef`)
}

// TestCheckpointDeltaSameSequenceOtherHash proves the same sequence with
// another hash fails.
func TestCheckpointDeltaSameSequenceOtherHash(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	in := deltaOf(2, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err != nil {
		t.Fatalf("save delta: %v", err)
	}

	other := deltaOf(2, `abZZef`, "["+editJSON(2, 4, "ZZ")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, other); err == nil ||
		!errors.Is(err, stack.ErrInvalidDelta) {
		t.Fatalf("same sequence, other content: %v, want ErrInvalidDelta", err)
	}

	assertExport(t, f, `abXYef`)
}

// TestCheckpointDeltaOverLimit proves a result above the size limit fails.
func TestCheckpointDeltaOverLimit(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	s := f.started(t, stack.KindUpdate)

	// The fixture bounds checkpoints by 1024 bytes: a base of 1000
	// bytes saves, while a delta that grows it past the limit fails.
	large := strings.Repeat("a", 1000)
	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, large)); err != nil {
		t.Fatalf("save base: %v", err)
	}

	grown := large + strings.Repeat("b", 100)
	edits := "[" + editJSON(len(large), len(large), strings.Repeat("b", 100)) + "]"

	err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, deltaOf(2, grown, edits))
	if !errors.Is(err, stack.ErrInvalidDelta) {
		t.Fatalf("oversized result: %v, want ErrInvalidDelta", err)
	}

	assertExport(t, f, large)
}

// TestCheckpointDeltaWithoutCheckpoint proves a delta on an update that
// saved nothing fails: the first save of an update is always full.
func TestCheckpointDeltaWithoutCheckpoint(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	s := f.started(t, stack.KindUpdate)

	in := deltaOf(1, deltaBaseText, "["+editJSON(0, 0, deltaBaseText)+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); err == nil ||
		!errors.Is(err, stack.ErrInvalidDelta) {
		t.Fatalf("delta without checkpoint: %v, want ErrInvalidDelta", err)
	}
}

// TestCheckpointDeltaPreview proves a delta on a preview fails.
func TestCheckpointDeltaPreview(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	s := f.started(t, stack.KindPreview)

	in := deltaOf(1, deltaBaseText, "["+editJSON(0, 0, deltaBaseText)+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); !errors.Is(
		err,
		stack.ErrWrongUpdateState,
	) {
		t.Fatalf("preview delta: %v, want ErrWrongUpdateState", err)
	}
}

// TestCheckpointDeltaSchema proves a delta without a schema version fails.
func TestCheckpointDeltaSchema(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	in := deltaOf(2, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	in.SchemaVersion = 0

	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, in); !errors.Is(
		err,
		stack.ErrInvalidDelta,
	) {
		t.Fatalf("no schema version: %v, want ErrInvalidDelta", err)
	}

	assertExport(t, f, deltaBaseText)
}

// TestCheckpointDeltaChain proves deltas chain across sequences.
func TestCheckpointDeltaChain(t *testing.T) {
	t.Parallel()

	f, s := savedDelta(t)
	ctx := t.Context()

	first := deltaOf(2, `abXYef`, "["+editJSON(2, 4, "XY")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, first); err != nil {
		t.Fatalf("first delta: %v", err)
	}

	second := deltaOf(3, `abXY`, "["+editJSON(4, 6, "")+"]")
	if err := f.svc.SaveCheckpointDelta(ctx, f.ref, s.Update.ID, s.Token, second); err != nil {
		t.Fatalf("second delta: %v", err)
	}

	assertExport(t, f, `abXY`)
}

// assertExport proves the stack exports want: a failed delta saves
// nothing, so the last good checkpoint still serves.
func assertExport(t *testing.T, f updatesFixture, want string) {
	t.Helper()

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	if got := string(cp.Deployment); got != want {
		t.Errorf("export = %q, want %q", got, want)
	}
}

// FuzzCheckpointDelta feeds random deployment deltas to a delta save and
// proves the save either applies them or fails with ErrInvalidDelta: a
// delta never panics and never fails with another error.
func FuzzCheckpointDelta(f *testing.F) {
	f.Helper()

	seeds := []string{
		`[]`,
		"[" + editJSON(2, 4, "XY") + "]",
		"[" + editJSON(1, 3, "X") + "," + editJSON(2, 4, "Y") + "]",
		"[" + editJSON(4, 99, "X") + "]",
		`null`,
		`{}`,
		`[{"Span":{},"NewText":"x"}]`,
		`not json`,
		"[" + editJSON(2, 4, "XY") + "] trailing",
		`[{"span":{"start":{"offset":0},"end":{"offset":6}},"newText":"abcdef"}]`,
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, delta string) {
		fx := newUpdates(t)
		ctx := t.Context()

		s := fx.started(t, stack.KindUpdate)

		if err := fx.svc.SaveCheckpoint(ctx, fx.ref, s.Update.ID, s.Token, checkpoint(1, `abcdef`)); err != nil {
			t.Fatalf("save base: %v", err)
		}

		in := deltaOf(2, `abcdef`, delta)
		// The fixed hash only matches deltas that reproduce the base, so
		// most inputs fail: the point is what they fail with.
		err := fx.svc.SaveCheckpointDelta(ctx, fx.ref, s.Update.ID, s.Token, in)

		switch {
		case err == nil:
			cp, exportErr := fx.svc.Export(ctx, fx.ref)
			if exportErr != nil {
				t.Fatalf("export after delta: %v", exportErr)
			}

			sum := sha256.Sum256(cp.Deployment)
			if hex.EncodeToString(sum[:]) != in.Hash {
				t.Fatal("saved deployment does not match the delta hash")
			}
		case errors.Is(err, stack.ErrInvalidDelta):
		default:
			t.Fatalf("delta %q: %v, want nil or ErrInvalidDelta", delta, err)
		}
	})
}
