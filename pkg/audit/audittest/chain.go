package audittest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ChainStore is every port the chain cases exercise: the audit store, the
// server scope, the chain state and the check of many scopes.
type ChainStore interface {
	Store
	audit.ServerStore
	audit.ChainStore
	audit.ScopesChecker
}

// ChainTestStore is one fresh, empty store with the tamper hook of
// TestStore, so a case can change an entry behind the back of the store.
type ChainTestStore struct {
	Store       ChainStore
	TamperEntry func(t *testing.T, ns namespace.Name, seq int64, field, value string)
}

// ChainFactory returns a fresh, empty store for one chain subtest.
type ChainFactory func(t *testing.T) ChainTestStore

// RunChain executes the conformance cases of audit.ChainStore and of the
// chain check against stores produced by newStore: the chain from the first
// entry, its end, a new start with the digest of the earlier entries, and
// the changes that the check must find.
func RunChain(t *testing.T, newStore ChainFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s ChainTestStore)
		name string
	}{
		{name: "a new store keeps the chain on from the first entry", test: testChainDefaultOn},
		{name: "an end entry closes the chain and later entries hold no hash", test: testChainEnd},
		{name: "a start entry holds the digest of the earlier entries", test: testChainStartDigest},
		{name: "a changed entry before a start entry is found", test: testChainChangedBeforeStart},
		{name: "a removed hash inside the chain is found", test: testChainStrippedInside},
		{name: "a renamed end entry is found", test: testChainEndRenamed},
		{name: "a scope that began after the chain went off checks as valid", test: testChainScopeAfterOff},
		{name: "a scope without a chain that began while it was on is found", test: testChainScopeBeforeOff},
		{name: "a chain that ends while the state is on is found", test: testChainEndedWhileOn},
		{name: "a chain that goes on while the state is off is found", test: testChainOpenWhileOff},
		{name: "a row below sequence 1 is found", test: testChainRowBelowOne},
		{name: "the log agrees with the state after each migration", test: testChainLogAgrees},
		{name: "a second migration to the same state writes no entry", test: testChainMigrateTwice},
		{name: "concurrent end entries write one entry", test: testChainConcurrentEnds},
		{name: "each namespace entry holds its failover version under the hash", test: testChainFenceVersion},
		{name: "a stale fence refuses a start entry", test: testChainStaleFence},
		{name: "the chain record round-trips", test: testChainRecordRoundTrip},
		{name: "a check of each scope answers the result of check", test: testChainCheckEach},
		{name: "concurrent appends keep the chain", test: testChainConcurrentAppends},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// chainStep is the time between two events of a case.
const chainStep = 10 * time.Second

// timeline writes the events of one case at fixed times, one chainStep
// apart.
type timeline struct {
	at  time.Time
	t   *testing.T
	s   ChainStore
	ns  namespace.Namespace
	req int
}

// newTimeline creates the namespace acme in s and starts the clock.
func newTimeline(t *testing.T, s ChainStore) *timeline {
	t.Helper()

	return &timeline{at: now(), t: t, s: s, ns: newNamespace(t, s), req: 0}
}

// tick advances the clock by one step and answers the new time.
func (tl *timeline) tick() time.Time {
	tl.at = tl.at.Add(chainStep)

	return tl.at
}

// append appends n entries to acme, one step apart.
func (tl *timeline) append(n int) []audit.Entry {
	tl.t.Helper()

	out := make([]audit.Entry, 0, n)

	for range n {
		tl.req++
		e := newEntry(tl.ns.Name, "admin", "req-"+strconv.Itoa(tl.req), tl.tick())
		out = append(out, appendEntry(tl.t, tl.s, tl.ns.Fence(), e))
	}

	return out
}

// migrate runs the migration of a change to state in acme and in the server
// scope, by alice.
func (tl *timeline) migrate(state audit.ChainState) {
	tl.t.Helper()

	fences := []namespace.Fence{tl.ns.Fence(), audit.ServerFence()}
	change := audit.ChainChange{At: tl.tick(), By: "alice", To: state}

	if err := audit.MigrateChain(tl.t.Context(), tl.s, fences, change); err != nil {
		tl.t.Fatalf("migrate the chain %s: %v", state, err)
	}
}

// strip removes the hash and the previous hash of the entries of ns at the
// sequences seqs.
func strip(t *testing.T, ts ChainTestStore, ns namespace.Name, seqs ...int64) {
	t.Helper()

	for _, seq := range seqs {
		ts.TamperEntry(t, ns, seq, "hash", "")
		ts.TamperEntry(t, ns, seq, "prev_hash", "")
	}
}

// check runs Check on ns and fails the test on error.
func check(t *testing.T, s ChainStore, ns namespace.Name) audit.CheckResult {
	t.Helper()

	got, err := s.Check(t.Context(), ns)
	if err != nil {
		t.Fatalf("check %s: %v", ns, err)
	}

	return got
}

// wantValid fails the test unless got is valid.
func wantValid(t *testing.T, got audit.CheckResult) {
	t.Helper()

	if !got.Valid {
		t.Fatalf("check: got %+v, want valid", got)
	}
}

// wantInvalidAt fails the test unless got is invalid at seq.
func wantInvalidAt(t *testing.T, got audit.CheckResult, seq int64) {
	t.Helper()

	if got.Valid || got.BadSequence != seq {
		t.Fatalf("check: got %+v, want invalid at sequence %d", got, seq)
	}
}

// listScope answers every entry of ns, oldest first.
func listScope(t *testing.T, s ChainStore, ns namespace.Name) []audit.Entry {
	t.Helper()

	listing, err := s.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list %s: %v", ns, err)
	}

	out := make([]audit.Entry, len(listing.Entries))
	for i := range listing.Entries {
		out[len(out)-1-i] = listing.Entries[i]
	}

	return out
}

