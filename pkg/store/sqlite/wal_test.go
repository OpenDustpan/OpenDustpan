package sqlite_test

// Tests for the write-ahead log guard: the pragmas the store sets, the bound
// the guard holds under concurrent load, a backup taken while the log
// outgrows the database file, writes that succeed while checkpoints stay
// busy, and the single warning a long busy period writes.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestWALGuardBoundsLogUnderLoad runs writers and readers through the public
// store API with a small guard threshold and asserts the write-ahead log
// stays below a bound. The load runs in phased bursts: every round starts 8
// readers, gives them a head start, then starts 8 writers, joins them, and
// pauses every worker at once. The readers keep short read transactions open
// all the time during a burst, so the passive automatic checkpoint never
// restarts the log: every writer commit meets an active reader, because the
// readers resume first after each pause. The shared pause models the request
// boundaries of real CLI traffic and gives the guard an instant with no
// readers to force its truncate checkpoint, which its retries always reach.
// Without the guard the log grows past the bound.
func TestWALGuardBoundsLogUnderLoad(t *testing.T) {
	t.Parallel()

	restore := sqlite.SetWALGuardForTest(20*time.Millisecond, 1<<20, 30*time.Second, 5*time.Second)
	defer restore()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	stopSampler := make(chan struct{})
	samplerDone := make(chan int64, 1)

	go sampleWAL(ctx, path, stopSampler, samplerDone)

	const rounds = 20

	const writerOps = 3

	const readerOps = 20

	var counter atomic.Int64

	for range rounds {
		var wg sync.WaitGroup

		roundErrs := make([]error, 16)

		for r := range 8 {
			wg.Go(func() {
				roundErrs[8+r] = readWALUnits(ctx, s, ns, readerOps)
			})
		}

		pauseLoad(ctx, t, 2*time.Millisecond)

		for w := range 8 {
			wg.Go(func() {
				roundErrs[w] = writeWALUnits(ctx, walWriter{store: s, ns: ns, counter: &counter, worker: w}, writerOps)
			})
		}

		wg.Wait()

		for i, err := range roundErrs {
			if err != nil {
				t.Fatalf("worker %d: %v", i, err)
			}
		}

		pauseLoad(ctx, t, 8*time.Millisecond)
	}

	close(stopSampler)

	maxWAL := <-samplerDone

	t.Logf("maximum write-ahead log size under load: %d bytes", maxWAL)

	const bound = 8 << 20

	if maxWAL >= bound {
		t.Fatalf("maximum write-ahead log size = %d bytes, want below %d bytes", maxWAL, bound)
	}
}

// pauseLoad pauses every load worker at once for d, so the guard meets an
// instant with no readers. The pause ends early when ctx ends.
func pauseLoad(ctx context.Context, t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)

	select {
	case <-ctx.Done():
		timer.Stop()
		t.Fatalf("load context ended: %v", ctx.Err())
	case <-timer.C:
	}
}

// walWriter holds one load writer: its store, namespace, worker number and
// the shared name counter.
type walWriter struct {
	store   *sqlite.Store
	ns      namespace.Namespace
	counter *atomic.Int64
	worker  int
}

// writeWALUnits writes ops stacks and workspaces with unique names through
// the public store API.
func writeWALUnits(ctx context.Context, w walWriter, ops int) error {
	project := "proj-" + strconv.Itoa(w.worker)

	for range ops {
		n := w.counter.Add(1)

		if n%2 == 0 {
			if err := createWALStack(ctx, w.store, w.ns, project, n); err != nil {
				return err
			}
		} else if err := createWALWorkspace(ctx, w.store, w.ns, w.worker, n); err != nil {
			return err
		}
	}

	return nil
}

// createWALStack creates one stack with a unique name for the load run.
func createWALStack(
	ctx context.Context, s *sqlite.Store, ns namespace.Namespace, project string, n int64,
) error {
	ref, err := stack.ParseRef("acme", project, "stack-"+strconv.FormatInt(n, 10))
	if err != nil {
		return err
	}

	now := time.Now()

	st := stack.Stack{
		CreatedAt: now,
		UpdatedAt: now,
		Tags:      map[string]string{"worker": project},
		Ref:       ref,
		ID:        stack.ID(ident.NewID("stk-")),
		Version:   0,
	}

	return s.CreateStack(ctx, st, ns.Fence())
}

