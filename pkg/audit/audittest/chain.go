package audittest

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
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

// ChainApprover is the test-only approval port the approval cases use: it
// appends an approval entry and starts its migration operation in one
// transaction.
type ChainApprover interface {
	// ApproveChainChange appends e and starts fixed, or the operation of
	// retryKey when fixed names an ended operation, in one transaction. It
	// returns audit.ErrNoChainChange when no change waits,
	// audit.ErrChainMigrating while a migration can run, and
	// durable.ErrKeyConflict when the fixed key names an operation of
	// another kind.
	ApproveChainChange(
		ctx context.Context, e audit.Entry, fixed durable.StartRequest, retryKey string,
	) (durable.Operation, error)
}

// ChainBreaker breaks audit appends for the rollback case.
type ChainBreaker interface {
	// BreakAuditAppends breaks every later audit append. It returns an error
	// when the break hook fails.
	BreakAuditAppends(ctx context.Context) error
}

// ChainOperations answers the durable operations for the approval cases.
type ChainOperations interface {
	// DurableOperationsPort answers the durable operations of the store.
	DurableOperationsPort() durable.Store
}

// ChainTestStore is one fresh, empty store with the tamper hook of
// TestStore, so a case can change an entry behind the back of the store.
// Reopen answers a second handle on the same database. It is nil for an
// adapter that allows one handle per database: the case that needs a fresh
// handle then skips. DeleteChainState deletes the row of the chain state
// behind the back of the store, as a corrupt database lacks it.
type ChainTestStore struct {
	Store            ChainStore
	TamperEntry      func(t *testing.T, ns namespace.Name, seq int64, field, value string)
	Reopen           func(t *testing.T) ChainStore
	DeleteChainState func(t *testing.T)
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
		{name: "a missing chain state row is ErrChainStateMissing", test: testChainStateMissing},
		{name: "an end entry closes the chain and later entries hold no hash", test: testChainEnd},
		{name: "a start entry holds the digest of the earlier entries", test: testChainStartDigest},
		{name: "a changed entry before a start entry is found", test: testChainChangedBeforeStart},
		{
			name: "a changed hashed entry before a start entry fails at its sequence",
			test: testChainChangedHashedBeforeStart,
		},
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
		{
			name: "two appends between the end entry and the stored state write no hash",
			test: testChainAppendAfterEndTwo,
		},
		{
			name: "three appends between the end entry and the stored state write no hash",
			test: testChainAppendAfterEndThree,
		},
		{
			name: "an append between the stored state and the start entry holds no hash",
			test: testChainAppendAfterStateBeforeStart,
		},
		{
			name: "an append after a failed migration to on stays unhashed until the start entry",
			test: testChainAppendAfterFailedMigration,
		},
		{name: "a second handle follows the stored state", test: testChainSecondHandle},
		{name: "one approval writes one approval entry and one operation", test: testChainApproveOne},
		{name: "a conflicting start key writes no entry", test: testChainApproveConflictingKey},
		{name: "no pending change answers its sentinel error", test: testChainApproveNoChange},
		{name: "a failed audit append leaves no entry and no operation", test: testChainApproveRollback},
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

func testChainStateMissing(t *testing.T, ts ChainTestStore) {
	t.Helper()

	ts.DeleteChainState(t)

	if _, err := ts.Store.ChainRecord(t.Context()); !errors.Is(err, audit.ErrChainStateMissing) {
		t.Fatalf("chain record without a row: err = %v, want ErrChainStateMissing", err)
	}
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

func testChainChangedHashedBeforeStart(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(2)
	tl.migrate(audit.ChainStateOff)
	tl.migrate(audit.ChainStateOn)

	// Entry 1 holds a hash, so the check finds the changed field at
	// sequence 1, before it compares the digest.
	ts.TamperEntry(t, tl.ns.Name, 1, "subject", "mallory")

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
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
		ChangedAt:          tl.tick(),
		ChangedBy:          "mallory",
		State:              audit.ChainStateOn,
		Pending:            false,
		EnteredMaintenance: false,
		MigratingAt:        time.Time{},
		LogReplaced:        false,
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
		ChangedAt:          tl.tick(),
		ChangedBy:          "mallory",
		State:              audit.ChainStateOff,
		Pending:            false,
		EnteredMaintenance: false,
		MigratingAt:        time.Time{},
		LogReplaced:        false,
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

	// A chained scope gets a start entry too: only a start entry as the
	// last entry makes the marker done. A second start entry is not
	// written.
	for i, want := range []bool{true, false} {
		e := newEntry(tl.ns.Name, "alice", "req-start-"+strconv.Itoa(i), tl.tick())
		e.Action = audit.ActionStartChain

		wrote, err := ts.Store.StartChain(t.Context(), tl.ns.Fence(), e)
		if err != nil || wrote != want {
			t.Fatalf("start entry %d in a chained scope: wrote %v, %v; want %v", i, wrote, err, want)
		}
	}

	tl.migrate(audit.ChainStateOff)
	tl.migrate(audit.ChainStateOff)

	if got := listScope(t, ts.Store, tl.ns.Name); len(got) != 4 {
		t.Fatalf("entries after two migrations off: got %d, want 4", len(got))
	}

	tl.migrate(audit.ChainStateOn)
	tl.migrate(audit.ChainStateOn)

	if got := listScope(t, ts.Store, tl.ns.Name); len(got) != 5 {
		t.Fatalf("entries after two migrations on: got %d, want 5", len(got))
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
		{
			ChangedAt: at, ChangedBy: "alice", State: audit.ChainStateOff, Pending: false,
			EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
		},
		{
			ChangedAt: at.Add(time.Second), ChangedBy: "bob", State: audit.ChainStateOn, Pending: true,
			EnteredMaintenance: true, MigratingAt: at.Add(time.Second), LogReplaced: false,
		},
		{
			ChangedAt: at.Add(2 * time.Second), ChangedBy: "bob", State: audit.ChainStateOn, Pending: false,
			EnteredMaintenance: true, MigratingAt: time.Time{}, LogReplaced: true,
		},
		{
			ChangedAt: at.Add(3 * time.Second), ChangedBy: "bob", State: audit.ChainStateOn, Pending: false,
			EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: true,
		},
	} {
		if err := s.SaveChainRecord(t.Context(), want); err != nil {
			t.Fatalf("save chain record %+v: %v", want, err)
		}

		got, err := s.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("chain record: %v", err)
		}

		if !got.ChangedAt.Equal(want.ChangedAt) || got.ChangedBy != want.ChangedBy || got.State != want.State ||
			got.Pending != want.Pending || got.EnteredMaintenance != want.EnteredMaintenance ||
			!got.MigratingAt.Equal(want.MigratingAt) || got.LogReplaced != want.LogReplaced {
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

// testChainAppendAfterEndTwo checks two appends after an end entry while
// the stored state is still on.
func testChainAppendAfterEndTwo(t *testing.T, ts ChainTestStore) {
	t.Helper()

	testChainAppendAfterEnd(t, ts, 2)
}

// testChainAppendAfterEndThree checks three appends after an end entry
// while the stored state is still on.
func testChainAppendAfterEndThree(t *testing.T, ts ChainTestStore) {
	t.Helper()

	testChainAppendAfterEnd(t, ts, 3)
}

// testChainAppendAfterEnd appends n entries after an end entry while the
// stored state is still on: none holds a hash. Storing off then checks as
// valid.
func testChainAppendAfterEnd(t *testing.T, ts ChainTestStore, n int) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.append(1)

	end := newEntry(tl.ns.Name, "alice", "req-end", tl.tick())
	end.Action = audit.ActionEndChain

	wrote, err := ts.Store.EndChain(t.Context(), tl.ns.Fence(), end)
	if err != nil || !wrote {
		t.Fatalf("end chain: wrote %v, %v; want one entry", wrote, err)
	}

	later := tl.append(n)
	for i := range later {
		if later[i].Hash != "" || later[i].PrevHash != "" {
			t.Fatalf("append %d after the end entry with the state still on: got %+v, want no hash", i, later[i])
		}
	}

	rec := audit.ChainRecord{
		ChangedAt: tl.tick(), ChangedBy: "alice", State: audit.ChainStateOff, Pending: false,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
}

func testChainAppendAfterStateBeforeStart(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.migrate(audit.ChainStateOff)
	tl.append(1)

	turning := audit.ChainRecord{
		ChangedAt: tl.tick(), ChangedBy: "alice", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), turning); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	// The scope acme ends with an entry without a hash. An append after the
	// stored state turns on, and before the start entry, holds no hash; the
	// start entry still follows, with a digest that covers it.
	waiting := tl.append(1)[0]
	if waiting.Hash != "" || waiting.PrevHash != "" {
		t.Fatalf("append after the stored state with no start entry yet: got %+v, want no hash", waiting)
	}

	start := newEntry(tl.ns.Name, "alice", "req-start", tl.tick())
	start.Action = audit.ActionStartChain

	wrote, err := ts.Store.StartChain(t.Context(), tl.ns.Fence(), start)
	if err != nil || !wrote {
		t.Fatalf("start chain: wrote %v, %v; want one entry", wrote, err)
	}

	done := audit.ChainRecord{
		ChangedAt: tl.tick(), ChangedBy: "alice", State: audit.ChainStateOn, Pending: false,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), done); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
}

func testChainAppendAfterFailedMigration(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.migrate(audit.ChainStateOff)
	tl.append(1)

	// The migration to on stores the state first, then writes the start
	// entries. It stops after the store here, so no start entry follows.
	turning := audit.ChainRecord{
		ChangedAt: tl.tick(), ChangedBy: "alice", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), turning); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	waiting := tl.append(1)[0]
	if waiting.Hash != "" || waiting.PrevHash != "" {
		t.Fatalf("append after a failed migration to on: got %+v, want no hash", waiting)
	}

	agrees, err := audit.LogAgrees(t.Context(), ts.Store, []namespace.Name{tl.ns.Name}, audit.ChainStateOn)
	if err != nil {
		t.Fatalf("log agrees: %v", err)
	}

	if agrees {
		t.Fatal("log after a failed migration to on agrees with on, want no agreement")
	}

	start := newEntry(tl.ns.Name, "alice", "req-start", tl.tick())
	start.Action = audit.ActionStartChain

	wrote, err := ts.Store.StartChain(t.Context(), tl.ns.Fence(), start)
	if err != nil || !wrote {
		t.Fatalf("start chain: wrote %v, %v; want one entry", wrote, err)
	}

	done := audit.ChainRecord{
		ChangedAt: tl.tick(), ChangedBy: "alice", State: audit.ChainStateOn, Pending: false,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	}
	if err := ts.Store.SaveChainRecord(t.Context(), done); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
}

func testChainSecondHandle(t *testing.T, ts ChainTestStore) {
	t.Helper()

	if ts.Reopen == nil {
		t.Skip("the adapter allows one handle per database, so no fresh handle can read the chain state")
	}

	tl := newTimeline(t, ts.Store)
	tl.append(1)

	other := ts.Reopen(t)

	tl.migrate(audit.ChainStateOff)

	off := newEntry(tl.ns.Name, "admin", "req-h2-off", tl.tick())

	storedOff, err := other.Append(t.Context(), tl.ns.Fence(), off)
	if err != nil {
		t.Fatalf("append through a second handle while off: %v", err)
	}

	if storedOff.Hash != "" {
		t.Fatalf("append through a second handle while off: hash %q, want none", storedOff.Hash)
	}

	tl.migrate(audit.ChainStateOn)

	on := newEntry(tl.ns.Name, "admin", "req-h2-on", tl.tick())

	storedOn, err := other.Append(t.Context(), tl.ns.Fence(), on)
	if err != nil {
		t.Fatalf("append through a second handle while on: %v", err)
	}

	if storedOn.Hash == "" {
		t.Fatal("append through a second handle while on: no hash, want a hash")
	}

	wantValid(t, check(t, ts.Store, tl.ns.Name))
}

// approvalKind names the durable operation of a chain approval.
const approvalKind = durable.Kind("audit-chain-migration")

// approvalKey is the fixed start key of a chain approval.
const approvalKey = "audit-chain-migration"

// approvalVersion is the version of the migration operation.
const approvalVersion = 1

// approvalRetryKey is the start key of a retry after an ended operation.
const approvalRetryKey = "audit-chain-migration-retry"

// approvalPorts answers the approval, break and durable ports of s. It fails
// the test when the store misses one: every adapter runs the approval cases.
//
//nolint:ireturn // The approval cases need the three test ports together.
func approvalPorts(t *testing.T, s ChainStore) (ChainApprover, ChainBreaker, durable.Store) {
	t.Helper()

	approver, ok := s.(ChainApprover)
	if !ok {
		t.Fatal("the store does not approve chain changes")
	}

	breaker, ok := s.(ChainBreaker)
	if !ok {
		t.Fatal("the store does not break audit appends")
	}

	holder, ok := s.(ChainOperations)
	if !ok {
		t.Fatal("the store keeps no durable operations")
	}

	return approver, breaker, holder.DurableOperationsPort()
}

// saveApprovalWait stores a waiting change to off.
func saveApprovalWait(t *testing.T, s ChainStore) {
	t.Helper()

	err := s.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	})
	if err != nil {
		t.Fatalf("save chain record: %v", err)
	}
}