func testChainDefaultOn(t *testing.T, ts ChainTestStore) {
	t.Helper()

	rec, err := ts.Store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	if rec.State != audit.ChainStateOn || rec.Pending || rec.LogReplaced {
		t.Fatalf("chain record of a new store: got %+v, want on", rec)
	}

	tl := newTimeline(t, ts.Store)
	entries := tl.append(2)

	if entries[0].Hash == "" || entries[0].PrevHash != "" || entries[1].PrevHash != entries[0].Hash {
		t.Fatalf("entries of a new store: got %+v, want a chain from the first entry", entries)
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
}

func testChainEnd(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)
	tl.migrate(audit.ChainStateOff)
	later := tl.append(2)

	got := listScope(t, ts.Store, tl.ns.Name)
	end := got[2]

	if end.Action != audit.ActionEndChain || end.Subject != "alice" || end.Hash == "" || end.PrevHash != got[1].Hash {
		t.Fatalf("end entry: got %+v, want a chained end entry by alice linked to sequence 2", end)
	}

	if later[0].Hash != "" || later[1].Hash != "" {
		t.Fatalf("entries after the end: got %+v, want no hash", later)
	}

	rec, err := ts.Store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	if rec.State != audit.ChainStateOff || rec.ChangedBy != "alice" || rec.ChangedAt.IsZero() {
		t.Fatalf("chain record after the end: got %+v, want off by alice", rec)
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
	wantValid(t, check(t, ts.Store, audit.ServerScope))
}

func testChainStartDigest(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)
	tl.migrate(audit.ChainStateOff)
	tl.append(2)
	tl.migrate(audit.ChainStateOn)
	tl.append(2)

	got := listScope(t, ts.Store, tl.ns.Name)

	var digest audit.Digest
	for i := range 5 {
		digest.Add(got[i], audit.EncodeChanges(got[i].Changes))
	}

	start := got[5]
	if start.Action != audit.ActionStartChain || start.PrevHash != "" || start.Hash == "" ||
		audit.EncodeChanges(start.Changes) != audit.EncodeChanges(audit.StartChanges(&digest)) {
		t.Fatalf("start entry: got %+v, want the digest %s of 5 entries", start, digest.Sum())
	}

	if got[6].PrevHash != start.Hash {
		t.Fatalf("entry after the start: got %+v, want a link to the start entry", got[6])
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
	wantValid(t, check(t, ts.Store, audit.ServerScope))
}

func testChainChangedBeforeStart(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)
	tl.migrate(audit.ChainStateOff)
	tl.append(1)
	tl.migrate(audit.ChainStateOn)

	// Entry 3 holds no hash, so only the digest of the start entry at
	// sequence 4 protects it.
	ts.TamperEntry(t, tl.ns.Name, 3, "subject", "mallory")

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 4)
}

func testChainStrippedInside(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(3)
	strip(t, ts, tl.ns.Name, 2)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 2)
}

func testChainEndRenamed(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)
	tl.migrate(audit.ChainStateOff)
	tl.append(1)

	// Without its action, the end entry no longer ends the chain, so the
	// entry after it must hold a hash. Its hash fails first.
	ts.TamperEntry(t, tl.ns.Name, 2, "action", "unit:decrypt")

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 2)
}

func testChainScopeAfterOff(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)

	// The server scope holds an entry, acme none: the migration ends the
	// chain of the server scope only.
	if _, err := ts.Store.AppendServer(t.Context(), newServerEntry("admin", "req-0")); err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	tl.migrate(audit.ChainStateOff)
	tl.append(2)

	wantValid(t, check(t, ts.Store, tl.ns.Name))
}

