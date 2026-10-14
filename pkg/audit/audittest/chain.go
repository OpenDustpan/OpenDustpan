package audittest

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ChainStore is every port the chain cases exercise: the audit store, the
// server scope and the chain switch under test.
type ChainStore interface {
	Store
	audit.ServerStore
	audit.ChainSwitch
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

// RunChain executes the conformance cases of audit.ChainSwitch and of the
// chain check across runs of chained and unchained entries (ADR 60
// decision 17, ADR 64) against stores produced by newStore.
func RunChain(t *testing.T, newStore ChainFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s ChainTestStore)
		name string
	}{
		{name: "with the chain off an entry holds no hash", test: testChainOff},
		{name: "a chained run after an unchained run checks as valid", test: testChainRuns},
		{name: "a changed field in a later chained run is found", test: testChainLaterRunTamper},
		{name: "hashes removed from a chain that was never off are found", test: testChainStrippedAlwaysOn},
		{name: "hashes removed after the chain went on again are found", test: testChainStrippedAfterOn},
		{name: "a chain turned off without a switch entry is found", test: testChainOffWithoutEntry},
		{name: "an unchained entry within the switch lag checks as valid", test: testChainSwitchLag},
		{name: "a forged switch to off without a hash is found", test: testChainForgedOff},
		{name: "a forged switch to off at the tail with an early time is found", test: testChainForgedOffAtTail},
		{name: "the time of an entry moved back after its hash is gone is found", test: testChainTimeMovedBack},
		{name: "a chain record without switch entries is found", test: testChainSwitchesRemoved},
		{name: "a forged switch to off at sequence 0 is found", test: testChainForgedOffAtZero},
		{name: "a forged switch to off at sequence -1 with an early time is found", test: testChainForgedOffBelowZero},
		{name: "a switch to on renamed and stripped is found", test: testChainSwitchRenamed},
		{name: "a switch to on with another action and stripped is found", test: testChainSwitchActionChanged},
		{name: "a start on rewritten to off without a hash is found", test: testChainStartRewritten},
		{name: "a second start entry is skipped", test: testChainSecondStartSkipped},
		{name: "a check of each scope answers the result of check", test: testChainCheckEach},
		{name: "chained entries are found in any scope", test: testHasChainedEntries},
		{name: "the chain record round-trips", test: testChainRecordRoundTrip},
		{name: "a refused switch entry records nothing", test: testRecordChainIsAtomic},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// chainStep is the time between two events of a timeline: longer than
// audit.ChainSwitchLag, so the lag never hides a case.
const chainStep = 10 * time.Second

// timeline writes the events of one case at fixed times, one chainStep
// apart, so the switch lag of the check is under the control of the case.
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
		e := newEntry(tl.ns.Name, "admin", fmt.Sprintf("req-%d", tl.req), tl.tick())
		out = append(out, appendEntry(tl.t, tl.s, tl.ns.Fence(), e))
	}

	return out
}

// start records the state of a new installation as the server does at its
// first start: a switch entry for the state, with a hash only when on.
func (tl *timeline) start(on bool) {
	tl.t.Helper()

	tl.record(switchSpec{state: stateOf(on), chained: on, ever: on})
}

// turn records a change of the chain as the server does: a switch entry
// with a hash, and a record that the log was ever chained.
func (tl *timeline) turn(on bool) {
	tl.t.Helper()

	tl.record(switchSpec{state: stateOf(on), chained: true, ever: true})
}

// switchSpec is one switch of a timeline: the new state, whether the
// switch entry holds a hash, and whether the log was ever chained.
type switchSpec struct {
	state   audit.ChainState
	chained bool
	ever    bool
}

// record appends the switch entry of spec and records its state.
func (tl *timeline) record(spec switchSpec) {
	tl.t.Helper()

	entry := audit.ChainEntry{Entry: switchEntry(spec.state, tl.tick()), Chained: spec.chained, Start: false}
	rec := audit.ChainRecord{State: spec.state, EverChained: spec.ever}

	if err := tl.s.RecordChain(tl.t.Context(), rec, []audit.ChainEntry{entry}); err != nil {
		tl.t.Fatalf("record chain %s: %v", spec.state, err)
	}
}

