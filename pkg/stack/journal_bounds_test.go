package stack_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// TestAppendJournalRefusesOversizedBatch checks that a batch over the batch
// bound is refused with ErrTooLarge before anything is stored.
func TestAppendJournalRefusesOversizedBatch(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

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

	err = f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, big)
	if err == nil || !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("oversized batch: err = %v, want ErrTooLarge", err)
	}

	if !strings.Contains(err.Error(), "pulumi up") {
		t.Fatalf("oversized batch names no command that helps: %v", err)
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

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

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

// deadlineReplayer is a JournalReplayer that reports the deadline of its
// context, then blocks until the context ends and reports the context error.
// It proves that the build runs under MaxJournalBuildDuration: stretching the
// timeout turns the deadline check red.
type deadlineReplayer struct {
	deadline chan time.Time
}

func (d deadlineReplayer) Replay(
	ctx context.Context, _ func(context.Context) (io.ReadCloser, error), _ [][]byte, _ io.Writer,
) error {
	if deadline, ok := ctx.Deadline(); ok {
		select {
		case d.deadline <- deadline:
		default:
		}
	}

	<-ctx.Done()
	return fmt.Errorf("stall replayer: %w", ctx.Err())
}

// TestBuildJournalCheckpointTimesOut checks that a build past a short build
// timeout is refused with ErrJournalBuildTimeout and stores nothing. It also
// checks that the refused build runs under the configured timeout, not a
// longer one: a timeout 100x larger turns both the elapsed and the deadline
// checks red.
//
//nolint:paralleltest // The test lowers the build timeout, so it runs alone.
func TestBuildJournalCheckpointTimesOut(t *testing.T) {
	old := stack.MaxJournalBuildDuration
	stack.MaxJournalBuildDuration = 20000000 // 20ms
	t.Cleanup(func() { stack.MaxJournalBuildDuration = old })

	clk := newClock()
	deadlines := make(chan time.Time, 1)
	f := newBuildFixture(t, clk, deadlineReplayer{deadline: deadlines})

	s := f.startJournaled(t)

	start := time.Now()
	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	elapsed := time.Since(start)
	if !errors.Is(err, stack.ErrJournalBuildTimeout) {
		t.Fatalf("complete with a timed-out build: err = %v, want ErrJournalBuildTimeout", err)
	}

	if elapsed > time.Second {
		t.Fatalf("refused build took %v, want under a second for a 20ms timeout", elapsed)
	}

	select {
	case deadline := <-deadlines:
		if got := deadline.Sub(start); got > time.Second {
			t.Fatalf("build deadline is %v after the start, want under a second for a 20ms timeout", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("build did not report its deadline")
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

// signalReplayer is a JournalReplayer that reports when the build starts,
// then blocks until its context ends and reports the context error. It proves
// that a test reaches the build: the test waits for started before it cancels.
type signalReplayer struct {
	started chan struct{}
}

func (s signalReplayer) Replay(
	ctx context.Context, _ func(context.Context) (io.ReadCloser, error), _ [][]byte, _ io.Writer,
) error {
	select {
	case s.started <- struct{}{}:
	default:
	}

	<-ctx.Done()
	return fmt.Errorf("stall replayer: %w", ctx.Err())
}

// TestBuildJournalCheckpointCanceledIsNotTimeout checks that a build canceled
// while it runs reports the cancel, not ErrJournalBuildTimeout. The test
// waits for the build to start before it cancels, so the cancel lands in the
// build: removing the parent-context check from the build turns this test red.
func TestBuildJournalCheckpointCanceledIsNotTimeout(t *testing.T) {
	t.Parallel()

	clk := newClock()
	started := make(chan struct{}, 1)
	f := newBuildFixture(t, clk, signalReplayer{started: started})

	s := f.startJournaled(t)

	canceled, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		_, err := f.svc.Complete(canceled, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("complete did not reach the build")
	}

	cancel()

	select {
	case err := <-done:
		if errors.Is(err, stack.ErrJournalBuildTimeout) {
			t.Fatalf("complete with a canceled context: err = %v, want a cancel, not ErrJournalBuildTimeout", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("complete with a canceled context: err = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("complete did not return after the cancel")
	}

	u, err := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("get update after canceled build: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 2 {
		t.Fatalf("journal keys after canceled build = %v, want the two batches", keys)
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

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

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
	checkTooLargeText(t, err)
}

// checkTooLargeText checks that a bound refusal names the limit once and the
// one command that helps.
func checkTooLargeText(t *testing.T, err error) {
	t.Helper()

	text := err.Error()
	if !strings.Contains(text, "payload too large") {
		t.Fatalf("bound refusal lacks a clear message: %v", err)
	}
	if !strings.Contains(text, "pulumi up") {
		t.Fatalf("bound refusal names no command that helps: %v", err)
	}
	if n := strings.Count(text, "exceed the limit of"); n != 1 {
		t.Fatalf("bound refusal names its limit %d times, want once: %v", n, err)
	}
}

// paddedState returns a resource state with pad bytes of padding.
func paddedState(name string, pad int) string {
	return `{"urn":"urn:pulumi:dev::web::t:mod:T::` + name + `","custom":true,"type":"t:mod:T","pad":"` +
		strings.Repeat("x", pad) + `"}`
}

// TestBuildJournalCheckpointMapsStateBound checks that a build past the
// new-state bound fails with ErrTooLarge, which the handler answers with 413.
//
//nolint:paralleltest // The test lowers the state bound, so it runs alone.
func TestBuildJournalCheckpointMapsStateBound(t *testing.T) {
	old := pulumijournal.MaxNewStateBytesPerUpdate
	pulumijournal.MaxNewStateBytesPerUpdate = 2000
	t.Cleanup(func() { pulumijournal.MaxNewStateBytesPerUpdate = old })

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	body := journalBatch(
		beginEntry(3, 2),
		successEntry(4, 2, paddedState("c", 1500)),
		successEntry(5, 3, paddedState("d", 1500)),
	)

	if err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("append states over the bound: %v", err)
	}

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("complete with oversized states: err = %v, want ErrTooLarge", err)
	}
	if !errors.Is(err, pulumijournal.ErrNewStateTooLarge) {
		t.Fatalf("complete with oversized states: err = %v, want ErrNewStateTooLarge", err)
	}
	checkTooLargeText(t, err)
}

// TestBuildJournalCheckpointMapsLinkBound checks that a build past the added
// link bound fails with ErrTooLarge, which the handler answers with 413.
//
//nolint:paralleltest // The test lowers the link bound, so it runs alone.
func TestBuildJournalCheckpointMapsLinkBound(t *testing.T) {
	old := pulumijournal.MaxLinkIndexEntriesPerUpdate
	pulumijournal.MaxLinkIndexEntriesPerUpdate = 4
	t.Cleanup(func() { pulumijournal.MaxLinkIndexEntriesPerUpdate = old })

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	body := journalBatch(
		beginEntry(3, 2),
		successEntry(4, 2, newResource),
		beginEntry(5, 3),
		successEntry(6, 3, paddedState("c", 10)),
		beginEntry(7, 4),
		successEntry(8, 4, paddedState("d", 10)),
		beginEntry(9, 5),
		successEntry(10, 5, paddedState("e", 10)),
	)

	if err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("append links over the bound: %v", err)
	}

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("complete with too many links: err = %v, want ErrTooLarge", err)
	}
	if !errors.Is(err, pulumijournal.ErrLinkIndexTooLarge) {
		t.Fatalf("complete with too many links: err = %v, want ErrLinkIndexTooLarge", err)
	}
	checkTooLargeText(t, err)
}

// TestBuildJournalCheckpointMapsAddedBytesBound checks that a build past the
// new-state plus added-link bound fails with ErrTooLarge, which the handler
// answers with 413.
//
//nolint:paralleltest // The test lowers the counted-bytes bound, so it runs alone.
func TestBuildJournalCheckpointMapsAddedBytesBound(t *testing.T) {
	old := pulumijournal.MaxNewStatePlusLinkBytesPerUpdate
	pulumijournal.MaxNewStatePlusLinkBytesPerUpdate = 1000
	t.Cleanup(func() { pulumijournal.MaxNewStatePlusLinkBytesPerUpdate = old })

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	body := journalBatch(
		beginEntry(3, 2),
		successEntry(4, 2, paddedState("c", 600)),
		successEntry(5, 3, paddedState("d", 600)),
	)

	if err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("append counted bytes over the bound: %v", err)
	}

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("complete with oversized counted bytes: err = %v, want ErrTooLarge", err)
	}
	if !errors.Is(err, pulumijournal.ErrAddedBytesTooLarge) {
		t.Fatalf("complete with oversized counted bytes: err = %v, want ErrAddedBytesTooLarge", err)
	}
	checkTooLargeText(t, err)
}

// TestAppendJournalAcceptsSingleEntryOverBatchBound checks that a batch over
// the batch bound still passes when it holds one entry and fits the
// checkpoint bound: the CLI cannot split one entry.
//
//nolint:paralleltest // The test lowers the batch bound, so it runs alone.
func TestAppendJournalAcceptsSingleEntryOverBatchBound(t *testing.T) {
	old := stack.MaxJournalBatchBytes
	stack.MaxJournalBatchBytes = 1024
	t.Cleanup(func() { stack.MaxJournalBatchBytes = old })

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	u, err := f.svc.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(ctx, f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	body := journalBatch(successEntry(1, 1, paddedState("big", 1100)))
	if len(body) <= stack.MaxJournalBatchBytes {
		t.Fatalf("single-entry batch is %d bytes, want over the lowered bound", len(body))
	}

	if err := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, body); err != nil {
		t.Fatalf("single-entry batch over the bound: err = %v, want nil", err)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 1 {
		t.Fatalf("journal keys after single-entry batch = %v, want one", keys)
	}
}

// TestAppendJournalRefusesMultiEntryOverBatchBound checks that a batch over
// the batch bound with more than one entry is still refused with ErrTooLarge,
// even when it fits the checkpoint bound.
//
//nolint:paralleltest // The test lowers the batch bound, so it runs alone.
func TestAppendJournalRefusesMultiEntryOverBatchBound(t *testing.T) {
	old := stack.MaxJournalBatchBytes
	stack.MaxJournalBatchBytes = 1024
	t.Cleanup(func() { stack.MaxJournalBatchBytes = old })

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

	u, err := f.svc.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(ctx, f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	body := journalBatch(beginEntry(1, 1), successEntry(2, 1, paddedState("big", 1100)))
	if len(body) <= stack.MaxJournalBatchBytes {
		t.Fatalf("multi-entry batch is %d bytes, want over the lowered bound", len(body))
	}

	if err := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, body); !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("multi-entry batch over the bound: err = %v, want ErrTooLarge", err)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 0 {
		t.Fatalf("journal keys after refused batch = %v, want none", keys)
	}
}

// TestMaxJournalBatchBytesFollowsBound checks that MaxJournalBatchBytes
// reports the batch bound, so lowering the bound in a test takes effect.
func TestMaxJournalBatchBytesFollowsBound(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	if got := f.svc.MaxJournalBatchBytes(); got != stack.MaxJournalBatchBytes {
		t.Fatalf("MaxJournalBatchBytes = %d, want the bound %d", got, stack.MaxJournalBatchBytes)
	}
}

// TestJournalEntriesUsesConfiguredCheckpointBound checks that the writer
// refuses a batch by the bound it was built with: a batch that fits the
// larger bound but not the smaller one is stored only by the larger writer,
// while the default bound would store both.
func TestJournalEntriesUsesConfiguredCheckpointBound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	smallBound := 1 << 20
	largeBound := 16 << 20

	if smallBound >= stack.DefaultMaxCheckpointBytes || largeBound >= stack.DefaultMaxCheckpointBytes {
		t.Fatal("test bounds must stay below the default bound")
	}

	body := journalBatch(successEntry(1, 1, paddedState("big", 8<<20)))

	if len(body) <= stack.MaxJournalBatchBytes {
		t.Fatal("test batch is below the batch bound, want a batch over it")
	}

	if len(body) <= smallBound {
		t.Fatal("test batch fits the small bound, want a batch over it")
	}

	if len(body) >= largeBound {
		t.Fatal("test batch reaches the large bound, want a batch below it")
	}

	small, err := stack.NewJournalEntries(
		newFakeBlobs(),
		newFakeRows(),
		allowAll{},
		smallBound,
		stacktest.NewRefusals(),
	)
	if err != nil {
		t.Fatalf("new journal writer with the small bound: %v", err)
	}

	if appendErr := small.Append(ctx, journalBatchFor(body), fence()); !errors.Is(appendErr, stack.ErrTooLarge) {
		t.Fatalf("append with the small bound: err = %v, want ErrTooLarge", appendErr)
	}

	large, err := stack.NewJournalEntries(
		newFakeBlobs(),
		newFakeRows(),
		allowAll{},
		largeBound,
		stacktest.NewRefusals(),
	)
	if err != nil {
		t.Fatalf("new journal writer with the large bound: %v", err)
	}

	if err := large.Append(ctx, journalBatchFor(body), fence()); err != nil {
		t.Fatalf("append with the large bound: err = %v, want nil", err)
	}
}

// TestJournalBuildTimeoutDefault checks the default build timeout: the build
// of 10,000 resources takes a fraction of a second, and the bound holds the
// answer to complete far below the 20 minutes after which the CLI stops.
func TestJournalBuildTimeoutDefault(t *testing.T) {
	t.Parallel()

	if stack.MaxJournalBuildDuration != 5*time.Minute {
		t.Fatalf("MaxJournalBuildDuration = %v, want 5m", stack.MaxJournalBuildDuration)
	}
}

// blockingBaseStore blocks the base reads of the build until the context
// ends, then reports the context error. It proves that the base loads under
// the build timeout: loading without it hangs the build past the timeout.
type blockingBaseStore struct {
	*journalMemStore

	started chan struct{}
}

func (b *blockingBaseStore) CheckpointByVersion(
	ctx context.Context, _ stack.Ref, _ int64,
) (stack.Checkpoint, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}

	<-ctx.Done()
	return stack.Checkpoint{}, fmt.Errorf("base load: %w", ctx.Err())
}

func (b *blockingBaseStore) LatestCheckpoint(
	ctx context.Context, _ stack.Ref,
) (stack.Checkpoint, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}

	<-ctx.Done()
	return stack.Checkpoint{}, fmt.Errorf("base load: %w", ctx.Err())
}

// TestBuildJournalCheckpointBaseTimeout checks that a base load past the build
// timeout is refused with ErrJournalBuildTimeout: the base loads under the
// build timeout, not the caller context.
//
//nolint:paralleltest // The test lowers the build timeout, so it runs alone.
func TestBuildJournalCheckpointBaseTimeout(t *testing.T) {
	old := stack.MaxJournalBuildDuration
	stack.MaxJournalBuildDuration = 20000000 // 20ms
	t.Cleanup(func() { stack.MaxJournalBuildDuration = old })

	clk := newClock()
	f := newBuildFixtureWith(
		t, clk, pulumijournal.NewReplayer(clk.Now, ""),
		func(store *journalMemStore) stack.CheckpointStore {
			return &blockingBaseStore{journalMemStore: store, started: make(chan struct{}, 1)}
		},
	)

	s := f.startJournaled(t)

	done := make(chan error, 1)

	go func() {
		_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, stack.ErrJournalBuildTimeout) {
			t.Fatalf("complete with a timed-out base load: err = %v, want ErrJournalBuildTimeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("complete did not return after the build timeout")
	}
}

// TestBuildJournalCheckpointBaseCancelIsNotTimeout checks that a base load
// canceled while it runs reports the cancel, not ErrJournalBuildTimeout.
//
//nolint:paralleltest // The test blocks the base load, so it runs alone.
func TestBuildJournalCheckpointBaseCancelIsNotTimeout(t *testing.T) {
	clk := newClock()
	started := make(chan struct{}, 1)
	f := newBuildFixtureWith(
		t, clk, pulumijournal.NewReplayer(clk.Now, ""),
		func(store *journalMemStore) stack.CheckpointStore {
			return &blockingBaseStore{journalMemStore: store, started: started}
		},
	)

	s := f.startJournaled(t)

	canceled, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		_, err := f.svc.Complete(canceled, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("complete did not reach the base load")
	}

	cancel()

	select {
	case err := <-done:
		if errors.Is(err, stack.ErrJournalBuildTimeout) {
			t.Fatalf("complete with a canceled base load: err = %v, want a cancel, not ErrJournalBuildTimeout", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("complete with a canceled base load: err = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("complete did not return after the cancel")
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

// TestJournalBuildDurationDefaultIsFiveMinutes checks the default build timeout.
// The timeout tests lower it and restore it, so the default is read as serial.
//
//nolint:paralleltest // The test reads the default, which the lowering tests restore before it runs.
func TestJournalBuildDurationDefaultIsFiveMinutes(t *testing.T) {
	if want := 5 * time.Minute; stack.MaxJournalBuildDuration != want {
		t.Fatalf("default build timeout is %v, want %v", stack.MaxJournalBuildDuration, want)
	}
}
