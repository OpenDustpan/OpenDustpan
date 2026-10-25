package inventory_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// sweepNow is the time of the injected clock of the sweep tests.
func sweepNow() time.Time {
	return time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
}

// errDriver stands for a driver error whose text must not reach the log.
var errDriver = errors.New("pq: connection to 10.0.0.7 refused")

// memExpired is an inventory.ExpiredDeleted over a list, through
// inventory.PageExpired.
type memExpired struct {
	entries []inventory.DeletedEntry
	calls   int
}

func (m *memExpired) ListExpiredDeleted(
	_ context.Context, now time.Time, after *inventory.ExpiredCursor, limit int,
) ([]inventory.DeletedEntry, error) {
	m.calls++

	return inventory.PageExpired(m.entries, now, after, limit), nil
}

// sweepTool is an inventory.SweepTool that records each purge with its
// due time and each blob delete, and answers the errors that errs and
// blobErrs hold for the unit ID.
type sweepTool struct {
	mu       sync.Mutex
	errs     map[string]error
	blobErrs map[string]error
	entries  []audit.Entry
	dueBys   []time.Time
	deleted  []string
	// deletedIn holds the namespace ID of each blob delete.
	deletedIn []namespace.ID
}

func newSweepTool() *sweepTool {
	return &sweepTool{
		mu: sync.Mutex{}, errs: map[string]error{}, blobErrs: map[string]error{},
		entries: nil, dueBys: nil, deleted: nil, deletedIn: nil,
	}
}

func (l *sweepTool) PurgeExpired(
	_ context.Context, _ namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.errs[unitID]; err != nil {
		return err
	}

	l.entries = append(l.entries, e)
	l.dueBys = append(l.dueBys, dueBy)

	return nil
}

func (l *sweepTool) DeletePurgedBlobs(_ context.Context, ns namespace.ID, unitID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.deleted = append(l.deleted, unitID)
	l.deletedIn = append(l.deletedIn, ns)

	return l.blobErrs[unitID]
}

// memRecords is an inventory.BlobPurges over a list, through
// inventory.PageBlobPurges.
type memRecords struct {
	records   []inventory.BlobPurge
	finished  []string
	finishErr error
	calls     int
}

func (m *memRecords) ListBlobPurges(
	_ context.Context, after *inventory.BlobPurgeCursor, limit int,
) ([]inventory.BlobPurge, error) {
	m.calls++

	return inventory.PageBlobPurges(m.records, after, limit), nil
}

func (m *memRecords) FinishBlobPurge(_ context.Context, tool, unitID string) error {
	if m.finishErr != nil {
		return m.finishErr
	}

	m.finished = append(m.finished, tool+"/"+unitID)

	return nil
}

// sweepFixture holds a Sweeper over in-memory ports and the log it writes.
type sweepFixture struct {
	sweeper *inventory.Sweeper
	expired *memExpired
	tool    *sweepTool
	records *memRecords
	log     *bytes.Buffer
}

