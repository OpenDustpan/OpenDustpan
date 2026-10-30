package dustpan

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// openScheduleChainStore opens a SQLite store for the chain tests with
// instances, and closes it at the end of the test.
func openScheduleChainStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// newInstanceChain returns the chain of one instance on st, with the metrics
// of that instance.
func newInstanceChain(t *testing.T, st *sqlite.Store) *auditChain {
	t.Helper()

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.auditChain = audit.ChainOn

	observed := metrics.NewServerMetrics("test", time.Now())

	chain, err := newAuditChain(cfg, observed)
	if err != nil || chain == nil {
		t.Fatalf("new audit chain = %v, %v", chain, err)
	}

	return chain
}

// errBlobList is the failure of failingListBlobs.
var errBlobList = errors.New("blob list fails")

// failingListBlobs is a blob store whose List fails. It counts the calls of
// List, so a test can prove that it read the epochs.
type failingListBlobs struct {
	blob.Store

	lists *atomic.Int64
}

func (b failingListBlobs) list(context.Context, string) ([]blob.Object, error) {
	b.lists.Add(1)

	return nil, errBlobList
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (b failingListBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := b.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

// TestAuditChainGaugeKeepsTheMarksWhenTheEpochCannotBeRead proves that a
// scrape that cannot read the current checkpoint epoch keeps the alarm: the
// gauge counts the marks of the newest epoch, and a stored result of 0 does
// not clear it.
func TestAuditChainGaugeKeepsTheMarksWhenTheEpochCannotBeRead(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)
	chain := newInstanceChain(t, st)

	mark := audit.CheckMark{At: time.Time{}, Scope: "acme", Sequence: 3, Epoch: 1}
	if err := st.SaveCheckMark(t.Context(), mark); err != nil {
		t.Fatalf("save mark: %v", err)
	}

	lists := &atomic.Int64{}
	chain.checkpoints = audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs:   failingListBlobs{Store: openTestBlobs(t), lists: lists},
		Entries: st,
		Signer:  newTestCheckpointSigner(t),
		Marks:   st,
		Now:     nil,
	})

	chain.storeResult(t.Context(), 0)

	if got := metricLine(t, chain); !strings.Contains(got, "dustpan_audit_chain_invalid 1") {
		t.Fatalf("metrics with an epoch that cannot be read:\n%s\nwant one invalid scope: the mark stands", got)
	}

	if lists.Load() == 0 {
		t.Fatal("the scrape did not list the epochs, so the test did not reach the failed read")
	}
}

// TestJobSchedulesRunAuditChainCheckOncePerTick proves that two instances on
// one store run the audit chain check once per tick: no two operations share
// a tick key, and the stored result carries the time of a check. The
// intervals are short for the test; the production interval stays one hour.
func TestJobSchedulesRunAuditChainCheckOncePerTick(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)

	a := testServer(st.DurableOperations(), "a")
	a.chain, a.auditChainEvery = newInstanceChain(t, st), scheduleTestEvery

	b := testServer(st.DurableOperations(), "b")
	b.chain, b.auditChainEvery = newInstanceChain(t, st), scheduleTestEvery

	aPair, bPair := newPair(t, a), newPair(t, b)

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestDeadline)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	waitForTwoRuns(t, a, kindAuditChain)
	cancel()
	wg.Wait()

	checkTickKeysOnce(t, a, kindAuditChain)

	state, err := st.CheckState(t.Context())
	if err != nil {
		t.Fatalf("check state: %v", err)
	}

	if state.CheckedAt.IsZero() || state.Invalid != 0 || len(state.Marks) != 0 {
		t.Fatalf("stored state after the checks = %+v, want a check time, no invalid scope and no mark", state)
	}
}

// TestJobSchedulesListAuditChainCheckOnlyWithAChain proves that a server
// without the audit chain has no schedule for it.
func TestJobSchedulesListAuditChainCheckOnlyWithAChain(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)

	without := testServer(st.DurableOperations(), "a")
	with := testServer(st.DurableOperations(), "b")
	with.chain = newInstanceChain(t, st)

	tests := []struct {
		srv  *Server
		name string
		want bool
	}{
		{name: "a server without a chain", srv: without, want: false},
		{name: "a server with a chain", srv: with, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := hasAuditChainSchedule(t, tt.srv); got != tt.want {
				t.Fatalf("schedule %s listed = %t, want %t", auditChainScheduleName, got, tt.want)
			}
		})
	}
}