// stateOf answers the state of a chain that is on or off.
func stateOf(on bool) audit.ChainState {
	return map[bool]audit.ChainState{true: audit.ChainStateOn, false: audit.ChainStateOff}[on]
}

// switchEntry returns the switch entry of ServerScope for state at at.
func switchEntry(state audit.ChainState, at time.Time) audit.Entry {
	e := newEntry(audit.ServerScope, "opendustpan serve", "", at)
	e.Kind = audit.KindChange
	e.Action = audit.ActionSetChain
	e.Object = audit.ServerScope.String()
	e.ValuePath = "/audit/chain/" + string(state)

	return e
}

// strip removes the hash and the previous hash of the entries of acme at
// the sequences seqs.
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

// wantInvalidAt fails the test unless got is invalid at seq.
func wantInvalidAt(t *testing.T, got audit.CheckResult, seq int64) {
	t.Helper()

	if got.Valid || got.BadSequence != seq {
		t.Fatalf("check: got %+v, want invalid at sequence %d", got, seq)
	}
}

func testChainOff(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(false)

	run := tl.append(2)
	for i := range run {
		if run[i].Hash != "" || run[i].PrevHash != "" {
			t.Fatalf("entry %d with the chain off: hash %q, previous hash %q, want both empty",
				run[i].Sequence, run[i].Hash, run[i].PrevHash)
		}
	}

	got := check(t, ts.Store, tl.ns.Name)
	if !got.Valid || got.Unchained != 2 || got.Checked != 2 {
		t.Fatalf("check: got %+v, want valid with 2 checked and 2 unchained", got)
	}

	if server := check(t, ts.Store, audit.ServerScope); !server.Valid {
		t.Fatalf("check of the server scope: got %+v, want valid", server)
	}
}

func testChainRuns(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.append(2)
	tl.turn(false)
	tl.append(2)
	tl.turn(true)

	run := tl.append(2)
	if run[0].PrevHash != "" || run[0].Hash == "" {
		t.Fatalf("first entry of the new run: previous hash %q, hash %q, want an empty link and a hash",
			run[0].PrevHash, run[0].Hash)
	}

	if run[1].PrevHash != run[0].Hash {
		t.Fatal("second entry of the new run does not link to the first")
	}

	got := check(t, ts.Store, tl.ns.Name)
	if !got.Valid || got.Unchained != 2 || got.Checked != 6 {
		t.Fatalf("check: got %+v, want valid with 6 checked and 2 unchained", got)
	}
}

func testChainLaterRunTamper(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(false)
	tl.append(1)
	tl.turn(true)
	tl.append(2)

	ts.TamperEntry(t, tl.ns.Name, 3, "subject", "mallory")

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 3)
}

func testChainStrippedAlwaysOn(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.append(3)

	// Someone removes every hash of a scope whose chain was never off.
	strip(t, ts, tl.ns.Name, 1, 2, 3)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainStrippedAfterOn(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.append(1)
	tl.turn(false)
	tl.append(1)
	tl.turn(true)
	tl.append(2)

	// Someone removes the hashes after the chain went on again.
	strip(t, ts, tl.ns.Name, 3, 4)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 3)
}

func testChainOffWithoutEntry(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.append(2)

	// A handle that turns the chain off with no switch entry.
	ts.Store.SetChain(false)
	tl.append(1)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 3)
}

func testChainSwitchLag(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(false)
	tl.append(1)
	tl.turn(true)

	// Another instance still holds the state from before the switch to on,
	// and appends within the lag.
	ts.Store.SetChain(false)

	late := newEntry(tl.ns.Name, "admin", "req-late", tl.at.Add(audit.ChainSwitchLag/2))
	appendEntry(t, ts.Store, tl.ns.Fence(), late)

	if got := check(t, ts.Store, tl.ns.Name); !got.Valid || got.Unchained != 2 {
		t.Fatalf("check with an entry within the lag: got %+v, want valid with 2 unchained", got)
	}

	// After the lag, an entry without a hash does not match.
	after := newEntry(tl.ns.Name, "admin", "req-after", tl.at.Add(2*audit.ChainSwitchLag))
	appendEntry(t, ts.Store, tl.ns.Fence(), after)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 3)
}

