package stack_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// refusedBatch returns a batch body that batch shape checks pass but
// verification refuses: an entry of a kind the verifier does not know.
func refusedBatch() []byte {
	return []byte(`{"entries":[{"version":1,"kind":99,"sequenceID":3,"operationID":3,` +
		`"removeOld":null,"removeNew":null}]}`)
}

// appendRefusedBatch appends a batch the verifier refuses, so the update holds
// a recorded refusal.
func appendRefusedBatch(t *testing.T, f *buildFixture, s stack.Started) {
	t.Helper()

	err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, refusedBatch())
	if !errors.Is(err, pulumijournal.ErrUnknownKind) {
		t.Fatalf("refused batch: err = %v, want the unknown kind refusal", err)
	}
}

// TestJournalCompleteRefusedBatchStaysInProgress checks that a complete with
// succeeded is refused while the update holds a recorded refusal. The message
// says that a journal batch was not stored and names pulumi cancel. The
// update stays in progress and the stack stays locked.
func TestJournalCompleteRefusedBatchStaysInProgress(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)
	appendRefusedBatch(t, f, s)

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrJournalIncomplete) {
		t.Fatalf("complete with a refusal: err = %v, want ErrJournalIncomplete", err)
	}

	if msg := err.Error(); !strings.Contains(msg, "was not stored") || !strings.Contains(msg, "pulumi cancel") {
		t.Fatalf("complete message = %q, want it to name the unstored batch and pulumi cancel", msg)
	}

	u, err := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("get update after refused complete: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}

	active, err := f.svc.Active(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("active update after refused complete: %v", err)
	}

	if active.ID != s.Update.ID {
		t.Fatalf("active update = %s, want the refused update still running", active.ID)
	}

	next, err := f.svc.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, err := f.svc.Start(t.Context(), f.ref, next.ID); !errors.Is(err, stack.ErrUpdateConflict) {
		t.Fatalf("start while refused update runs: err = %v, want ErrUpdateConflict", err)
	}
}

// TestJournalCompleteStoredRetryEndsUpdate checks that a recorded refusal
// does not block complete once a retry stored its sequences: the refused
// batch stored nothing, the retry stores sequence 3, and complete with
// succeeded ends the update.
func TestJournalCompleteStoredRetryEndsUpdate(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)
	appendRefusedBatch(t, f, s)

	retry := journalBatch(beginEntry(3, 3))
	if err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, retry); err != nil {
		t.Fatalf("retry of the refused batch: %v", err)
	}

	done, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete after the retry stored the batch: %v", err)
	}

	if done.Status != stack.StatusSucceeded {
		t.Fatalf("complete status = %q, want succeeded", done.Status)
	}
}

// TestJournalCompleteGapStaysInProgress checks that a complete with succeeded
// is refused while the stored sequence IDs hold a gap, even with no recorded
// refusal. The update stays in progress.
func TestJournalCompleteGapStaysInProgress(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	// Sequence 4 stores with no sequence 3, which leaves a gap.
	body := journalBatch(beginEntry(4, 2))
	if err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("append sequence 4: %v", err)
	}

	if seqs, err := f.store.JournalSequences(t.Context(), f.stackID, s.Update.ID); err != nil ||
		len(seqs) != 3 {
		t.Fatalf("stored sequences = %v, %v; want three with a gap", seqs, err)
	}

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrJournalIncomplete) {
		t.Fatalf("complete with a gap: err = %v, want ErrJournalIncomplete", err)
	}

	u, err := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("get update after refused complete: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}
}

// TestJournalCompleteFailedBuildsDespiteRefusal checks that a complete with
// failed still builds the checkpoint from the stored entries and ends the
// update, even with a recorded refusal.
func TestJournalCompleteFailedBuildsDespiteRefusal(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)
	appendRefusedBatch(t, f, s)

	done, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusFailed)
	if err != nil {
		t.Fatalf("complete with failed and a refusal: %v", err)
	}

	if done.Status != stack.StatusFailed {
		t.Fatalf("complete status = %q, want failed", done.Status)
	}

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export after failed complete: %v", err)
	}

	if want := f.replay(t); !bytes.Equal(cp.Deployment, want) {
		t.Fatalf("export after failed complete = %s, want replay %s", cp.Deployment, want)
	}
}

// TestJournalCancelBuildsDespiteRefusal checks that canceling a journaled
// update with a recorded refusal builds the checkpoint from the stored
// entries and ends the update.
func TestJournalCancelBuildsDespiteRefusal(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)
	appendRefusedBatch(t, f, s)

	done, err := f.svc.Cancel(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("cancel with a refusal: %v", err)
	}

	if done.Status != stack.StatusCanceled {
		t.Fatalf("cancel status = %q, want %q", done.Status, stack.StatusCanceled)
	}

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export after cancel: %v", err)
	}

	if want := f.replay(t); !bytes.Equal(cp.Deployment, want) {
		t.Fatalf("export after cancel = %s, want replay %s", cp.Deployment, want)
	}
}

// TestJournalCompleteCheckpointsIgnoreRefusals checks that a recorded refusal
// never blocks an update that saves checkpoints: only a journaled update
// reads the journal at complete.
func TestJournalCompleteCheckpointsIgnoreRefusals(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s, err := func() (stack.Started, error) {
		u, err := f.svc.Create(
			t.Context(),
			f.ref,
			stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		)
		if err != nil {
			return stack.Started{}, err
		}

		return f.svc.Start(t.Context(), f.ref, u.ID)
	}()
	if err != nil {
		t.Fatalf("start checkpoint update: %v", err)
	}

	refusal := stack.JournalRefusal{
		Ref:         f.ref,
		Update:      s.Update.ID,
		SequenceIDs: []int64{1},
		Status:      400,
		Reason:      "refused batch",
	}

	if err := f.store.RecordJournalRefusal(t.Context(), refusal, fence()); err != nil {
		t.Fatalf("record refusal: %v", err)
	}

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete of a checkpoint update with a refusal: %v", err)
	}
}