func newSweepFixture(t *testing.T, entries []inventory.DeletedEntry, records []inventory.BlobPurge) sweepFixture {
	t.Helper()

	f := sweepFixture{
		sweeper: nil,
		expired: &memExpired{entries: entries, calls: 0},
		tool:    newSweepTool(),
		records: &memRecords{records: records, finished: nil, finishErr: nil, calls: 0},
		log:     &bytes.Buffer{},
	}

	sweeper, err := inventory.NewSweeper(inventory.SweeperConfig{
		Expired:    f.expired,
		BlobPurges: f.records,
		Lock:       newMemLock(),
		Tools:      map[string]inventory.SweepTool{"pulumi": f.tool},
		Logger:     slog.New(slog.NewTextHandler(f.log, nil)),
		Now:        sweepNow,
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}

	f.sweeper = sweeper

	return f
}

// sweepEntry is the audit entry that a sweep starts from.
func sweepEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "@server", IDPSubject: "", TokenID: "", Action: "unit:purge",
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// expiredUnits returns n deleted units of acme whose retention ended one
// hour before sweepNow, and one unit whose retention ends one hour after it.
func expiredUnits(n int) []inventory.DeletedEntry {
	out := make([]inventory.DeletedEntry, 0, n+1)

	for i := range n {
		out = append(out, inventory.DeletedEntry{
			DeletedAt:  sweepNow().Add(-31 * 24 * time.Hour),
			PurgeAfter: sweepNow().Add(-time.Hour),
			Namespace:  "acme",
			Tool:       "pulumi",
			UnitID:     fmt.Sprintf("stk-%03d", i),
			Name:       fmt.Sprintf("web/old-%03d", i),
		})
	}

	return append(out, inventory.DeletedEntry{
		DeletedAt:  sweepNow().Add(-29 * 24 * time.Hour),
		PurgeAfter: sweepNow().Add(time.Hour),
		Namespace:  "acme",
		Tool:       "pulumi",
		UnitID:     "stk-kept",
		Name:       "web/kept",
	})
}

// TestSweepPurgesExpiredUnitsOnly proves that a sweep purges each unit whose
// retention ended, page by page, and leaves a unit whose retention runs.
// Each purge carries the audit entry of the sweep with the namespace, the
// reference and the stable ID of its unit.
func TestSweepPurgesExpiredUnitsOnly(t *testing.T) {
	t.Parallel()

	n := 2*inventory.SweepPageSize + 7
	f := newSweepFixture(t, expiredUnits(n), nil)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Purged) != n || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf(
			"sweep = %d purged, %d failed, %d skipped, want %d purged",
			len(res.Purged),
			res.Failed,
			res.Skipped,
			n,
		)
	}

	if f.expired.calls != 3 {
		t.Fatalf("store calls = %d, want 3 pages", f.expired.calls)
	}

	for _, e := range f.tool.entries {
		if e.ObjectID == "stk-kept" {
			t.Fatal("sweep purged a unit whose retention still runs")
		}
	}

	checkFirstPurgeEntry(t, f.tool.entries[0])
}

// checkFirstPurgeEntry checks the audit entry of the purge of stk-000.
func checkFirstPurgeEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Namespace != "acme" || e.Object != "pulumi:acme/web/old-000" || e.ObjectID != "stk-000" ||
		e.Subject != "@server" || e.Action != "unit:purge" {
		t.Fatalf("first purge entry = %+v, want acme, pulumi:acme/web/old-000, stk-000, @server, unit:purge", e)
	}
}

// TestSweepSkipsAndCountsFailures proves that a unit that a restore took
// first or whose namespace has its home elsewhere counts as skipped without a
// warning, that any other failure counts as failed with a warning that names
// the unit and the error kind but not the error text, and that the sweep
// goes on after a failure.
func TestSweepSkipsAndCountsFailures(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t, expiredUnits(4), nil)
	f.tool.errs["stk-000"] = fmt.Errorf("purge: %w", inventory.ErrNotFound)
	f.tool.errs["stk-001"] = fmt.Errorf("fence: %w", namespace.ErrNotHome)
	f.tool.errs["stk-002"] = fmt.Errorf("purge: %w", errDriver)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Purged) != 1 || res.Skipped != 2 || res.Failed != 1 {
		t.Fatalf("sweep = %d purged, %d skipped, %d failed, want 1, 2 and 1",
			len(res.Purged), res.Skipped, res.Failed)
	}

	logged := f.log.String()
	if !strings.Contains(logged, "pulumi:acme/web/old-002") || !strings.Contains(logged, "error_kind=store") {
		t.Fatalf("log = %q, want the reference of the failed unit and its error kind", logged)
	}

	if strings.Contains(logged, "10.0.0.7") || strings.Contains(logged, "old-000") ||
		strings.Contains(logged, "old-001") {
		t.Fatalf("log = %q, want no driver text and no warning for a skipped unit", logged)
	}
}

