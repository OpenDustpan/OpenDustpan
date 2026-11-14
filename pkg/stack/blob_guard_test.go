package stack_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errRegistry is the error of a failed read of the registry.
var errRegistry = errors.New("registry unavailable")

// runningWorks are the two kinds of work that hold blob deletes back.
func runningWorks() map[string]blobguard.Work {
	return map[string]blobguard.Work{
		"a backup":  {Kind: blobguard.Backup, ID: "bkp-1"},
		"a restore": {Kind: blobguard.Restore, ID: "rst-1"},
	}
}

// updateBlobs returns the keys that the update holds in the blob store.
func updateBlobs(t *testing.T, f updatesFixture, id stack.UpdateID) []blob.Object {
	t.Helper()

	listed, err := blobtest.ListAll(t.Context(), f.blobs, f.prefix+"stk-test/"+id.String()+"/")
	if err != nil {
		t.Fatalf("list blobs of update %s: %v", id, err)
	}

	return listed
}

// memBlobOf returns the memory blob store of the fixture.
func memBlobOf(t *testing.T, f updatesFixture) *memBlob {
	t.Helper()

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	return mem
}

// TestCheckpointReplaceKeepsTheBlobWhileWorkRuns proves that a save which
// replaces a checkpoint key commits while a backup or a restore runs, keeps
// the previous blob, and that the next save deletes it after the work ended.
func TestCheckpointReplaceKeepsTheBlobWhileWorkRuns(t *testing.T) {
	t.Parallel()

	for name, work := range runningWorks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			guard := blobguardtest.NewGuard()
			f := newUpdatesGuarded(t, newClock(), nil, nil, guard)
			s := f.started(t, stack.KindUpdate)
			f.saveSeq(t, s, 1, `{"a":1}`)

			guard.Start(work)
			f.saveSeq(t, s, 2, `{"a":2}`)

			if got := updateBlobs(t, f, s.Update.ID); len(got) != 2 {
				t.Fatalf("blobs of the update while %s runs = %d, want both checkpoints", name, len(got))
			}

			cp, err := f.svc.Export(t.Context(), f.ref)
			if err != nil || string(cp.Deployment) != `{"a":2}` {
				t.Fatalf("export while %s runs: %q, %v, want the second checkpoint", name, cp.Deployment, err)
			}

			guard.End()
			memBlobOf(t, f).ageAll(2 * time.Hour)
			f.saveSeq(t, s, 3, `{"a":3}`)

			if got := updateBlobs(t, f, s.Update.ID); len(got) != 1 {
				t.Fatalf("blobs of the update after the work ended = %d, want the third checkpoint only", len(got))
			}
		})
	}
}

// saveSeq saves checkpoint seq of the update with the deployment body.
func (f updatesFixture) saveSeq(t *testing.T, s stack.Started, seq int64, body string) {
	t.Helper()

	err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(seq, envelope(body)))
	if err != nil {
		t.Fatalf("save checkpoint %d: %v", seq, err)
	}
}

// TestUpdateSweepKeepsOrphansWhileWorkRuns proves that the sweep of a save
// deletes nothing while a backup or a restore runs, and deletes the orphan
// after the work ended.
func TestUpdateSweepKeepsOrphansWhileWorkRuns(t *testing.T) {
	t.Parallel()

	for name, work := range runningWorks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			guard := blobguardtest.NewGuard()
			f := newUpdatesGuarded(t, newClock(), nil, nil, guard)
			orphan := f.orphanOfNewUpdate(t)

			guard.Start(work)

			f.saveSeq(t, orphan.update, 2, `{"a":2}`)

			if _, err := f.blobs.Get(t.Context(), orphan.key); err != nil {
				t.Fatalf("orphan while %s runs: %v, want it kept", name, err)
			}

			guard.End()

			f.saveSeq(t, orphan.update, 3, `{"a":3}`)

			if _, err := f.blobs.Get(t.Context(), orphan.key); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("orphan after the work ended: %v, want it deleted", err)
			}
		})
	}
}