// forgeOff appends a switch entry to off without a hash at at, as a person
// who can write to the database but does not compute hashes. The record
// stays on with a chained log.
func (tl *timeline) forgeOff(at time.Time) {
	tl.t.Helper()

	entry := audit.ChainEntry{Entry: switchEntry(audit.ChainStateOff, at), Chained: false, Start: false}
	rec := audit.ChainRecord{State: audit.ChainStateOn, EverChained: true}

	if err := tl.s.RecordChain(tl.t.Context(), rec, []audit.ChainEntry{entry}); err != nil {
		tl.t.Fatalf("forge a switch to off: %v", err)
	}
}

func testChainForgedOff(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.forgeOff(tl.tick())
	tl.append(3)

	// Someone adds a switch to off without a hash as the next entry of
	// @server, then removes every hash of acme.
	strip(t, ts, tl.ns.Name, 1, 2, 3)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainForgedOffAtTail(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	early := tl.at.Add(time.Millisecond)
	tl.append(3)

	// The forged switch to off is the last entry of @server, with a time
	// before every entry of acme.
	tl.forgeOff(early)
	strip(t, ts, tl.ns.Name, 1, 2, 3)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainTimeMovedBack(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(false)
	off := tl.append(1)
	tl.turn(true)
	tl.append(2)

	// Someone removes the hash of entry 3 and moves its time into the off
	// window, before entry 2.
	strip(t, ts, tl.ns.Name, 3)
	ts.TamperEntry(t, tl.ns.Name, 3, "at_ms", strconv.FormatInt(off[0].At.Add(time.Second).UnixMilli(), 10))

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 3)
}

func testChainSwitchesRemoved(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(false)
	tl.append(2)

	// Someone hides the only switch entry. The installation still holds
	// its chain record, so the check must not fall back to the rule for a
	// store without a timeline.
	ts.TamperEntry(t, audit.ServerScope, 1, "action", "server:other")

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

// forgeBelowOne appends a forged switch to off without a hash at at, then
// moves it to the sequence seq of @server, below 1, where the walk of the
// check never reads it.
func forgeBelowOne(t *testing.T, ts ChainTestStore, tl *timeline, at time.Time, seq int64) {
	t.Helper()

	tl.forgeOff(at)

	last, err := ts.Store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 1})
	if err != nil || len(last.Entries) != 1 {
		t.Fatalf("read the last entry of @server: %v", err)
	}

	ts.TamperEntry(t, audit.ServerScope, last.Entries[0].Sequence, "sequence", strconv.FormatInt(seq, 10))
}

func testChainForgedOffAtZero(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.append(3)

	// The forged row sorts first and claims the chain went off long ago,
	// with a time in the year 2100 that covers nothing in order.
	forgeBelowOne(t, ts, tl, time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC), 0)
	strip(t, ts, tl.ns.Name, 1, 2, 3)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
	wantInvalidAt(t, check(t, ts.Store, audit.ServerScope), 0)
}

func testChainForgedOffBelowZero(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	early := tl.at.Add(-time.Hour)
	tl.append(3)

	forgeBelowOne(t, ts, tl, early, -1)
	strip(t, ts, tl.ns.Name, 1, 2, 3)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
	wantInvalidAt(t, check(t, ts.Store, audit.ServerScope), -1)
}

// onAfterOffStart writes the timeline of a new installation that started
// with the chain off and turned it on, with two chained entries in acme
// after the switch.
func onAfterOffStart(t *testing.T, ts ChainTestStore) *timeline {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(false)
	tl.turn(true)
	tl.append(2)

	return tl
}