// TestSweepFinishesOnlyOldRecords proves the grace rule of the blob purge
// records: a record younger than BlobPurgeGrace stays and its blobs are not
// deleted again, an older record whose blobs are gone is finished, and an
// older record whose delete fails stays pending and counts as a failure.
func TestSweepFinishesOnlyOldRecords(t *testing.T) {
	t.Parallel()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	records := []inventory.BlobPurge{
		{PurgedAt: old, Namespace: "acme", NamespaceID: "ns-acme", Tool: "pulumi", UnitID: "stk-gone"},
		{PurgedAt: old, Namespace: "acme", NamespaceID: "ns-acme", Tool: "pulumi", UnitID: "stk-stuck"},
		{
			PurgedAt: sweepNow().Add(-time.Hour), Namespace: "acme", NamespaceID: "ns-acme", Tool: "pulumi",
			UnitID: "stk-young",
		},
	}
	f := newSweepFixture(t, nil, records)
	f.tool.blobErrs["stk-stuck"] = fmt.Errorf("delete: %w", errDriver)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.FinishedBlobPurges != 1 || res.PendingBlobPurges != 2 || res.Failed != 1 {
		t.Fatalf("sweep = %d finished, %d pending, %d failed, want 1, 2 and 1",
			res.FinishedBlobPurges, res.PendingBlobPurges, res.Failed)
	}

	if len(f.records.finished) != 1 || f.records.finished[0] != "pulumi/stk-gone" {
		t.Fatalf("finished = %v, want pulumi/stk-gone", f.records.finished)
	}

	for _, id := range f.tool.deleted {
		if id == "stk-young" {
			t.Fatal("sweep deleted the blobs of a record inside its grace period")
		}
	}

	// The sweep gives the tool the ID of the namespace row that the record
	// carries, not the name.
	for _, ns := range f.tool.deletedIn {
		if ns != "ns-acme" {
			t.Fatalf("sweep deleted blobs in the namespace %q, want ns-acme", ns)
		}
	}

	if strings.Contains(f.log.String(), "10.0.0.7") {
		t.Fatalf("log = %q, want no driver text", f.log.String())
	}
}

// TestSweepCountsAFailedFinish proves that a record whose finish fails stays
// pending and counts as a failure.
func TestSweepCountsAFailedFinish(t *testing.T) {
	t.Parallel()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	f := newSweepFixture(
		t,
		nil,
		[]inventory.BlobPurge{
			{PurgedAt: old, Namespace: "acme", NamespaceID: "ns-acme", Tool: "pulumi", UnitID: "stk-1"},
		},
	)
	f.records.finishErr = fmt.Errorf("finish: %w", namespace.ErrUnavailable)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.FinishedBlobPurges != 0 || res.PendingBlobPurges != 1 || res.Failed != 1 {
		t.Fatalf("sweep = %+v, want one pending record and one failure", res)
	}

	if !strings.Contains(f.log.String(), "error_kind=unavailable") {
		t.Fatalf("log = %q, want the error kind unavailable", f.log.String())
	}
}

// TestSweepUnknownTool proves that a unit or a record of a tool without a
// port counts as a failure, that the record stays pending, and that the
// sweep goes on.
func TestSweepUnknownTool(t *testing.T) {
	t.Parallel()

	units := expiredUnits(1)
	units[0].Tool = "other"
	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	f := newSweepFixture(
		t,
		units,
		[]inventory.BlobPurge{{PurgedAt: old, Namespace: "acme", NamespaceID: "ns-acme", Tool: "other", UnitID: "x"}},
	)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.Failed != 2 || res.PendingBlobPurges != 1 || len(res.Purged) != 0 {
		t.Fatalf("sweep = %+v, want two failures and one pending record", res)
	}
}