// orphanOf is an update that saved one checkpoint, and an aged blob that no
// row names in its directory.
type orphanOf struct {
	update stack.Started
	key    blob.Key
}

// orphanOfNewUpdate starts an update, saves one checkpoint, and writes an
// orphan blob in the directory of the update. All blobs are older than the
// grace period.
func (f updatesFixture) orphanOfNewUpdate(t *testing.T) orphanOf {
	t.Helper()

	s := f.started(t, stack.KindUpdate)

	err := f.svc.SaveCheckpoint(
		t.Context(),
		f.ref,
		s.Update.ID,
		s.Token,
		checkpoint(1, envelope(`{"a":1}`)),
	)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	key, err := blob.ParseKey(f.prefix + "stk-test/" + s.Update.ID.String() + "/99")
	if err != nil {
		t.Fatalf("parse orphan key: %v", err)
	}

	if err := f.blobs.Put(t.Context(), key, strings.NewReader("orphan")); err != nil {
		t.Fatalf("write orphan blob: %v", err)
	}

	aged, ok := f.blobs.(interface{ ageAll(d time.Duration) })
	if !ok {
		t.Fatalf("fixture blobs = %T, want a store that ages its objects", f.blobs)
	}

	aged.ageAll(2 * time.Hour)

	return orphanOf{update: s, key: key}
}

// complete completes the update.
func (f updatesFixture) complete(t *testing.T, s stack.Started) {
	t.Helper()

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete update: %v", err)
	}
}

// TestStackSweepKeepsOrphansWhileWorkRuns proves the same for the sweep that
// the start of an update runs over the whole stack.
func TestStackSweepKeepsOrphansWhileWorkRuns(t *testing.T) {
	t.Parallel()

	for name, work := range runningWorks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			guard := blobguardtest.NewGuard()
			f := newUpdatesGuarded(t, newClock(), nil, nil, guard)
			orphan := f.orphanOfNewUpdate(t)

			f.complete(t, orphan.update)

			guard.Start(work)
			f.started(t, stack.KindUpdate)

			if _, err := f.blobs.Get(t.Context(), orphan.key); err != nil {
				t.Fatalf("orphan while %s runs: %v, want it kept", name, err)
			}

			guard.End()
			f.started(t, stack.KindPreview)

			if _, err := f.blobs.Get(t.Context(), orphan.key); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("orphan after the work ended: %v, want it deleted", err)
			}
		})
	}
}

// logBuffer is a log sink that a test reads.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// TestSweepDeletesNothingWhenTheGuardCannotAnswer proves that every answer but
// "nothing runs" keeps the orphan: a failed read, an answer with an unknown
// kind, and a service with no guard. The log line holds an error kind and no
// blob key.
func TestSweepDeletesNothingWhenTheGuardCannotAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		guard func() blobguard.Watch
	}{
		{name: "a failed read", guard: func() blobguard.Watch {
			g := blobguardtest.NewGuard()
			g.Fail(errRegistry)

			return g
		}},
		{name: "an unknown kind", guard: func() blobguard.Watch {
			g := blobguardtest.NewGuard()
			g.Start(blobguard.Work{Kind: "other", ID: "x-1"})

			return g
		}},
		{name: "no guard", guard: func() blobguard.Watch { return nil }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logs := &logBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
			f := newUpdatesGuarded(t, newClock(), nil, slog.New(slog.NewTextHandler(logs, nil)), tc.guard())
			orphan := f.orphanOfNewUpdate(t)

			f.saveSeq(t, orphan.update, 2, `{"a":2}`)

			if _, err := f.blobs.Get(t.Context(), orphan.key); err != nil {
				t.Fatalf("orphan with %s: %v, want it kept", tc.name, err)
			}

			if !strings.Contains(logs.String(), "error_kind=") || strings.Contains(logs.String(), "/99") {
				t.Fatalf("log with %s = %q, want an error kind and no blob key", tc.name, logs.String())
			}
		})
	}
}