// approvalCaller answers the caller entry of the approval cases.
func approvalCaller() audit.Entry {
	return audit.Entry{
		At: now(), Namespace: audit.ServerScope, Subject: "alice", IDPSubject: "", TokenID: "tok-1",
		Action: audit.ActionSetChain, Object: audit.ServerScope.String(), ObjectID: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "req-1", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0,
	}
}

// approvalFixed answers the start request of change under the fixed key.
func approvalFixed(t *testing.T, change audit.ChainChange) durable.StartRequest {
	t.Helper()

	input, err := json.Marshal(change)
	if err != nil {
		t.Fatalf("encode change: %v", err)
	}

	return durable.StartRequest{Key: approvalKey, Kind: approvalKind, Version: approvalVersion, Input: input}
}

// approvalEntryCount answers the count of approval entries in the server
// scope: entries with the action that records an approved chain change.
func approvalEntryCount(t *testing.T, s ChainStore) int {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	var count int

	for i := range listing.Entries {
		if listing.Entries[i].Action == audit.ActionSetChain {
			count++
		}
	}

	return count
}

// approvalOperationCount answers the count of migration operations.
func approvalOperationCount(t *testing.T, ops durable.Store) int {
	t.Helper()

	found, err := ops.List(t.Context(), durable.Filter{Kind: approvalKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	return len(found)
}

// testChainApproveOne proves that one approval of a waiting change writes one
// approval entry and starts one operation.
func testChainApproveOne(t *testing.T, ts ChainTestStore) {
	t.Helper()

	approver, _, ops := approvalPorts(t, ts.Store)
	saveApprovalWait(t, ts.Store)

	change := audit.ChainChange{At: now(), By: "alice", To: audit.ChainStateOff}
	entry := audit.ChainSetEntry(approvalCaller(), change)
	fixed := approvalFixed(t, change)

	_, err := approver.ApproveChainChange(t.Context(), entry, fixed, approvalRetryKey)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if got := approvalEntryCount(t, ts.Store); got != 1 {
		t.Fatalf("approval entries = %d, want 1", got)
	}

	if got := approvalOperationCount(t, ops); got != 1 {
		t.Fatalf("migration operations = %d, want 1", got)
	}
}

// testChainApproveConflictingKey proves that an approval whose fixed key names
// an operation of another kind writes no entry and starts no operation.
func testChainApproveConflictingKey(t *testing.T, ts ChainTestStore) {
	t.Helper()

	approver, _, ops := approvalPorts(t, ts.Store)
	saveApprovalWait(t, ts.Store)

	conflict := durable.StartRequest{Key: approvalKey, Kind: "other-kind", Version: 1, Input: nil}

	_, _, err := ops.Start(t.Context(), conflict)
	if err != nil {
		t.Fatalf("start conflicting operation: %v", err)
	}

	change := audit.ChainChange{At: now(), By: "alice", To: audit.ChainStateOff}
	entry := audit.ChainSetEntry(approvalCaller(), change)
	fixed := approvalFixed(t, change)

	_, err = approver.ApproveChainChange(t.Context(), entry, fixed, approvalRetryKey)
	if !errors.Is(err, durable.ErrKeyConflict) {
		t.Fatalf("approve with a conflicting start key: %v, want ErrKeyConflict", err)
	}

	if got := approvalEntryCount(t, ts.Store); got != 0 {
		t.Fatalf("approval entries after a failed start = %d, want none", got)
	}

	if got := approvalOperationCount(t, ops); got != 0 {
		t.Fatalf("migration operations after a failed start = %d, want none", got)
	}
}

// testChainApproveNoChange proves that an approval with no waiting change
// answers the sentinel error and writes nothing.
func testChainApproveNoChange(t *testing.T, ts ChainTestStore) {
	t.Helper()

	approver, _, ops := approvalPorts(t, ts.Store)

	change := audit.ChainChange{At: now(), By: "alice", To: audit.ChainStateOff}
	entry := audit.ChainSetEntry(approvalCaller(), change)
	fixed := approvalFixed(t, change)

	_, err := approver.ApproveChainChange(t.Context(), entry, fixed, approvalRetryKey)
	if !errors.Is(err, audit.ErrNoChainChange) {
		t.Fatalf("approve with nothing waiting: %v, want ErrNoChainChange", err)
	}

	if got := approvalEntryCount(t, ts.Store); got != 0 {
		t.Fatalf("approval entries with nothing waiting = %d, want none", got)
	}

	if got := approvalOperationCount(t, ops); got != 0 {
		t.Fatalf("migration operations with nothing waiting = %d, want none", got)
	}
}

// testChainApproveRollback proves that when the durable start succeeds but
// the audit append fails, neither the entry nor the operation exists: the
// transaction rolled back.
func testChainApproveRollback(t *testing.T, ts ChainTestStore) {
	t.Helper()

	approver, breaker, ops := approvalPorts(t, ts.Store)
	saveApprovalWait(t, ts.Store)

	if err := breaker.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	change := audit.ChainChange{At: now(), By: "alice", To: audit.ChainStateOff}
	entry := audit.ChainSetEntry(approvalCaller(), change)
	fixed := approvalFixed(t, change)

	_, err := approver.ApproveChainChange(t.Context(), entry, fixed, approvalRetryKey)
	if err == nil {
		t.Fatal("approve with a broken audit append succeeded, want an error")
	}

	if got := approvalEntryCount(t, ts.Store); got != 0 {
		t.Fatalf("approval entries after a broken append = %d, want none", got)
	}

	if got := approvalOperationCount(t, ops); got != 0 {
		t.Fatalf("migration operations after a broken append = %d, want none", got)
	}
}