func testChainScopeBeforeOff(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.migrate(audit.ChainStateOff)
	tl.append(2)

	// Someone moves the first entry of acme to a time before the chain went
	// off: then acme existed while the chain was on, and needs a chain.
	ts.TamperEntry(t, tl.ns.Name, 1, "at_ms", strconv.FormatInt(now().Add(-time.Hour).UnixMilli(), 10))

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainEndedWhileOn(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)
	tl.migrate(audit.ChainStateOff)

	// Someone writes the state on without the start entries of a migration.
	rec := audit.ChainRecord{
		ChangedAt:   tl.tick(),
		ChangedBy:   "mallory",
		State:       audit.ChainStateOn,
		Pending:     false,
		LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 2)
}

func testChainOpenWhileOff(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)

	// Someone writes the state off without the end entries of a migration.
	rec := audit.ChainRecord{
		ChangedAt:   tl.tick(),
		ChangedBy:   "mallory",
		State:       audit.ChainStateOff,
		Pending:     false,
		LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 2)
}

func testChainRowBelowOne(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)
	ts.TamperEntry(t, tl.ns.Name, 1, "sequence", "0")

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 0)
}

func testChainLogAgrees(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)

	scopes := []namespace.Name{tl.ns.Name, audit.ServerScope}

	agrees := func(state audit.ChainState) bool {
		t.Helper()

		ok, err := audit.LogAgrees(t.Context(), ts.Store, scopes, state)
		if err != nil {
			t.Fatalf("log agrees: %v", err)
		}

		return ok
	}

	if !agrees(audit.ChainStateOn) || agrees(audit.ChainStateOff) {
		t.Fatal("a chained log: want agreement with on only")
	}

	tl.migrate(audit.ChainStateOff)

	if agrees(audit.ChainStateOn) || !agrees(audit.ChainStateOff) {
		t.Fatal("a log after an end entry: want agreement with off only")
	}

	tl.append(1)

	if agrees(audit.ChainStateOn) || !agrees(audit.ChainStateOff) {
		t.Fatal("a log with an entry without a hash: want agreement with off only")
	}
}

func testChainMigrateTwice(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)

	e := newEntry(tl.ns.Name, "alice", "req-start", tl.tick())
	e.Action = audit.ActionStartChain

	wrote, err := ts.Store.StartChain(t.Context(), tl.ns.Fence(), e)
	if err != nil || wrote {
		t.Fatalf("start entry in a chained scope: wrote %v, %v; want no entry", wrote, err)
	}

	tl.migrate(audit.ChainStateOff)
	tl.migrate(audit.ChainStateOff)

	if got := listScope(t, ts.Store, tl.ns.Name); len(got) != 3 {
		t.Fatalf("entries after two migrations off: got %d, want 3", len(got))
	}

	tl.migrate(audit.ChainStateOn)
	tl.migrate(audit.ChainStateOn)

	if got := listScope(t, ts.Store, tl.ns.Name); len(got) != 4 {
		t.Fatalf("entries after two migrations on: got %d, want 4", len(got))
	}

	if got := check(t, ts.Store, tl.ns.Name); !got.Valid {
		t.Fatalf("check after the migrations: got %+v, want valid", got)
	}

	empty := audit.ServerFence()

	end := newEntry(audit.ServerScope, "alice", "req-end", tl.tick())
	end.Action = audit.ActionEndChain

	if wrote, err := ts.Store.EndChain(t.Context(), empty, end); err != nil || wrote {
		t.Fatalf("end entry in an empty scope: wrote %v, %v; want no entry", wrote, err)
	}
}

func testChainConcurrentEnds(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)

	const workers = 4

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wrote int
	)

	for i := range workers {
		e := newEntry(tl.ns.Name, "alice", "req-end-"+strconv.Itoa(i), tl.tick())
		e.Action = audit.ActionEndChain

		wg.Go(func() {
			ok, err := ts.Store.EndChain(t.Context(), tl.ns.Fence(), e)

			mu.Lock()
			defer mu.Unlock()

			if err == nil && ok {
				wrote++
			}
		})
	}

	wg.Wait()

	if wrote != 1 {
		t.Fatalf("end entries written: got %d, want 1", wrote)
	}

	if got := listScope(t, ts.Store, tl.ns.Name); len(got) != 2 {
		t.Fatalf("entries: got %d, want 2", len(got))
	}
}

// fenceBumper increments the failover version of every namespace, as a
// forced failover or a restore does. Both SQL stores implement it.
type fenceBumper interface {
	BumpFailoverVersions(ctx context.Context) (int64, error)
}