// orderSpy records the order of the calls that a sweep makes.
type orderSpy struct {
	*memBlob

	mu     sync.Mutex
	events []string
}

func (o *orderSpy) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	o.record("list")

	return o.memBlob.ListPage(ctx, req)
}

func (o *orderSpy) Delete(ctx context.Context, key blob.Key) error {
	o.record("delete")

	return o.memBlob.Delete(ctx, key)
}

func (o *orderSpy) record(event string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.events) == 0 || o.events[len(o.events)-1] != event {
		o.events = append(o.events, event)
	}
}

func (o *orderSpy) order() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return strings.Join(o.events, ",")
}

// TestSweepsReadTheGuardAfterTheirReads proves the order of the sweeps: the
// listing, the read of the keys that rows name, the guard, and then the first
// delete. A guard read from before the listing never allows a delete.
func TestSweepsReadTheGuardAfterTheirReads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sweep func(t *testing.T, f updatesFixture, o orphanOf)
	}{
		{name: "update sweep", sweep: func(t *testing.T, f updatesFixture, o orphanOf) {
			t.Helper()
			f.saveSeq(t, o.update, 2, `{"a":2}`)
		}},
		{name: "stack sweep", sweep: func(t *testing.T, f updatesFixture, o orphanOf) {
			t.Helper()

			f.complete(t, o.update)

			f.started(t, stack.KindPreview)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			spy := &orderSpy{memBlob: newMemBlob(clk), mu: sync.Mutex{}, events: nil}
			guard := blobguardtest.NewGuard()
			guard.OnRead(func() { spy.record("guard") })

			base := newUpdatesGuarded(t, clk, spy, nil, guard)
			orphan := base.orphanOfNewUpdate(t)

			// The backup starts after the listing of the sweep and before its
			// read of the keys that rows name: a guard read before the listing
			// would not see it.
			svc := base.withKeepSetHookAndGuard(t, guard, func() {
				spy.record("keys")
				guard.Start(blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"})
			})
			base.svc = svc

			spy.mu.Lock()
			spy.events = nil
			spy.mu.Unlock()

			tc.sweep(t, base, orphan)

			if _, err := spy.Get(t.Context(), orphan.key); err != nil {
				t.Fatalf("orphan after a backup started during the sweep: %v, want it kept", err)
			}

			got := spy.order()
			// A save deletes the previous blob before the sweep starts, and that
			// delete comes before the backup. Only the calls from the keys on
			// are the sweep.
			from := strings.Index(got, "list,keys")
			if from < 0 || !strings.Contains(got, "list,keys,guard") || strings.Contains(got[from:], "delete") {
				t.Fatalf("calls of the sweep = %q, want list, keys, guard and no delete after them", got)
			}
		})
	}
}

// TestPurgeBlobDeleteWaitsForWork proves that DeleteStackBlobs deletes nothing
// while a backup or a restore runs, returns an error that names the work and
// matches blobguard.ErrWait, and deletes the blobs after the work ended.
func TestPurgeBlobDeleteWaitsForWork(t *testing.T) {
	t.Parallel()

	for name, work := range runningWorks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			guard := blobguardtest.NewGuard()
			f := newUpdatesGuarded(t, newClock(), nil, nil, guard)
			orphan := f.orphanOfNewUpdate(t)

			guard.Start(work)

			err := f.svc.DeleteStackBlobs(t.Context(), f.nsID, "stk-test")

			var wait *blobguard.WaitError
			if !errors.Is(err, blobguard.ErrWait) || !errors.As(err, &wait) || wait.Work != work {
				t.Fatalf("delete while %s runs: %v, want a WaitError for %v", name, err, work)
			}

			if _, getErr := f.blobs.Get(t.Context(), orphan.key); getErr != nil {
				t.Fatalf("blob while %s runs: %v, want it kept", name, getErr)
			}

			guard.End()

			if err := f.svc.DeleteStackBlobs(t.Context(), f.nsID, "stk-test"); err != nil {
				t.Fatalf("delete after the work ended: %v", err)
			}

			if left := updateBlobs(t, f, orphan.update.Update.ID); len(left) != 0 {
				t.Fatalf("blobs after the work ended = %d, want none", len(left))
			}
		})
	}
}

