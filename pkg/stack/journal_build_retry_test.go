package stack_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// failFirstBuiltStore fails the first built checkpoint write after the blob
// is stored, like a commit that never landed.
type failFirstBuiltStore struct {
	*journalMemStore

	mu     sync.Mutex
	failed bool
}

// PutBuiltCheckpoint fails the first call and passes later calls through.
func (s *failFirstBuiltStore) PutBuiltCheckpoint(
	ctx context.Context, cp stack.Checkpoint, fence namespace.Fence,
) ([]blob.Key, error) {
	s.mu.Lock()
	first := !s.failed
	s.failed = true
	s.mu.Unlock()

	if first {
		return nil, stack.ErrCheckpointUnavailable
	}

	return s.journalMemStore.PutBuiltCheckpoint(ctx, cp, fence)
}

// changeAfterFirstReplayer replays with the real replayer. From the second
// call on, it changes the ID of the new resource in the output, so the
// retry builds a different deployment.
type changeAfterFirstReplayer struct {
	inner stack.JournalReplayer

	mu    sync.Mutex
	calls int
}

// Replay implements stack.JournalReplayer.
func (r *changeAfterFirstReplayer) Replay(
	ctx context.Context, openBase func(context.Context) (io.ReadCloser, error), entries io.Reader, out io.Writer,
) error {
	r.mu.Lock()
	r.calls++
	later := r.calls > 1
	r.mu.Unlock()

	var buf bytes.Buffer
	if err := r.inner.Replay(ctx, openBase, entries, &buf); err != nil {
		return err
	}

	got := buf.Bytes()
	if later {
		got = bytes.ReplaceAll(got, []byte(`"id":"b"`), []byte(`"id":"c"`))
	}

	_, err := out.Write(got)

	return err
}

// newRetryFixture returns a build fixture whose first built checkpoint
// write fails after the blob is stored.
func newRetryFixture(t *testing.T, clk *clock, replayer stack.JournalReplayer) *buildFixture {
	t.Helper()

	return newBuildFixtureWith(t, clk, replayer, func(m *journalMemStore) stack.CheckpointStore {
		return &failFirstBuiltStore{journalMemStore: m, mu: sync.Mutex{}, failed: false}
	})
}

// TestJournalBuildRetryAfterClockMoves checks that a complete retried after
// a failed built checkpoint write succeeds when the clock moved between the
// two builds. Only the manifest time of the two builds differs, so the retry
// adopts the blob of the first build.
func TestJournalBuildRetryAfterClockMoves(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newRetryFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)
	first := f.replay(t)

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err == nil {
		t.Fatal("complete with a failed write: err = nil, want an error")
	}

	clk.Advance(time.Second)

	if bytes.Equal(f.replay(t), first) {
		t.Fatal("replay after the clock moved = the first replay, want a new manifest time")
	}

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("retry complete after the clock moved: %v", err)
	}

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export after retry: %v", err)
	}

	if !bytes.Equal(cp.Deployment, first) {
		t.Fatalf("export after retry = %s, want the first build %s", cp.Deployment, first)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 0 {
		t.Fatalf("journal keys after retry = %v, want none", keys)
	}
}

// TestJournalBuildRetryOtherContentConflicts checks that a retry whose build
// differs from the stored blob in more than the manifest fails with
// ErrConcurrentModification and keeps the journal.
func TestJournalBuildRetryOtherContentConflicts(t *testing.T) {
	t.Parallel()

	clk := newClock()
	replayer := &changeAfterFirstReplayer{inner: pulumijournal.NewReplayer(clk.Now, ""), mu: sync.Mutex{}, calls: 0}
	f := newRetryFixture(t, clk, replayer)

	s := f.startJournaled(t)

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err == nil {
		t.Fatal("complete with a failed write: err = nil, want an error")
	}

	_, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrConcurrentModification) {
		t.Fatalf("retry complete with other content: err = %v, want ErrConcurrentModification", err)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 2 {
		t.Fatalf("journal keys after refused retry = %v, want the two batches", keys)
	}
}