// createWALWorkspace creates one workspace with a unique name for the load
// run.
func createWALWorkspace(
	ctx context.Context, s *sqlite.Store, ns namespace.Namespace, worker int, n int64,
) error {
	name, err := workspace.ParseName("space-" + strconv.Itoa(worker) + "-" + strconv.FormatInt(n, 10))
	if err != nil {
		return err
	}

	now := time.Now()

	w := workspace.Workspace{
		CreatedAt: now,
		UpdatedAt: now,
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        workspace.ID(ident.NewID("ws-")),
		Ref:       workspace.Ref{Namespace: ns.Name, Name: name},
		Revision:  1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	return s.CreateWorkspace(ctx, w, ns.Fence())
}

// readWALUnits lists stacks and workspaces ops times, keeping short read
// transactions open so the automatic checkpoint cannot restart the log.
func readWALUnits(ctx context.Context, s *sqlite.Store, ns namespace.Namespace, ops int) error {
	for range ops {
		if _, err := s.ListStacks(ctx, stack.Filter{Namespace: ns.Name, Project: ""}); err != nil {
			return err
		}

		if _, err := s.ListWorkspaces(ctx, ns.Name); err != nil {
			return err
		}
	}

	return nil
}

// sampleWAL records the largest write-ahead log size seen every 10 ms until
// stop closes or ctx ends, then reports the maximum on done. Done holds one
// slot, so the sampler never blocks on its report, even when the test fails
// first and stops listening.
func sampleWAL(ctx context.Context, path string, stop <-chan struct{}, done chan<- int64) {
	var maxWAL int64

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			done <- maxWAL

			return
		case <-stop:
			done <- maxWAL

			return
		case <-ticker.C:
			if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > maxWAL {
				maxWAL = info.Size()
			}
		}
	}
}

// TestWALPragmas proves the store sets the write-ahead log pragmas
// explicitly: the automatic checkpoint size and the journal size limit.
func TestWALPragmas(t *testing.T) {
	t.Parallel()

	s := open(t)

	pragmas, err := sqlite.WALPragmasForTest(t.Context(), s)
	if err != nil {
		t.Fatalf("read write-ahead log pragmas: %v", err)
	}

	if pragmas.AutoCheckpoint != 1000 {
		t.Fatalf("wal_autocheckpoint = %d, want 1000", pragmas.AutoCheckpoint)
	}

	if pragmas.JournalSizeLimit != 67108864 {
		t.Fatalf("journal_size_limit = %d, want 67108864", pragmas.JournalSizeLimit)
	}
}

// TestBackupWithLargeWAL holds a read transaction open on a second handle so
// the log cannot restart, grows the log past the database file with the
// guard disabled, and proves the backup still captures every row written
// before it.
func TestBackupWithLargeWAL(t *testing.T) {
	t.Parallel()

	restore := sqlite.SetWALGuardForTest(20*time.Millisecond, 1<<62, 30*time.Second, 5*time.Second)
	defer restore()

	ctx := t.Context()
	dir := t.TempDir()

	src := openWALBackupSource(ctx, t, dir)

	refs := growWALPastDB(ctx, t, src.store, src.ns, src.path)

	sizes := fileSizes(t, src.path)
	if sizes.wal <= sizes.db {
		t.Fatalf(
			"write-ahead log size = %d bytes with database = %d bytes, want the log larger",
			sizes.wal,
			sizes.db,
		)
	}

	bk := exportWALBackup(t, src.store, sizes.wal)

	if rbErr := src.tx.Rollback(); rbErr != nil {
		t.Fatalf("roll back read transaction: %v", rbErr)
	}

	dst := importWALBackup(ctx, t, dir, bk)

	checkRestoredRows(ctx, t, src, dst, refs)
}

// walBackupSource holds the source store of the backup test with its
// namespace, path and held read transaction.
type walBackupSource struct {
	store *sqlite.Store
	ns    namespace.Namespace
	tx    *sql.Tx
	path  string
}

// openRawDB opens a second database/sql handle to the database file at path
// with the store busy timeout. Raw test handles run beside the store while
// its guard ticks: without the timeout a guard TRUNCATE that blocks writers
// fails a concurrent raw write with SQLITE_BUSY. The WAL tests use it to
// hold a reader open; the tamper helpers in inspect_test.go and sqlite_test.go
// use it too, so keep the timeout in sync with the store default.
func openRawDB(path string) (*sql.DB, error) {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(10000)")

	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open raw database: %w", err)
	}

	return conn, nil
}