// TestPurgeBlobDeleteFailsClosed proves that a failed guard read and a service
// with no guard delete nothing and return an error that does not match
// blobguard.ErrWait.
func TestPurgeBlobDeleteFailsClosed(t *testing.T) {
	t.Parallel()

	failing := blobguardtest.NewGuard()
	failing.Fail(errRegistry)

	tests := map[string]struct {
		guard blobguard.Watch
		want  error
	}{
		"a failed read": {guard: failing, want: nil},
		"no guard":      {guard: nil, want: blobguard.ErrNoWatch},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newUpdatesGuarded(t, newClock(), nil, nil, tc.guard)
			orphan := f.orphanOfNewUpdate(t)

			err := f.svc.DeleteStackBlobs(t.Context(), f.nsID, "stk-test")
			if err == nil || errors.Is(err, blobguard.ErrWait) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("delete with %s: %v, want a failure other than a wait", name, err)
			}

			if _, getErr := f.blobs.Get(t.Context(), orphan.key); getErr != nil {
				t.Fatalf("blob with %s: %v, want it kept", name, getErr)
			}
		})
	}
}

// TestJournalBuildKeepsBatchBlobsWhileWorkRuns proves that the build of the
// checkpoint of a journaled update commits while a backup or a restore runs,
// keeps the batch blobs, and that the next start deletes them after the work
// ended.
func TestJournalBuildKeepsBatchBlobsWhileWorkRuns(t *testing.T) {
	t.Parallel()

	for name, work := range runningWorks() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			guard := blobguardtest.NewGuard()
			f := newBuildFixtureGuarded(t, clk, pulumijournal.NewReplayer(clk.Now, ""), nil, guard)
			s := f.startJournaled(t)
			before := f.journalKeys(t, s.Update.ID)

			guard.Start(work)

			if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
				t.Fatalf("complete journaled update while %s runs: %v", name, err)
			}

			if _, err := f.store.CheckpointByUpdate(t.Context(), f.ref, s.Update.ID); err != nil {
				t.Fatalf("built checkpoint while %s runs: %v", name, err)
			}

			f.requireKept(t, before, "while "+name+" runs")

			guard.End()
			f.blobs.ageAll(2 * time.Hour)
			f.startPreview(t)
			f.requireGone(t, before, "after the work ended")
		})
	}
}

// startPreview starts a preview, which sweeps the blobs of the stack.
func (f *buildFixture) startPreview(t *testing.T) {
	t.Helper()

	next, err := f.svc.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}

	if _, err := f.svc.Start(t.Context(), f.ref, next.ID); err != nil {
		t.Fatalf("start preview: %v", err)
	}
}

// requireKept checks that every key is stored.
func (f *buildFixture) requireKept(t *testing.T, keys []blob.Key, when string) {
	t.Helper()

	for _, key := range keys {
		if _, err := f.blobs.Get(t.Context(), key); err != nil {
			t.Fatalf("journal blob %s: err = %v, want it stored", when, err)
		}
	}
}

// requireGone checks that no key is stored.
func (f *buildFixture) requireGone(t *testing.T, keys []blob.Key, when string) {
	t.Helper()

	for _, key := range keys {
		if _, err := f.blobs.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("journal blob %s: err = %v, want ErrNotFound", when, err)
		}
	}
}