// hasAuditChainSchedule reports whether srv lists the audit chain schedule
// with the default interval.
func hasAuditChainSchedule(t *testing.T, srv *Server) bool {
	t.Helper()

	for _, spec := range srv.jobSchedules() {
		if spec.Name != auditChainScheduleName {
			continue
		}

		if spec.Interval != auditChainCheckInterval {
			t.Fatalf("interval = %v, want %v", spec.Interval, auditChainCheckInterval)
		}

		return true
	}

	return false
}

// errCheckStore is the failure of failingChecks.
var errCheckStore = errors.New("check store fails")

// failingChecks is an audit.CheckStore whose every call fails.
type failingChecks struct{}

func (failingChecks) CheckState(context.Context) (audit.CheckState, error) {
	return audit.CheckState{CheckedAt: time.Time{}, Marks: nil, Invalid: 0}, errCheckStore
}

func (failingChecks) SaveCheckResult(context.Context, int) error { return errCheckStore }

func (failingChecks) SaveCheckMark(context.Context, audit.CheckMark) error { return errCheckStore }

// TestAuditChainGaugeReadsTheStoredResult proves that every instance exports
// the stored result, not its own: instance A stores a result and instance B
// exports it. A result that cannot be read exports as one invalid scope.
func TestAuditChainGaugeReadsTheStoredResult(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)
	a, b := newInstanceChain(t, st), newInstanceChain(t, st)

	tests := []struct {
		name string
		// store is false when no check stored a result yet.
		store   bool
		invalid int
	}{
		{name: "no check ran", store: false, invalid: 0},
		{name: "instance A found 2 scopes", store: true, invalid: 2},
		{name: "a later check found none", store: true, invalid: 0},
	}

	for _, tt := range tests {
		if tt.store {
			a.storeResult(t.Context(), tt.invalid)
		}

		want := "dustpan_audit_chain_invalid " + strconv.Itoa(tt.invalid)

		for name, chain := range map[string]*auditChain{"A": a, "B": b} {
			if got := metricLine(t, chain); !strings.Contains(got, want) {
				t.Fatalf("%s: instance %s metrics:\n%s\nwant %q", tt.name, name, got, want)
			}
		}
	}

	b.checks = failingChecks{}

	if got := metricLine(t, b); !strings.Contains(got, "dustpan_audit_chain_invalid 1") {
		t.Fatalf("metrics with a store that cannot be read:\n%s\nwant one invalid scope: a failed read denies", got)
	}
}

// TestAuditChainGaugeKeepsTheStandingMarks proves that an older check that
// ends late cannot clear the alarm: the stored result is last-writer-wins, so
// the gauge exports the larger of the stored count and the count of marks in
// the newest epoch. A mark of an older epoch does not count.
func TestAuditChainGaugeKeepsTheStandingMarks(t *testing.T) {
	t.Parallel()

	st := openScheduleChainStore(t)
	a, b := newInstanceChain(t, st), newInstanceChain(t, st)

	for _, m := range []audit.CheckMark{
		{At: time.Time{}, Scope: "acme", Sequence: 3, Epoch: 2},
		{At: time.Time{}, Scope: "beta", Sequence: 4, Epoch: 2},
		{At: time.Time{}, Scope: "gamma", Sequence: 5, Epoch: 1},
	} {
		if err := st.SaveCheckMark(t.Context(), m); err != nil {
			t.Fatalf("save mark of %s: %v", m.Scope, err)
		}
	}

	tests := []struct {
		name    string
		invalid int
		want    int
	}{
		{name: "a newer check found 3 scopes", invalid: 3, want: 3},
		{name: "an older check ends late with none", invalid: 0, want: 2},
		{name: "a result below the marks", invalid: 1, want: 2},
	}

	for _, tt := range tests {
		a.storeResult(t.Context(), tt.invalid)

		want := "dustpan_audit_chain_invalid " + strconv.Itoa(tt.want)

		for name, chain := range map[string]*auditChain{"A": a, "B": b} {
			if got := metricLine(t, chain); !strings.Contains(got, want) {
				t.Fatalf("%s: instance %s metrics:\n%s\nwant %q", tt.name, name, got, want)
			}
		}
	}
}