// openWALBackupSource opens the source store, creates its namespace, and
// holds a read transaction open on a second handle to the same file, so the
// log cannot restart while the test grows it.
func openWALBackupSource(ctx context.Context, t *testing.T, dir string) walBackupSource {
	t.Helper()

	path := filepath.Join(dir, "dustpan.db")

	src, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := src.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if nsErr := src.CreateNamespace(ctx, ns); nsErr != nil {
		t.Fatalf("create namespace: %v", nsErr)
	}

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin read transaction: %v", err)
	}

	var count int64

	if scanErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM namespaces`).Scan(&count); scanErr != nil {
		t.Fatalf("read through the held transaction: %v", scanErr)
	}

	return walBackupSource{store: src, ns: ns, tx: tx, path: path}
}

// walBackup holds a portable backup taken with a large write-ahead log: the
// table files by path, the summary and the descriptors.
type walBackup struct {
	files map[string][]byte
	sum   backup.Summary
	descs []backup.PortableFile
}

// exportWALBackup exports src in the portable format into memory. It logs how
// long the backup takes with a large log behind it.
func exportWALBackup(t *testing.T, src *sqlite.Store, walSize int64) walBackup {
	t.Helper()

	start := time.Now()

	files, sum, descs := storetest.PortableFiles(t, src)

	t.Logf("backup with a %d byte write-ahead log took %s", walSize, time.Since(start))

	return walBackup{files: files, sum: sum, descs: descs}
}

// importWALBackup opens a fresh store, stages the portable files of bk into it
// and applies the staged data, then returns the restored store.
func importWALBackup(ctx context.Context, t *testing.T, dir string, bk walBackup) *sqlite.Store {
	t.Helper()

	dst, err := sqlite.Open(ctx, filepath.Join(dir, "restored.db"))
	if err != nil {
		t.Fatalf("open restore destination: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := dst.Close(); closeErr != nil {
			t.Errorf("close restore destination: %v", closeErr)
		}
	})

	if _, err := dst.StagePortable(ctx, storetest.PortableOpener(bk.files), bk.sum, bk.descs); err != nil {
		t.Fatalf("stage backup: %v", err)
	}

	if err := dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	return dst
}

// growWALPastDB writes stacks until the write-ahead log file is larger than
// the database file and returns every reference written. It caps the batches
// so a broken test fails fast instead of writing forever.
func growWALPastDB(
	ctx context.Context, t *testing.T, s *sqlite.Store, ns namespace.Namespace, path string,
) []stack.Ref {
	t.Helper()

	var refs []stack.Ref

	for batch := range 30 {
		for i := range 200 {
			ref, err := stack.ParseRef("acme", "backup", "row-"+strconv.Itoa(batch*200+i))
			if err != nil {
				t.Fatalf("parse stack reference: %v", err)
			}

			now := time.Now()

			st := stack.Stack{
				CreatedAt: now,
				UpdatedAt: now,
				Tags:      map[string]string{"batch": strconv.Itoa(batch)},
				Ref:       ref,
				ID:        stack.ID(ident.NewID("stk-")),
				Version:   0,
			}

			if err := s.CreateStack(ctx, st, ns.Fence()); err != nil {
				t.Fatalf("create stack %s: %v", ref, err)
			}

			refs = append(refs, ref)
		}

		sizes := fileSizes(t, path)
		if sizes.wal > sizes.db {
			t.Logf(
				"write-ahead log size = %d bytes with database = %d bytes after %d stacks",
				sizes.wal,
				sizes.db,
				len(refs),
			)

			return refs
		}
	}

	t.Fatal("write-ahead log never outgrew the database file")

	return nil
}

// dbSizes holds the sizes of the database file and its write-ahead log.
type dbSizes struct {
	db  int64
	wal int64
}

// fileSizes returns the sizes of the database file and its write-ahead log
// (0 when the log file is absent).
func fileSizes(t *testing.T, path string) dbSizes {
	t.Helper()

	var sizes dbSizes

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database file: %v", err)
	}

	sizes.db = info.Size()

	if info, err := os.Stat(path + "-wal"); err == nil {
		sizes.wal = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat write-ahead log: %v", err)
	}

	return sizes
}

// checkRestoredRows proves every written stack is in the restored store with
// its identity and tags, and both stores hold the same stack count.
func checkRestoredRows(
	ctx context.Context, t *testing.T, src walBackupSource, dst *sqlite.Store, refs []stack.Ref,
) {
	t.Helper()

	srcList, err := src.store.ListStacks(ctx, stack.Filter{Namespace: src.ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list source stacks: %v", err)
	}

	dstList, err := dst.ListStacks(ctx, stack.Filter{Namespace: src.ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list restored stacks: %v", err)
	}

	if len(dstList) != len(srcList) || len(dstList) != len(refs) {
		t.Fatalf(
			"restored stacks = %d with source = %d and written = %d, want all equal",
			len(dstList),
			len(srcList),
			len(refs),
		)
	}

	for _, ref := range refs {
		want, err := src.store.GetStack(ctx, ref)
		if err != nil {
			t.Fatalf("get source stack %s: %v", ref, err)
		}

		got, err := dst.GetStack(ctx, ref)
		if err != nil {
			t.Fatalf("get restored stack %s: %v", ref, err)
		}

		if got.ID != want.ID || got.Version != want.Version || !maps.Equal(got.Tags, want.Tags) {
			t.Fatalf("restored stack %s = %+v, want %+v", ref, got, want)
		}
	}
}

// TestWALGuardBusyWritesSucceed holds a read transaction open on a second
// handle so every TRUNCATE checkpoint stays busy, and proves writes still
// succeed quickly: the guard gives up until the next tick instead of blocking
// writers or reporting an error. After the reader ends, the next ticks bring
// the log back under the threshold.
func TestWALGuardBusyWritesSucceed(t *testing.T) {
	t.Parallel()

	const threshold = int64(1 << 16)

	restore := sqlite.SetWALGuardForTest(20*time.Millisecond, threshold, 30*time.Second, 320*time.Millisecond)
	defer restore()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, ns := openWALBusyStore(ctx, t, path, nil)

	raw, tx := holdWALReader(ctx, t, path)

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	written := writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "busy",
	})

	t.Logf(
		"write-ahead log size = %d bytes after %d writes with the checkpoint busy",
		fileSizes(t, path).wal,
		written,
	)

	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("roll back read transaction: %v", rbErr)
	}

	waitForWALBelow(ctx, t, path, threshold, 10*time.Second)

	t.Logf("write-ahead log size = %d bytes after the reader ended", fileSizes(t, path).wal)
}

// TestWALGuardBusySlowAttemptsCapped holds a read transaction open on a
// second handle so every TRUNCATE checkpoint stays busy, and proves every
// failed tick tries at most 2 slow attempts: slow attempts wait for readers
// and may stall writers, while fast busy attempts only meet the checkpoint
// lock and stall no writer. The counts come from the test hook, not from log
// text.
func TestWALGuardBusySlowAttemptsCapped(t *testing.T) {
	t.Parallel()

	const threshold = int64(1 << 16)

	restore := sqlite.SetWALGuardForTest(20*time.Millisecond, threshold, 30*time.Second, 320*time.Millisecond)
	defer restore()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, ns := openWALBusyStore(ctx, t, path, nil)

	raw, tx := holdWALReader(ctx, t, path)

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "slowcap",
	})

	waitForFailedTicks(ctx, t, 5, 10*time.Second)

	requireBusyTickSlowCapped(t)

	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("roll back read transaction: %v", rbErr)
	}
}

// TestWALGuardBusyBackoff holds a read transaction open so every TRUNCATE
// checkpoint stays busy with a slow attempt, and proves the wait before the
// next tick doubles up to the cap: the intervals between failed ticks grow.
// After the reader ends and a truncate succeeds, a new busy period starts
// from the base interval again.
func TestWALGuardBusyBackoff(t *testing.T) {
	t.Parallel()

	const threshold = int64(1 << 16)
	const base = 20 * time.Millisecond
	const backoffCap = 160 * time.Millisecond

	restore := sqlite.SetWALGuardForTest(base, threshold, 30*time.Second, backoffCap)
	defer restore()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, ns := openWALBusyStore(ctx, t, path, nil)

	raw, tx := holdWALReader(ctx, t, path)

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "backoff-1",
	})

	waitForFailedTicks(ctx, t, 6, 15*time.Second)

	bounds := failedTickWaitBounds(t)

	t.Logf("failed ticks while busy: smallest wait = %s, largest wait = %s", bounds.min, bounds.max)

	requireBackoffGrowth(t, bounds.min, bounds.max, base, backoffCap)

	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("roll back read transaction: %v", rbErr)
	}

	// Wait for the reset to become observable: the guard's next tick after
	// the reader ends succeeds and records it. Poll the hook, not the file:
	// the file drops at checkpoint time but the tick records just after, so
	// file polling can return without an observable success.
	waitForSucceededTicks(ctx, t, 1, 10*time.Second)

	waitForWALBelow(ctx, t, path, threshold, 10*time.Second)

	t.Logf("write-ahead log size = %d bytes after the reader ended", fileSizes(t, path).wal)

	before := len(sqlite.WALGuardTicksForTest())

	pinned := pinWALReader(ctx, t, raw)

	writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "backoff-2",
	})

	waitForFailedTicks(ctx, t, before+3, 15*time.Second)

	requireBackoffReset(t, failedTicks(sqlite.WALGuardTicksForTest()[before:]), base)

	if rbErr := pinned.Rollback(); rbErr != nil {
		t.Fatalf("roll back second read transaction: %v", rbErr)
	}
}

// waitForFailedTicks polls the test hook until it holds at least want failed
// guard ticks or timeout passes.
func waitForFailedTicks(ctx context.Context, t *testing.T, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for {
		if got := len(failedTicks(sqlite.WALGuardTicksForTest())); got >= want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"failed guard ticks = %d after %s, want %d",
				len(failedTicks(sqlite.WALGuardTicksForTest())),
				timeout,
				want,
			)
		}

		timer := time.NewTimer(10 * time.Millisecond)

		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("load context ended: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

// waitForSucceededTicks polls the test hook until it holds at least want
// successful guard ticks or timeout passes.
func waitForSucceededTicks(ctx context.Context, t *testing.T, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for {
		if got := len(succeededTicks(sqlite.WALGuardTicksForTest())); got >= want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"successful guard ticks = %d after %s, want %d",
				len(succeededTicks(sqlite.WALGuardTicksForTest())),
				timeout,
				want,
			)
		}

		timer := time.NewTimer(10 * time.Millisecond)

		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("load context ended: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

// tickWaitBounds holds the smallest and the largest guard wait between the
// failed ticks the hook holds.
type tickWaitBounds struct {
	min time.Duration
	max time.Duration
}

// failedTickWaitBounds returns the smallest and the largest guard wait
// between the failed ticks the hook holds.
func failedTickWaitBounds(t *testing.T) tickWaitBounds {
	t.Helper()

	waits := tickWaits(failedTicks(sqlite.WALGuardTicksForTest()))
	if len(waits) == 0 {
		t.Fatal("no failed guard tick waits while busy, want growth to observe")
	}

	bounds := tickWaitBounds{min: waits[0], max: waits[0]}

	for _, wait := range waits[1:] {
		bounds.min = min(bounds.min, wait)
		bounds.max = max(bounds.max, wait)
	}

	return bounds
}

// requireBackoffGrowth asserts the waits between failed ticks start near the
// base interval and grow toward the backoff cap.
func requireBackoffGrowth(t *testing.T, minWait, maxWait, base, backoffCap time.Duration) {
	t.Helper()

	if maxWait < 100*time.Millisecond {
		t.Fatalf("largest failed tick wait = %s, want at least 100 ms so backoff grows the wait", maxWait)
	}

	if maxWait > backoffCap+100*time.Millisecond {
		t.Fatalf("largest failed tick wait = %s, want at most %s plus slack", maxWait, backoffCap)
	}

	if minWait > 3*base {
		t.Fatalf(
			"smallest failed tick wait = %s, want at most %s so the first ticks keep the base",
			minWait,
			3*base,
		)
	}
}

// requireBackoffReset asserts the second busy period starts from the base
// interval again: its first wait stays near the base.
func requireBackoffReset(t *testing.T, second []sqlite.WALGuardTick, base time.Duration) {
	t.Helper()

	if len(second) < 3 {
		t.Fatalf("failed ticks in the second busy period = %d, want at least 3", len(second))
	}

	resetWait := tickWaits(second)[0]

	t.Logf("first wait of the second busy period = %s, want near the %s base", resetWait, base)

	if resetWait > 3*base {
		t.Fatalf("first wait after the reset = %s, want at most %s", resetWait, 3*base)
	}
}

// failedTicks returns the failed guard ticks in order.
func failedTicks(ticks []sqlite.WALGuardTick) []sqlite.WALGuardTick {
	var out []sqlite.WALGuardTick

	for _, tick := range ticks {
		if !tick.Succeeded {
			out = append(out, tick)
		}
	}

	return out
}

// succeededTicks returns the successful guard ticks in order.
func succeededTicks(ticks []sqlite.WALGuardTick) []sqlite.WALGuardTick {
	var out []sqlite.WALGuardTick

	for _, tick := range ticks {
		if tick.Succeeded {
			out = append(out, tick)
		}
	}

	return out
}

// tickWaits returns the guard wait before each tick after the first, as the
// guard recorded it: a scheduled wait, not a wall-clock gap, so a loaded
// runner cannot stretch it.
func tickWaits(ticks []sqlite.WALGuardTick) []time.Duration {
	out := []time.Duration{}

	for i := 1; i < len(ticks); i++ {
		out = append(out, ticks[i].Waited)
	}

	return out
}

// TestWALGuardBusyWritesStayFast holds a read transaction open so every
// TRUNCATE checkpoint stays busy, runs writes for 3 s past the threshold,
// and proves writers are rarely blocked: one tick tries at most 2 slow
// attempts and gives up until the next tick, so the p99 write latency stays
// far below the bound. One tick stalls writers for about 105 ms at most
// (2 slow attempts with a 50 ms busy timeout plus a 5 ms retry delay, plus
// the time of a successful checkpoint copy), so the 350 ms bound keeps more
// than a 3x margin over the theoretical worst stall. It also proves the bound
// itself through the test hook: every failed tick tried at most 2 slow
// attempts.
func TestWALGuardBusyWritesStayFast(t *testing.T) {
	t.Parallel()

	const threshold = int64(1 << 16)

	restore := sqlite.SetWALGuardForTest(20*time.Millisecond, threshold, 30*time.Second, 320*time.Millisecond)
	defer restore()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, ns := openWALBusyStore(ctx, t, path, nil)

	raw, tx := holdWALReader(ctx, t, path)

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "fast-setup",
	})

	const window = 3 * time.Second

	latencies := writeStacksForWindow(ctx, t, walTimedWrites{
		store:  s,
		ns:     ns,
		tx:     tx,
		window: window,
		prefix: "fast",
	})

	if sizes := fileSizes(t, path); sizes.wal < threshold {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Logf("roll back read transaction: %v", rbErr)
		}

		t.Fatalf(
			"write-ahead log size = %d bytes, want at least %d bytes so every checkpoint stayed busy",
			sizes.wal,
			threshold,
		)
	}

	slices.Sort(latencies)

	p50 := percentileLatency(latencies, 0.5)
	p90 := percentileLatency(latencies, 0.9)
	p99 := percentileLatency(latencies, 0.99)

	if len(latencies) == 0 {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Logf("roll back read transaction: %v", rbErr)
		}

		t.Fatalf("no timed writes in %s, want at least one sample", window)
	}

	maxLatency := latencies[len(latencies)-1]

	t.Logf(
		"%d writes in %s with the checkpoint busy: p50 = %s, p90 = %s, p99 = %s, max = %s",
		len(latencies),
		window,
		p50,
		p90,
		p99,
		maxLatency,
	)

	const bound = 350 * time.Millisecond

	if p99 >= bound {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Logf("roll back read transaction: %v", rbErr)
		}

		t.Fatalf("p99 write latency = %s over %d writes, want under %s", p99, len(latencies), bound)
	}

	requireBusyTickSlowCapped(t)

	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("roll back read transaction: %v", rbErr)
	}
}

// percentileLatency returns the latency at fraction q of the sorted
// latencies with the nearest-rank method, clamped to the samples.
func percentileLatency(sorted []time.Duration, q float64) time.Duration {
	index := max(int(math.Ceil(q*float64(len(sorted))))-1, 0)

	return sorted[min(index, len(sorted)-1)]
}

// walTimedWrites holds one timed write batch: the store, the namespace, the
// held reader to roll back on error, the window to fill, and the name prefix
// of the written stacks.
type walTimedWrites struct {
	store  *sqlite.Store
	ns     namespace.Namespace
	tx     *sql.Tx
	window time.Duration
	prefix string
}

// writeStacksForWindow writes stacks from job through the public store API
// for the window and returns every write latency. A write error rolls the
// held reader back and stops the test, so the reader never outlives it.
func writeStacksForWindow(ctx context.Context, t *testing.T, job walTimedWrites) []time.Duration {
	t.Helper()

	latencies := []time.Duration{}

	deadline := time.Now().Add(job.window)

	for i := 0; time.Now().Before(deadline); i++ {
		ref, err := stack.ParseRef("acme", "busy", job.prefix+"-"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("parse stack reference: %v", err)
		}

		now := time.Now()

		st := stack.Stack{
			CreatedAt: now,
			UpdatedAt: now,
			Tags:      map[string]string{"run": "fast"},
			Ref:       ref,
			ID:        stack.ID(ident.NewID("stk-")),
			Version:   0,
		}

		start := time.Now()

		if err := job.store.CreateStack(ctx, st, job.ns.Fence()); err != nil {
			if rbErr := job.tx.Rollback(); rbErr != nil {
				t.Logf("roll back read transaction: %v", rbErr)
			}

			t.Fatalf("create stack %s: %v", ref, err)
		}

		latencies = append(latencies, time.Since(start))
	}

	return latencies
}

// requireBusyTickSlowCapped asserts the guard gave up at least one failed tick
// while busy and every failed tick tried at most 2 slow attempts and 20
// attempts in total. The counts come from the test hook, so the bound on the
// writer stall of one tick holds without depending on machine timing.
func requireBusyTickSlowCapped(t *testing.T) {
	t.Helper()

	failed := failedTicks(sqlite.WALGuardTicksForTest())
	if len(failed) == 0 {
		t.Fatal("no failed guard tick while the checkpoint stayed busy, want at least one")
	}

	for i, tick := range failed {
		if tick.SlowAttempts > 2 {
			t.Fatalf("failed guard tick %d tried %d slow attempts, want at most 2", i, tick.SlowAttempts)
		}

		if tick.Attempts > 20 {
			t.Fatalf("failed guard tick %d tried %d attempts, want at most 20", i, tick.Attempts)
		}
	}

	t.Logf("failed guard ticks while busy = %d, each with at most 2 slow attempts", len(failed))
}

// TestWALGuardBusyWarnsOnce holds a read transaction open so every TRUNCATE
// checkpoint stays busy, and proves the guard writes exactly one warning per
// busy period: one while busy with the log size, none more until the reader
// ends and a truncate succeeds, and a second warning after a new busy period.
func TestWALGuardBusyWarnsOnce(t *testing.T) {
	t.Parallel()

	const threshold = int64(1 << 16)
	const warnAfter = 300 * time.Millisecond

	restore := sqlite.SetWALGuardForTest(20*time.Millisecond, threshold, warnAfter, 320*time.Millisecond)
	defer restore()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "dustpan.db")

	recorder := &walLogRecorder{mu: sync.Mutex{}, records: nil}

	s, ns := openWALBusyStore(ctx, t, path, slog.New(recorder))

	raw, tx := holdWALReader(ctx, t, path)

	t.Cleanup(func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "warn-1",
	})

	waitForBusyWarnings(ctx, t, recorder, 1, 10*time.Second)

	// Stay busy past a second warn period: no repeat until a truncate
	// succeeds.
	pauseLoad(ctx, t, 700*time.Millisecond)

	requireBusyWarningCount(t, recorder, 1, "while busy")

	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatalf("roll back read transaction: %v", rbErr)
	}

	waitForWALBelow(ctx, t, path, threshold, 10*time.Second)

	// Let the next ticks record the success before the new busy period.
	pauseLoad(ctx, t, 100*time.Millisecond)

	requireBusyWarningCount(t, recorder, 1, "after success")

	pinned := pinWALReader(ctx, t, raw)

	writeStacksPastThreshold(ctx, t, walWriteJob{
		store:     s,
		ns:        ns,
		path:      path,
		threshold: threshold,
		prefix:    "warn-2",
	})

	waitForBusyWarnings(ctx, t, recorder, 2, 10*time.Second)

	warnings := recorder.busyWarnings()
	if len(warnings) != 2 {
		t.Fatalf("busy warnings after the second period = %d, want exactly 2", len(warnings))
	}

	requireBusyWarningAttrs(t, warnings, threshold, warnAfter)

	t.Logf("busy warnings = %d with wal_size = %v", len(warnings), warnings[0].attrs["wal_size"])

	if rbErr := pinned.Rollback(); rbErr != nil {
		t.Fatalf("roll back second read transaction: %v", rbErr)
	}
}

// openWALBusyStore opens a store at path with logger (nil keeps the discard
// default), creates its namespace, and returns both.
func openWALBusyStore(
	ctx context.Context, t *testing.T, path string, logger *slog.Logger,
) (*sqlite.Store, namespace.Namespace) {
	t.Helper()

	var s *sqlite.Store

	var err error

	if logger == nil {
		s, err = sqlite.Open(ctx, path)
	} else {
		s, err = sqlite.Open(ctx, path, sqlite.WithLogger(logger))
	}

	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	ns := namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return s, ns
}

// requireBusyWarningCount asserts the recorder holds want busy warnings at
// stage.
func requireBusyWarningCount(t *testing.T, recorder *walLogRecorder, want int, stage string) {
	t.Helper()

	if got := len(recorder.busyWarnings()); got != want {
		t.Fatalf("busy warnings %s = %d, want exactly %d", stage, got, want)
	}
}

// requireBusyWarningAttrs asserts every warning carries the log size in bytes
// and the time since the last successful truncate.
func requireBusyWarningAttrs(
	t *testing.T, warnings []walLogRecord, threshold int64, warnAfter time.Duration,
) {
	t.Helper()

	for i, warning := range warnings {
		size, ok := warning.attrs["wal_size"]
		if !ok || size.Int64() < threshold {
			t.Fatalf("warning %d wal_size = %v, want at least %d bytes", i, size.Int64(), threshold)
		}

		since, ok := warning.attrs["since_last_success"]
		if !ok || since.Duration() < warnAfter {
			t.Fatalf("warning %d since_last_success = %v, want at least %s", i, since, warnAfter)
		}
	}
}

// holdWALReader opens a second handle to the database file at path and holds
// a read transaction open on it, so the write-ahead log cannot restart and
// every TRUNCATE checkpoint stays busy. The caller ends the hold with
// Rollback on the returned transaction.
func holdWALReader(ctx context.Context, t *testing.T, path string) (*sql.DB, *sql.Tx) {
	t.Helper()

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	return raw, pinWALReader(ctx, t, raw)
}

// pinWALReader holds a read transaction open on raw, so the write-ahead log
// cannot restart while the test grows it. The caller ends the hold with
// Rollback on the returned transaction.
func pinWALReader(ctx context.Context, t *testing.T, raw *sql.DB) *sql.Tx {
	t.Helper()

	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin read transaction: %v", err)
	}

	var count int64

	if scanErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM namespaces`).Scan(&count); scanErr != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			t.Logf("roll back read transaction: %v", rbErr)
		}

		t.Fatalf("read through the held transaction: %v", scanErr)
	}

	return tx
}