// restoreAtSecondRead makes the guard report a running restore from its second
// read on: the first read, before the first delete, finds nothing running.
func restoreAtSecondRead(guard *blobguardtest.Guard) {
	guard.OnRead(func() {
		if guard.Calls() == 2 {
			guard.Start(blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"})
		}
	})
}

// TestPurgeLoopReadsTheGuardBeforeEachDelete proves that a restore which starts
// between two deletes of one purge keeps the blob that is left: the guard is
// read again before each delete.
func TestPurgeLoopReadsTheGuardBeforeEachDelete(t *testing.T) {
	t.Parallel()

	guard := blobguardtest.NewGuard()
	restoreAtSecondRead(guard)

	f := newUpdatesGuarded(t, newClock(), nil, nil, guard)
	orphan := f.orphanOfNewUpdate(t)

	err := f.svc.DeleteStackBlobs(t.Context(), f.nsID, "stk-test")
	if !errors.Is(err, blobguard.ErrWait) {
		t.Fatalf("delete with a restore that starts in the loop: err = %v, want a wait", err)
	}

	// The update directory holds the checkpoint and the orphan: the first
	// delete ran, the second did not.
	if got := updateBlobs(t, f, orphan.update.Update.ID); len(got) != 1 {
		t.Fatalf("blobs left after the restore started = %d, want exactly one", len(got))
	}
}

// TestSweepLoopReadsTheGuardBeforeEachDelete proves the same for the sweep:
// two orphans are due, a restore starts after the first delete, and the second
// orphan stays.
func TestSweepLoopReadsTheGuardBeforeEachDelete(t *testing.T) {
	t.Parallel()

	guard := blobguardtest.NewGuard()
	f := newUpdatesGuarded(t, newClock(), nil, nil, guard)
	orphan := f.orphanOfNewUpdate(t)

	second, err := blob.ParseKey(f.prefix + "stk-test/" + orphan.update.Update.ID.String() + "/98")
	if err != nil {
		t.Fatalf("parse second orphan key: %v", err)
	}

	if err := f.blobs.Put(t.Context(), second, strings.NewReader("orphan")); err != nil {
		t.Fatalf("write second orphan: %v", err)
	}

	aged, ok := f.blobs.(interface{ ageAll(d time.Duration) })
	if !ok {
		t.Fatalf("fixture blobs = %T, want a store that ages its objects", f.blobs)
	}

	aged.ageAll(2 * time.Hour)

	// The save reads the guard once for the replace of its previous checkpoint,
	// then once before each delete of the sweep. The restore starts at the
	// third read, so the first orphan goes and the second stays.
	guard.OnRead(func() {
		if guard.Calls() == 3 {
			guard.Start(blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"})
		}
	})
	f.saveSeq(t, orphan.update, 2, `{"a":2}`)

	left := 0

	for _, key := range []blob.Key{orphan.key, second} {
		if _, err := f.blobs.Get(t.Context(), key); err == nil {
			left++
		}
	}

	if left != 1 {
		t.Fatalf("orphans left after a restore started in the loop = %d, want exactly one", left)
	}
}

// TestJournalBuildReadsTheGuardBeforeEachBatchDelete proves that a restore
// which starts between the deletes of two batch blobs keeps the second one:
// the build reads the guard before each delete.
func TestJournalBuildReadsTheGuardBeforeEachBatchDelete(t *testing.T) {
	t.Parallel()

	clk := newClock()
	guard := blobguardtest.NewGuard()
	f := newBuildFixtureGuarded(t, clk, pulumijournal.NewReplayer(clk.Now, ""), nil, guard)
	s := f.startJournaled(t)
	keys := f.journalKeys(t, s.Update.ID)

	if len(keys) != 2 {
		t.Fatalf("journal keys = %d, want two batches", len(keys))
	}

	// The first read of the build comes before the first batch delete. The
	// restore starts at the second read, before the second batch delete.
	first := guard.Calls()

	guard.OnRead(func() {
		if guard.Calls() == first+2 {
			guard.Start(blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"})
		}
	})

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete journaled update: %v", err)
	}

	kept := 0

	for _, key := range keys {
		if _, err := f.blobs.Get(t.Context(), key); err == nil {
			kept++
		}
	}

	if kept != 1 {
		t.Fatalf("batch blobs left after a restore started between two deletes = %d, want exactly one", kept)
	}
}