// TestSweepRecordWithoutNamespaceHasItsOwnKind proves that a blob purge record
// whose namespace row is gone (an empty namespace ID) never reaches the tool,
// stays pending, counts as a failure, and logs the kind "namespace_missing"
// instead of "store".
func TestSweepRecordWithoutNamespaceHasItsOwnKind(t *testing.T) {
	t.Parallel()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	f := newSweepFixture(
		t,
		nil,
		[]inventory.BlobPurge{{PurgedAt: old, Namespace: "gone", NamespaceID: "", Tool: "pulumi", UnitID: "x"}},
	)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.Failed != 1 || res.PendingBlobPurges != 1 || res.FinishedBlobPurges != 0 {
		t.Fatalf("sweep = %+v, want one failure and one pending record", res)
	}

	if len(f.tool.deleted) != 0 {
		t.Fatalf("the tool deleted %v, want no call", f.tool.deleted)
	}

	log := f.log.String()
	if !strings.Contains(log, "error_kind=namespace_missing") || strings.Contains(log, "error_kind=store") {
		t.Fatalf("log = %q, want error_kind=namespace_missing", log)
	}
}

// TestNewSweeperRefusesMissingPorts proves that NewSweeper names each
// missing port with ErrMissingDependency.
func TestNewSweeperRefusesMissingPorts(t *testing.T) {
	t.Parallel()

	full := func() inventory.SweeperConfig {
		return inventory.SweeperConfig{
			Expired:    &memExpired{entries: nil, calls: 0},
			BlobPurges: &memRecords{records: nil, finished: nil, finishErr: nil, calls: 0},
			Lock:       newMemLock(),
			Tools:      map[string]inventory.SweepTool{"pulumi": newSweepTool()},
			Logger:     slog.New(slog.DiscardHandler),
			Now:        nil,
		}
	}

	cases := []struct {
		name string
		drop func(*inventory.SweeperConfig)
	}{
		{name: "expired", drop: func(c *inventory.SweeperConfig) { c.Expired = nil }},
		{name: "records", drop: func(c *inventory.SweeperConfig) { c.BlobPurges = nil }},
		{name: "lock", drop: func(c *inventory.SweeperConfig) { c.Lock = nil }},
		{name: "tools", drop: func(c *inventory.SweeperConfig) { c.Tools = nil }},
		{name: "logger", drop: func(c *inventory.SweeperConfig) { c.Logger = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full()
			tc.drop(&cfg)

			if _, err := inventory.NewSweeper(cfg); !errors.Is(err, inventory.ErrMissingDependency) {
				t.Fatalf("new sweeper without %s: err = %v, want ErrMissingDependency", tc.name, err)
			}
		})
	}

	if _, err := inventory.NewSweeper(full()); err != nil {
		t.Fatalf("new sweeper with every port: %v", err)
	}
}

// TestPageExpired proves the reference order of ListExpiredDeleted: the end
// of retention, then the tool, then the unit ID, with the cursor and the
// limit.
func TestPageExpired(t *testing.T) {
	t.Parallel()

	at := sweepNow().Add(-time.Hour)
	entries := []inventory.DeletedEntry{
		{DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "pulumi", UnitID: "b", Name: "web/b"},
		{DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "opentofu", UnitID: "z", Name: "z"},
		{
			DeletedAt:  at,
			PurgeAfter: at.Add(-time.Minute),
			Namespace:  "beta",
			Tool:       "pulumi",
			UnitID:     "c",
			Name:       "web/c",
		},
		{DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "pulumi", UnitID: "a", Name: "web/a"},
		{
			DeletedAt:  at,
			PurgeAfter: sweepNow().Add(time.Second),
			Namespace:  "acme",
			Tool:       "pulumi",
			UnitID:     "later",
			Name:       "x",
		},
		{DeletedAt: at, PurgeAfter: sweepNow(), Namespace: "acme", Tool: "pulumi", UnitID: "now", Name: "y"},
	}

	ids := func(page []inventory.DeletedEntry) string {
		out := make([]string, 0, len(page))
		for _, e := range page {
			out = append(out, e.UnitID)
		}

		return strings.Join(out, ",")
	}

	if got := ids(inventory.PageExpired(entries, sweepNow(), nil, 10)); got != "c,z,a,b,now" {
		t.Fatalf("expired = %s, want c,z,a,b,now", got)
	}

	cursor := &inventory.ExpiredCursor{PurgeAfter: at, Tool: "pulumi", UnitID: "a"}
	if got := ids(inventory.PageExpired(entries, sweepNow(), cursor, 1)); got != "b" {
		t.Fatalf("expired after pulumi/a, limit 1 = %s, want b", got)
	}
}