// walWriteJob holds one batch of threshold writes: the store, the namespace,
// the database path for size checks, the log size to reach, and the name
// prefix of the written stacks.
type walWriteJob struct {
	store     *sqlite.Store
	ns        namespace.Namespace
	path      string
	threshold int64
	prefix    string
}

// writeStacksPastThreshold writes stacks from job until the write-ahead log
// file reaches the threshold and returns the write count. Every write must
// finish in under 2 s without an error: a busy checkpoint never stalls a
// caller. It caps the writes so a broken test fails fast instead of writing
// forever.
func writeStacksPastThreshold(ctx context.Context, t *testing.T, job walWriteJob) int {
	t.Helper()

	const maxWrites = 3000

	for i := range maxWrites {
		ref, err := stack.ParseRef("acme", "busy", job.prefix+"-"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("parse stack reference: %v", err)
		}

		now := time.Now()

		st := stack.Stack{
			CreatedAt: now,
			UpdatedAt: now,
			Tags:      map[string]string{"run": job.prefix},
			Ref:       ref,
			ID:        stack.ID(ident.NewID("stk-")),
			Version:   0,
		}

		start := time.Now()

		if err := job.store.CreateStack(ctx, st, job.ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", ref, err)
		}

		if elapsed := time.Since(start); elapsed >= 2*time.Second {
			t.Fatalf("write %d took %s, want under 2 s", i, elapsed)
		}

		if i%20 == 19 {
			if sizes := fileSizes(t, job.path); sizes.wal >= job.threshold {
				return i + 1
			}
		}
	}

	t.Fatal("write-ahead log never reached the threshold while busy")

	return 0
}

