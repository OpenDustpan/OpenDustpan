package stack_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestAppendJournalRefusesOversizedBatch checks that a batch over the batch
// bound is refused with ErrTooLarge before anything is stored.
func TestAppendJournalRefusesOversizedBatch(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0}

	u, err := f.svc.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(ctx, f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	big := make([]byte, stack.MaxJournalBatchBytes+1)
	copy(big, `{"entries":[]}`)
	for i := len(`{"entries":[]}`); i < len(big); i++ {
		big[i] = ' '
	}

	if err := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, big); !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("oversized batch: err = %v, want ErrTooLarge", err)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 0 {
		t.Fatalf("journal keys after refused batch = %v, want none", keys)
	}
}

// TestBuildJournalCheckpointTimesOut checks that a build with a canceled
// context is refused with ErrJournalBuildTimeout and stores nothing.
func TestBuildJournalCheckpointTimesOut(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := f.svc.Complete(canceled, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrJournalBuildTimeout) {
		t.Fatalf("complete with a canceled context: err = %v, want ErrJournalBuildTimeout", err)
	}

	u, err := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("get update after timed-out build: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 2 {
		t.Fatalf("journal keys after timed-out build = %v, want the two batches", keys)
	}
}

// TestBuildJournalCheckpointPropagatesReplayBounds checks that a build with
// more entries than the replay allows fails with the replay error.
func TestBuildJournalCheckpointPropagatesReplayBounds(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0}

	u, err := f.svc.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(ctx, f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString(`{"entries":[`)
	for i := int64(0); i <= pulumijournal.MaxEntriesPerUpdate; i++ {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(`{"version":1,"kind":0,"sequenceID":` + itoa(i+1) + `,"operationID":` + itoa(i+1) + `,"removeOld":null,"removeNew":null}`)
	}
	buf.WriteString(`]}`)

	if err := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, buf.Bytes()); err != nil {
		t.Fatalf("append many entries: %v", err)
	}

	_, err = f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, pulumijournal.ErrTooManyEntries) {
		t.Fatalf("complete with too many entries: err = %v, want ErrTooManyEntries", err)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}