func testChainFenceVersion(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)

	if first := tl.append(1)[0]; first.FenceVersion != 1 {
		t.Fatalf("entry at failover version 1: fence version %d, want 1", first.FenceVersion)
	}

	server, err := ts.Store.AppendServer(t.Context(), newServerEntry("admin", "req-server"))
	if err != nil || server.FenceVersion != 0 {
		t.Fatalf("server entry = fence version %d, %v; want 0", server.FenceVersion, err)
	}

	bumper, ok := ts.Store.(fenceBumper)
	if !ok {
		t.Skip("the store cannot increment failover versions")
	}

	if _, err := bumper.BumpFailoverVersions(t.Context()); err != nil {
		t.Fatalf("increment failover versions: %v", err)
	}

	tl.ns.FailoverVersion++

	if second := tl.append(1)[0]; second.FenceVersion != 2 {
		t.Fatalf("entry after a failover: fence version %d, want 2", second.FenceVersion)
	}

	got := listScope(t, ts.Store, tl.ns.Name)
	if len(got) != 2 || got[0].FenceVersion != 1 || got[1].FenceVersion != 2 {
		t.Fatalf("listed fence versions: got %+v, want 1 and 2", got)
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))

	ts.TamperEntry(t, tl.ns.Name, 1, "fence_version", "3")

	if res := check(t, ts.Store, tl.ns.Name); res.Valid || res.BadSequence != 1 {
		t.Fatalf("check after a changed fence version: got %+v, want invalid at 1", res)
	}
}

func testChainStaleFence(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)

	stale := tl.ns.Fence()
	stale.FailoverVersion--

	e := newEntry(tl.ns.Name, "alice", "req-start", tl.tick())
	e.Action = audit.ActionStartChain

	if _, err := ts.Store.StartChain(t.Context(), stale, e); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("start with a stale fence: got %v, want ErrFenced", err)
	}
}

func testChainRecordRoundTrip(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	at := now().Truncate(time.Millisecond)

	for _, want := range []audit.ChainRecord{
		{ChangedAt: at, ChangedBy: "alice", State: audit.ChainStateOff, Pending: false, LogReplaced: false},
		{ChangedAt: at.Add(time.Second), ChangedBy: "bob", State: audit.ChainStateOn, Pending: true, LogReplaced: false},
		{ChangedAt: at.Add(2 * time.Second), ChangedBy: "bob", State: audit.ChainStateOn, Pending: false, LogReplaced: true},
	} {
		if err := s.SaveChainRecord(t.Context(), want); err != nil {
			t.Fatalf("save chain record %+v: %v", want, err)
		}

		got, err := s.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("chain record: %v", err)
		}

		if !got.ChangedAt.Equal(want.ChangedAt) || got.ChangedBy != want.ChangedBy || got.State != want.State ||
			got.Pending != want.Pending || got.LogReplaced != want.LogReplaced {
			t.Fatalf("chain record: got %+v, want %+v", got, want)
		}
	}
}

func testChainCheckEach(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)
	strip(t, ts, tl.ns.Name, 2)

	scopes := []namespace.Name{audit.ServerScope, tl.ns.Name}

	got, err := ts.Store.CheckEach(t.Context(), scopes)
	if err != nil {
		t.Fatalf("check each: %v", err)
	}

	if len(got) != len(scopes) {
		t.Fatalf("check each: got %d results, want %d", len(got), len(scopes))
	}

	for i, scope := range scopes {
		if want := check(t, ts.Store, scope); got[i] != want {
			t.Fatalf("check each %s: got %+v, want %+v", scope, got[i], want)
		}
	}
}

// Workers and appends of each worker of testChainConcurrentAppends.
const (
	concurrentWorkers = 8
	concurrentAppends = 25
)

func testChainConcurrentAppends(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	tl := newTimeline(t, s)
	base := tl.tick()

	// Each request takes its time before its transaction, so the times
	// arrive out of the order of the commits. Six workers append to acme
	// and two to the server scope.
	var wg sync.WaitGroup

	for w := range concurrentWorkers {
		wg.Go(func() {
			for i := range concurrentAppends {
				at := base.Add(time.Duration((i*37+w*101)%500) * time.Millisecond)
				if err := concurrentAppend(t, s, tl.ns, w, at); err != nil {
					t.Errorf("worker %d append %d: %v", w, i, err)

					return
				}
			}
		})
	}

	wg.Wait()

	for _, scope := range []namespace.Name{tl.ns.Name, audit.ServerScope} {
		wantValid(t, check(t, s, scope))
	}
}

// concurrentAppend appends one entry with the time at for worker w: to the
// server scope for the last two workers, else to ns.
func concurrentAppend(t *testing.T, s ChainStore, ns namespace.Namespace, w int, at time.Time) error {
	t.Helper()

	if w < concurrentWorkers-2 {
		_, err := s.Append(t.Context(), ns.Fence(), newEntry(ns.Name, "admin", "req-"+strconv.Itoa(w), at))

		return err
	}

	e := newEntry(audit.ServerScope, "admin", "req-"+strconv.Itoa(w), at)
	e.Action, e.Object = "server:update-settings", audit.ServerScope.String()

	_, err := s.AppendServer(t.Context(), e)

	return err
}
