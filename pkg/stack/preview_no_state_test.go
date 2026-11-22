package stack_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestPreviewAndDryRunWriteNoJournalOrCheckpoint pins that a preview and a dry
// run write no journal batch and no checkpoint, and that the service refuses
// each write of state of such an update with ErrWrongUpdateState.
func TestPreviewAndDryRunWriteNoJournalOrCheckpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan stack.Plan
	}{
		{
			name: "a preview",
			plan: stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		},
		{
			name: "a dry run update",
			plan: stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

			s := startJournalPlan(t, f, tc.plan)
			checkNoJournalOrCheckpoint(t, f, s)
		})
	}
}

// startJournalPlan creates an update of plan on the stack of f and starts it
// with journal version 1.
func startJournalPlan(t *testing.T, f *buildFixture, plan stack.Plan) stack.Started {
	t.Helper()

	ctx := t.Context()

	u, err := f.svc.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(ctx, f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return s
}

// checkNoJournalOrCheckpoint sends one journal batch and one checkpoint to the
// started update s, and fails t unless both are refused with
// ErrWrongUpdateState and the store holds no row, no blob and no checkpoint for
// the update.
func checkNoJournalOrCheckpoint(t *testing.T, f *buildFixture, s stack.Started) {
	t.Helper()

	ctx := t.Context()

	f.store.mu.Lock()
	checkpointsBefore := len(f.store.checkpoints)
	f.store.mu.Unlock()

	batchErr := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, journalBatch(beginEntry(1, 1)))
	if !errors.Is(batchErr, stack.ErrWrongUpdateState) {
		t.Fatalf("journal batch: %v, want ErrWrongUpdateState", batchErr)
	}

	saveErr := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope("{}")))
	if !errors.Is(saveErr, stack.ErrWrongUpdateState) {
		t.Fatalf("checkpoint: %v, want ErrWrongUpdateState", saveErr)
	}

	if rows := len(f.store.journal[s.Update.ID]); rows != 0 {
		t.Fatalf("journal rows = %d, want 0", rows)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 0 {
		t.Fatalf("journal blobs = %d, want 0", len(keys))
	}

	f.store.mu.Lock()
	_, saved := f.store.checkpoints[s.Update.ID]
	checkpointsAfter := len(f.store.checkpoints)
	f.store.mu.Unlock()

	if saved || checkpointsAfter != checkpointsBefore {
		t.Fatalf("checkpoints: saved %t, count %d -> %d, want none written",
			saved, checkpointsBefore, checkpointsAfter)
	}
}

// TestMutatingUpdateWritesJournalControl is the control of the preview pin: a
// real update writes its journal batch, so the zero counts above are not a
// broken fixture.
func TestMutatingUpdateWritesJournalControl(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}
	s := startJournalPlan(t, f, plan)

	batch := journalBatch(beginEntry(1, 1))

	if err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, batch); err != nil {
		t.Fatalf("control journal batch: %v", err)
	}

	if rows := len(f.store.journal[s.Update.ID]); rows != 1 {
		t.Fatalf("control journal rows = %d, want 1", rows)
	}
}