func testChainSwitchRenamed(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := onAfterOffStart(t, ts)

	// Someone renames the switch to on to a value path that names no state
	// and removes its hash, then removes every hash of acme.
	ts.TamperEntry(t, audit.ServerScope, 2, "value_path", "/audit/chain/On")
	strip(t, ts, audit.ServerScope, 2)
	strip(t, ts, tl.ns.Name, 1, 2)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainSwitchActionChanged(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := onAfterOffStart(t, ts)

	// The switch to on gets another action, so it is no switch entry.
	ts.TamperEntry(t, audit.ServerScope, 2, "action", "server:other")
	strip(t, ts, audit.ServerScope, 2)
	strip(t, ts, tl.ns.Name, 1, 2)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainStartRewritten(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
	tl.append(2)

	// Someone rewrites the start on as the unhashed start off of a new
	// installation, then removes every hash of acme. The record still says
	// on and chained.
	ts.TamperEntry(t, audit.ServerScope, 1, "value_path", "/audit/chain/off")
	strip(t, ts, audit.ServerScope, 1)
	strip(t, ts, tl.ns.Name, 1, 2)

	wantInvalidAt(t, check(t, ts.Store, tl.ns.Name), 1)
}

func testChainSecondStartSkipped(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	rec := audit.ChainRecord{State: audit.ChainStateOff, EverChained: false}

	// Two instances read no record at the same time, and both write the
	// start entry of a new installation.
	for range 2 {
		start := audit.ChainEntry{Entry: switchEntry(audit.ChainStateOff, now()), Chained: false, Start: true}
		if err := s.RecordChain(t.Context(), rec, []audit.ChainEntry{start}); err != nil {
			t.Fatalf("record start: %v", err)
		}
	}

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list @server: %v", err)
	}

	if listing.Total != 1 {
		t.Fatalf("@server holds %d entries, want 1 start entry", listing.Total)
	}

	if got := check(t, s, audit.ServerScope); !got.Valid {
		t.Fatalf("check @server: got %+v, want valid", got)
	}
}

func testChainCheckEach(t *testing.T, ts ChainTestStore) {
	t.Helper()

	tl := newTimeline(t, ts.Store)
	tl.start(true)
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

func testHasChainedEntries(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store
	has := func() bool {
		t.Helper()

		found, err := s.HasChainedEntries(t.Context())
		if err != nil {
			t.Fatalf("has chained entries: %v", err)
		}

		return found
	}

	if has() {
		t.Fatal("an empty store reports chained entries")
	}

	tl := newTimeline(t, s)
	tl.start(false)
	tl.append(1)

	if has() {
		t.Fatal("a store with only unchained entries reports chained entries")
	}

	tl.turn(true)

	if !has() {
		t.Fatal("a chained switch entry in the server scope is not found")
	}
}

func testChainRecordRoundTrip(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store

	read := func() audit.ChainRecord {
		t.Helper()

		got, err := s.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("chain record: %v", err)
		}

		return got
	}

	if got := read(); got.State != audit.ChainUnknown || got.EverChained {
		t.Fatalf("chain record of a new store: got %+v, want no record", got)
	}

	for _, want := range []audit.ChainRecord{
		{State: audit.ChainStateOff, EverChained: false},
		{State: audit.ChainStateOn, EverChained: true},
		{State: audit.ChainStateOff, EverChained: true},
	} {
		if err := s.RecordChain(t.Context(), want, nil); err != nil {
			t.Fatalf("record chain %+v: %v", want, err)
		}

		if got := read(); got != want {
			t.Fatalf("chain record: got %+v, want %+v", got, want)
		}
	}
}

func testRecordChainIsAtomic(t *testing.T, ts ChainTestStore) {
	t.Helper()

	s := ts.Store

	good := switchEntry(audit.ChainStateOff, now())
	bad := good
	bad.Namespace = "acme"

	err := s.RecordChain(t.Context(), audit.ChainRecord{State: audit.ChainStateOff, EverChained: false},
		[]audit.ChainEntry{{Entry: good, Chained: false, Start: false}, {Entry: bad, Chained: false, Start: false}})
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("record chain with an entry of another scope: got %v, want ErrInvalidEntry", err)
	}

	rec, err := s.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	if rec.State != audit.ChainUnknown {
		t.Fatalf("chain record after a refused switch: got %+v, want no record", rec)
	}

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 1})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	if listing.Total != 0 {
		t.Fatalf("server entries after a refused switch: got %d, want 0", listing.Total)
	}
}