// TestSweepPurgesWithTheSweepTime proves that each purge carries the time
// at the start of the sweep as its due time, so the tool can refuse a unit
// whose retention a restore and a new delete renewed after the list.
func TestSweepPurgesWithTheSweepTime(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t, expiredUnits(3), nil)

	if _, err := f.sweeper.Sweep(t.Context(), sweepEntry()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(f.tool.dueBys) != 3 {
		t.Fatalf("purges = %d, want 3", len(f.tool.dueBys))
	}

	for _, due := range f.tool.dueBys {
		if !due.Equal(sweepNow()) {
			t.Fatalf("due time = %v, want the sweep time %v", due, sweepNow())
		}
	}
}

// blockingTool is a sweepTool whose first purge waits until release closes.
type blockingTool struct {
	*sweepTool

	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingTool) PurgeExpired(
	ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})

	return b.sweepTool.PurgeExpired(ctx, ns, unitID, dueBy, e)
}

// memLock is an inventory.SweepLocker that stands for the sweep lock of one
// store. Sweepers that share a memLock stand for server processes on one
// store.
type memLock struct {
	held atomic.Bool
}

func newMemLock() *memLock {
	return &memLock{held: atomic.Bool{}}
}

func (m *memLock) TryLockSweep(_ context.Context) (func(), error) {
	if !m.held.CompareAndSwap(false, true) {
		return nil, inventory.ErrSweepRunning
	}

	return func() { m.held.Store(false) }, nil
}

// TestSweepRefusesASecondSweep proves that a sweep of a second server
// process on the same store, which starts while a sweep runs, returns
// ErrSweepRunning at once and does no work, that the first sweep goes on,
// and that a sweep after the first one can run. The test uses no sleep: the
// first sweep blocks in its tool until the second one answers.
func TestSweepRefusesASecondSweep(t *testing.T) {
	t.Parallel()

	lock := newMemLock()
	entries := expiredUnits(2)

	tool := &blockingTool{
		sweepTool: newSweepTool(), started: make(chan struct{}), release: make(chan struct{}), once: sync.Once{},
	}
	other := newSweepTool()

	newSweeper := func(tl inventory.SweepTool) *inventory.Sweeper {
		sweeper, err := inventory.NewSweeper(inventory.SweeperConfig{
			Expired:    &memExpired{entries: entries, calls: 0},
			BlobPurges: &memRecords{records: nil, finished: nil, finishErr: nil, calls: 0},
			Lock:       lock,
			Tools:      map[string]inventory.SweepTool{"pulumi": tl},
			Logger:     slog.New(slog.DiscardHandler),
			Now:        sweepNow,
		})
		if err != nil {
			t.Fatalf("new sweeper: %v", err)
		}

		return sweeper
	}

	first, second := newSweeper(tool), newSweeper(other)
	done := make(chan inventory.SweepResult)

	go func() {
		res, _ := first.Sweep(t.Context(), sweepEntry()) //nolint:errcheck // The test checks the result.
		done <- res
	}()

	<-tool.started

	res, err := second.Sweep(t.Context(), sweepEntry())
	if !errors.Is(err, inventory.ErrSweepRunning) {
		t.Fatalf("second sweep: err = %v, want ErrSweepRunning", err)
	}

	if len(res.Purged) != 0 || len(other.entries) != 0 {
		t.Fatalf("second sweep did work: %d purged, %d tool calls, want none", len(res.Purged), len(other.entries))
	}

	close(tool.release)

	if res := <-done; len(res.Purged) != 2 {
		t.Fatalf("first sweep purged %d units, want 2", len(res.Purged))
	}

	if _, err := second.Sweep(t.Context(), sweepEntry()); err != nil {
		t.Fatalf("sweep after the first one ended: %v", err)
	}
}