// TestAuditChainReadErrorsAreCountedAndLimited proves that a scrape that
// cannot read the stored result exports 1, adds one to
// dustpan_audit_chain_read_errors_total, and logs at most one warning each
// minute.
func TestAuditChainReadErrorsAreCountedAndLimited(t *testing.T) {
	t.Parallel()

	chain := newInstanceChain(t, openScheduleChainStore(t))

	var logs bytes.Buffer

	at := time.Unix(1_700_000_000, 0)
	chain.logger = slog.New(slog.NewTextHandler(&logs, nil))
	chain.now = func() time.Time { return at }
	chain.checks = failingChecks{}

	for range 3 {
		metricLine(t, chain)
	}

	got := metricLine(t, chain)

	for _, want := range []string{"dustpan_audit_chain_invalid 1", "dustpan_audit_chain_read_errors_total 4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("metrics after 4 failed reads:\n%s\nwant %q", got, want)
		}
	}

	if n := strings.Count(logs.String(), "read the stored result"); n != 1 {
		t.Fatalf("warnings within one minute = %d, want 1:\n%s", n, logs.String())
	}

	at = at.Add(2 * auditChainReadWarnEvery)

	metricLine(t, chain)

	if n := strings.Count(logs.String(), "read the stored result"); n != 2 {
		t.Fatalf("warnings after two minutes = %d, want 2:\n%s", n, logs.String())
	}
}

// countingEntries counts the chain checks of the entries that it wraps, and
// signals on first when the first check runs.
type countingEntries struct {
	audit.Store

	checks *atomic.Int64
	first  chan struct{}
	once   *sync.Once
}

func (c countingEntries) Check(ctx context.Context, ns namespace.Name) (audit.CheckResult, error) {
	c.checks.Add(1)
	c.once.Do(func() { close(c.first) })

	return c.Store.Check(ctx, ns)
}

// TestAuditChainChecksOnceAtStartWithADurableStore proves that an instance
// with a durable store checks once at start and runs no loop of its own: the
// schedule runs the later checks. Without a durable store the loop of the
// instance keeps checking.
func TestAuditChainChecksOnceAtStartWithADurableStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup   func(*Server)
		name    string
		durable bool
	}{
		{name: "with a durable store", durable: true, setup: func(*Server) {}},
		{name: "without a durable store", durable: false, setup: func(s *Server) { s.durable = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := countStartChecks(t, tt.setup)

			if tt.durable && got != 1 {
				t.Fatalf("chain checks with a durable store = %d, want 1", got)
			}

			if !tt.durable && got < 2 {
				t.Fatalf("chain checks without a durable store = %d, want 2 or more", got)
			}
		})
	}
}

// countStartChecks starts the chain checks of one server with a check
// interval of 20 ms, after setup changed it, lets ten intervals pass after the first check, and
// answers the count of chain checks.
func countStartChecks(t *testing.T, setup func(*Server)) int64 {
	t.Helper()

	st := openScheduleChainStore(t)
	chain := newInstanceChain(t, st)
	runs, first := &atomic.Int64{}, make(chan struct{})
	chain.entries = countingEntries{Store: chain.entries, checks: runs, first: first, once: &sync.Once{}}

	srv := testServer(st.DurableOperations(), "a")
	srv.chain, srv.auditChainEvery = chain, 20*time.Millisecond

	setup(srv)

	ctx, cancel := context.WithCancel(t.Context())
	wait := srv.startChainChecks(ctx)

	select {
	case <-first:
	case <-time.After(scheduleTestDeadline):
		t.Fatal("no chain check after the start")
	}

	// A loop of this instance would check again within these intervals.
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	wait()

	return runs.Load()
}