// waitForWALBelow polls the write-ahead log file at path until it drops below
// threshold or timeout passes.
func waitForWALBelow(ctx context.Context, t *testing.T, path string, threshold int64, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for {
		if sizes := fileSizes(t, path); sizes.wal < threshold {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"write-ahead log size = %d bytes after %s, want below %d bytes",
				fileSizes(t, path).wal,
				timeout,
				threshold,
			)
		}

		timer := time.NewTimer(20 * time.Millisecond)

		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("load context ended: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

// waitForBusyWarnings polls the recorder until it holds want busy warnings
// or timeout passes.
func waitForBusyWarnings(
	ctx context.Context, t *testing.T, recorder *walLogRecorder, want int, timeout time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for {
		if got := len(recorder.busyWarnings()); got >= want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("busy warnings = %d after %s, want %d", len(recorder.busyWarnings()), timeout, want)
		}

		timer := time.NewTimer(20 * time.Millisecond)

		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("load context ended: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

// walLogRecord holds one captured log record: its level, message and
// attributes by key.
type walLogRecord struct {
	level   slog.Level
	message string
	attrs   map[string]slog.Value
}

// walLogRecorder is a slog.Handler that records every record, so tests count
// the guard warnings. It is safe for concurrent use.
type walLogRecorder struct {
	mu      sync.Mutex
	records []walLogRecord
}

// Enabled accepts every level, so no guard output escapes the capture.
func (r *walLogRecorder) Enabled(context.Context, slog.Level) bool { return true }

// Handle records rec with its attributes.
func (r *walLogRecorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]slog.Value)

	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value

		return true
	})

	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, walLogRecord{level: rec.Level, message: rec.Message, attrs: attrs})

	return nil
}

// WithAttrs returns the same recorder: attributes arrive on the record.
func (r *walLogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns the same recorder: the guard logs no groups.
func (r *walLogRecorder) WithGroup(string) slog.Handler { return r }

// busyWarnings returns the captured warnings with the guard busy message.
func (r *walLogRecorder) busyWarnings() []walLogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []walLogRecord

	for _, rec := range r.records {
		if rec.level == slog.LevelWarn && rec.message == "write-ahead log checkpoint busy" {
			out = append(out, rec)
		}
	}

	return out
}