// TestSweepStopsWithoutFailures proves that the purges that the end of the
// sweep context cuts off count as skipped, with no warning and no failure.
func TestSweepStopsWithoutFailures(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	f := newSweepFixture(t, expiredUnits(5), nil)
	f.tool.errs["stk-001"] = context.Canceled

	stopping := &cancelTool{sweepTool: f.tool, cancel: cancel, at: "stk-001"}

	sweeper, err := inventory.NewSweeper(inventory.SweeperConfig{
		Expired:    f.expired,
		BlobPurges: f.records,
		Lock:       newMemLock(),
		Tools:      map[string]inventory.SweepTool{"pulumi": stopping},
		Logger:     slog.New(slog.NewTextHandler(f.log, nil)),
		Now:        sweepNow,
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}

	res, err := sweeper.Sweep(ctx, sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Purged) != 1 || res.Skipped != 4 || res.Failed != 0 {
		t.Fatalf("stopped sweep = %d purged, %d skipped, %d failed, want 1, 4 and 0",
			len(res.Purged), res.Skipped, res.Failed)
	}

	if strings.Contains(f.log.String(), "WARN") {
		t.Fatalf("log = %q, want no warning for a stopped sweep", f.log.String())
	}
}

// cancelTool cancels the sweep context when it purges the unit at, as a
// server that stops does.
type cancelTool struct {
	*sweepTool

	cancel context.CancelFunc
	at     string
}

func (c *cancelTool) PurgeExpired(
	ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	if unitID == c.at {
		c.cancel()
	}

	return c.sweepTool.PurgeExpired(ctx, ns, unitID, dueBy, e)
}

// TestSweepPagesTheRecords proves that the sweep reads the blob purge
// records page by page, so records whose delete keeps failing do not hide
// the newer ones.
func TestSweepPagesTheRecords(t *testing.T) {
	t.Parallel()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Hour)
	records := make([]inventory.BlobPurge, 0, 250)

	for i := range 250 {
		records = append(records, inventory.BlobPurge{
			PurgedAt: old.Add(
				time.Duration(i) * time.Second,
			),
			Namespace:   "acme",
			NamespaceID: "ns-acme",
			Tool:        "pulumi",
			UnitID:      fmt.Sprintf("stk-%03d", i),
		})
	}

	f := newSweepFixture(t, nil, records)
	for i := range 150 {
		f.tool.blobErrs[fmt.Sprintf("stk-%03d", i)] = errDriver
	}

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.FinishedBlobPurges != 100 || res.PendingBlobPurges != 150 || res.Failed != 150 || f.records.calls != 3 {
		t.Fatalf("sweep = %d finished, %d pending, %d failed in %d reads, want 100, 150, 150 in 3",
			res.FinishedBlobPurges, res.PendingBlobPurges, res.Failed, f.records.calls)
	}
}

// TestSweepCountDue proves that CountDue counts every unit whose retention
// period ended, across pages, and no unit whose retention runs.
func TestSweepCountDue(t *testing.T) {
	t.Parallel()

	n := inventory.SweepPageSize + 3
	f := newSweepFixture(t, expiredUnits(n), nil)

	due, err := f.sweeper.CountDue(t.Context())
	if err != nil || due != n {
		t.Fatalf("count due = %d, %v, want %d", due, err, n)
	}

	if len(f.tool.entries) != 0 {
		t.Fatal("CountDue purged a unit")
	}
}
