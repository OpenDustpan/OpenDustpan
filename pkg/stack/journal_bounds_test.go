package stack_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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

// TestAppendJournalAcceptsBatchAtLimit checks that a batch at the batch bound
// is stored: only a byte past the bound fails.
func TestAppendJournalAcceptsBatchAtLimit(t *testing.T) {
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

	at := make([]byte, stack.MaxJournalBatchBytes)
	copy(at, `{"entries":[]}`)
	for i := len(`{"entries":[]}`); i < len(at); i++ {
		at[i] = ' '
	}

	if err := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, at); err != nil {
		t.Fatalf("batch at the limit: err = %v, want nil", err)
	}
}

// stallReplayer is a JournalReplayer that blocks until its context ends, then
// reports the context error. It drives the real build deadline path.
type stallReplayer struct{}

func (stallReplayer) Replay(
	ctx context.Context, _ func(context.Context) (io.ReadCloser, error), _ io.Reader, _ io.Writer,
) error {
	<-ctx.Done()
	return fmt.Errorf("stall replayer: %w", ctx.Err())
}

// TestBuildJournalCheckpointTimesOut checks that a build past a short build
// timeout is refused with ErrJournalBuildTimeout and stores nothing.
//
//nolint:paralleltest // The test lowers the build timeout, so it runs alone.
func TestBuildJournalCheckpointTimesOut(t *testing.T) {
	old := stack.MaxJournalBuildDuration
	stack.MaxJournalBuildDuration = 20000000 // 20ms
	t.Cleanup(func() { stack.MaxJournalBuildDuration = old })

	clk := newClock()
	f := newBuildFixture(t, clk, stallReplayer{})

	s := f.startJournaled(t)

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrJournalBuildTimeout) {
		t.Fatalf("complete with a timed-out build: err = %v, want ErrJournalBuildTimeout", err)
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

// TestBuildJournalCheckpointCanceledIsNotTimeout checks that a build with a
// canceled parent context reports the cancel, not ErrJournalBuildTimeout.
//
//nolint:paralleltest // The test shares the build timeout with the timeout test, so it runs alone.
func TestBuildJournalCheckpointCanceledIsNotTimeout(t *testing.T) {
	clk := newClock()
	f := newBuildFixture(t, clk, stallReplayer{})

	s := f.startJournaled(t)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := f.svc.Complete(canceled, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if errors.Is(err, stack.ErrJournalBuildTimeout) {
		t.Fatalf("complete with a canceled context: err = %v, want a cancel, not ErrJournalBuildTimeout", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("complete with a canceled context: err = %v, want context.Canceled", err)
	}
}

// TestBuildJournalCheckpointMapsReplayBounds checks that a build past a replay
// bound fails with ErrTooLarge, which the handler answers with 413.
//
//nolint:paralleltest // The test lowers the entry bound, so it runs alone.
func TestBuildJournalCheckpointMapsReplayBounds(t *testing.T) {
	old := pulumijournal.MaxEntriesPerUpdate
	pulumijournal.MaxEntriesPerUpdate = 20
	t.Cleanup(func() { pulumijournal.MaxEntriesPerUpdate = old })

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
	for i := range 21 {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(
			`{"version":1,"kind":0,"sequenceID":` + itoa(
				int64(i+1),
			) + `,"operationID":` + itoa(
				int64(i+1),
			) + `,"removeOld":null,"removeNew":null}`,
		)
	}
	buf.WriteString(`]}`)

	if err = f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, buf.Bytes()); err != nil {
		t.Fatalf("append many entries: %v", err)
	}

	_, err = f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("complete with too many entries: err = %v, want ErrTooLarge", err)
	}
	if !errors.Is(err, pulumijournal.ErrTooManyEntries) {
		t.Fatalf("complete with too many entries: err = %v, want ErrTooManyEntries", err)
	}
	if !strings.Contains(err.Error(), "payload too large") {
		t.Fatalf("complete with too many entries: err = %v, want a clear message", err)
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
