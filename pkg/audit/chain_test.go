package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestParseChainSetting(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]audit.ChainSetting{"": audit.ChainOn, "on": audit.ChainOn, "off": audit.ChainOff} {
		got, err := audit.ParseChainSetting(raw)
		if err != nil || got != want {
			t.Fatalf("parse %q: got %q, %v, want %q", raw, got, err, want)
		}
	}

	for _, raw := range []string{"auto", "yes", "ON"} {
		if _, err := audit.ParseChainSetting(raw); !errors.Is(err, audit.ErrInvalidChainSetting) {
			t.Fatalf("parse %q: got %v, want ErrInvalidChainSetting", raw, err)
		}
	}
}

func TestChainSetting(t *testing.T) {
	t.Parallel()

	var zero audit.ChainSetting

	if zero != audit.ChainOn || zero.String() != "on" || zero.State() != audit.ChainStateOn {
		t.Fatalf("zero setting: got %q, state %q, want on", zero, zero.State())
	}

	if audit.ChainOff.String() != "off" || audit.ChainOff.State() != audit.ChainStateOff {
		t.Fatalf("off: got %q, state %q", audit.ChainOff, audit.ChainOff.State())
	}

	if got := audit.ChainSetting(7).String(); got != "7" {
		t.Fatalf("unknown setting: got %q, want 7", got)
	}
}

func TestDigest(t *testing.T) {
	t.Parallel()

	var empty audit.Digest
	if empty.Sum() != "" || empty.Count() != 0 {
		t.Fatalf("empty digest: got %q, %d", empty.Sum(), empty.Count())
	}

	one, two := walkEntry(1), walkChained(2, "")

	var a, b audit.Digest

	a.Add(one, "")
	a.Add(two, "")
	b.Add(one, "")
	b.Add(two, "")

	if a.Sum() == "" || a.Sum() != b.Sum() || a.Count() != 2 {
		t.Fatalf("digest: got %q and %q, count %d, want one value of 2 entries", a.Sum(), b.Sum(), a.Count())
	}

	// The digest covers the stored hash columns as well as the fields.
	stripped := two
	stripped.Hash = ""

	var c audit.Digest

	c.Add(one, "")
	c.Add(stripped, "")

	if c.Sum() == a.Sum() {
		t.Fatal("digest: a removed hash gives the same digest")
	}

	changed := one
	changed.Subject = "mallory"

	var d audit.Digest

	d.Add(changed, "")
	d.Add(two, "")

	if d.Sum() == a.Sum() {
		t.Fatal("digest: a changed field gives the same digest")
	}
}

// walkStep is the time between two entries of the walk tests.
const walkStep = 10 * time.Second

// walkAt answers the time of sequence seq.
func walkAt(seq int64) time.Time {
	base := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)

	return base.Add(time.Duration(seq) * walkStep)
}

// walkEntry returns entry seq of acme without a hash.
func walkEntry(seq int64) audit.Entry {
	return audit.Entry{
		At:           walkAt(seq),
		Namespace:    "acme",
		Subject:      "alice",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "unit:read-secrets",
		Object:       "opentofu:acme/prod",
		ObjectID:     "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "test",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindReveal,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req",
		PrevHash:     "",
		Hash:         "",
		Sequence:     seq,
		FenceVersion: 0,
	}
}

// walkChained returns entry seq linked to prev, with its hash.
func walkChained(seq int64, prev string) audit.Entry {
	e := walkEntry(seq)
	e.PrevHash = prev
	e.Hash = audit.ComputeHash(e, prev)

	return e
}

// walkMarker returns entry seq with action, linked to prev and hashed.
func walkMarker(seq int64, action, prev string, changes []audit.Change) audit.Entry {
	e := walkEntry(seq)
	e.Action, e.Changes, e.PrevHash = action, changes, prev
	e.Hash = audit.ComputeHash(e, prev)

	return e
}

// walkResult runs a walk over entries and answers the sequence of the first
// entry that fails, 0 when every entry passes, or -1 when Finish fails.
func walkResult(scope audit.ChainScope, entries ...audit.Entry) int64 {
	w := audit.NewChainWalk(scope)

	for i := range entries {
		if !w.Step(entries[i]) {
			return entries[i].Sequence
		}
	}

	if !w.Finish() {
		return -1
	}

	return 0
}

// recordOf answers a record with state that changed at walkAt(seq).
func recordOf(state audit.ChainState, seq int64) audit.ChainRecord {
	return audit.ChainRecord{
		ChangedAt:   walkAt(seq),
		ChangedBy:   "alice",
		State:       state,
		Pending:     false,
		MigratingAt: time.Time{},
		LogReplaced: false,
	}
}

// chainedRun returns entries 1 to n of one chain from sequence 1.
func chainedRun(n int64) []audit.Entry {
	out := make([]audit.Entry, 0, n)
	prev := ""

	for seq := int64(1); seq <= n; seq++ {
		e := walkChained(seq, prev)
		out = append(out, e)
		prev = e.Hash
	}

	return out
}

func TestChainWalkFromTheFirstEntry(t *testing.T) {
	t.Parallel()

	on := audit.ChainScope{Record: recordOf(audit.ChainStateOn, 0), Start: 0}
	run := chainedRun(3)

	if got := walkResult(on, run...); got != 0 {
		t.Fatalf("a chain from sequence 1: fails at %d", got)
	}

	bad := []struct {
		name    string
		entries []audit.Entry
		want    int64
	}{
		{name: "a gap", entries: []audit.Entry{run[0], run[2]}, want: 3},
		{name: "a broken link", entries: []audit.Entry{run[0], walkChained(2, "x")}, want: 2},
		{name: "a removed hash", entries: []audit.Entry{run[0], walkEntry(2)}, want: 2},
		{name: "an unchained first entry", entries: []audit.Entry{walkEntry(1)}, want: 1},
	}

	for _, tc := range bad {
		if got := walkResult(on, tc.entries...); got != tc.want {
			t.Fatalf("%s: fails at %d, want %d", tc.name, got, tc.want)
		}
	}

	// The same chain fails the finish when the record says off: an off
	// state needs an end entry.
	off := audit.ChainScope{Record: recordOf(audit.ChainStateOff, 9), Start: 0}
	if got := walkResult(off, run...); got != -1 {
		t.Fatalf("a chain without an end while off: got %d, want a failed finish", got)
	}
}

func TestChainWalkEnd(t *testing.T) {
	t.Parallel()

	run := chainedRun(2)
	end := walkMarker(3, audit.ActionEndChain, run[1].Hash, nil)
	after := walkEntry(4)
	entries := []audit.Entry{run[0], run[1], end, after}

	off := audit.ChainScope{Record: recordOf(audit.ChainStateOff, 3), Start: 0}
	if got := walkResult(off, entries...); got != 0 {
		t.Fatalf("a chain with an end while off: fails at %d", got)
	}

	on := audit.ChainScope{Record: recordOf(audit.ChainStateOn, 3), Start: 0}
	if got := walkResult(on, entries...); got != -1 {
		t.Fatalf("a chain that ends while on: got %d, want a failed finish", got)
	}

	hashedAfter := walkChained(4, end.Hash)
	if got := walkResult(off, run[0], run[1], end, hashedAfter); got != 4 {
		t.Fatalf("a hash after the end: fails at %d, want 4", got)
	}
}

func TestChainWalkStart(t *testing.T) {
	t.Parallel()

	one := walkEntry(1)
	two := walkEntry(2)

	var digest audit.Digest

	digest.Add(one, "")
	digest.Add(two, "")

	start := walkMarker(3, audit.ActionStartChain, "", audit.StartChanges(&digest))
	four := walkChained(4, start.Hash)
	scope := audit.ChainScope{Record: recordOf(audit.ChainStateOn, 3), Start: 3}

	if got := walkResult(scope, one, two, start, four); got != 0 {
		t.Fatalf("a start after two entries: fails at %d", got)
	}

	changed := two
	changed.Subject = "mallory"

	if got := walkResult(scope, one, changed, start, four); got != 3 {
		t.Fatalf("a changed entry before the start: fails at %d, want 3", got)
	}

	linked := walkMarker(3, audit.ActionStartChain, "x", audit.StartChanges(&digest))
	if got := walkResult(scope, one, two, linked); got != 3 {
		t.Fatalf("a start with a previous hash: fails at %d, want 3", got)
	}

	if got := walkResult(scope, one, two); got != -1 {
		t.Fatalf("a scope without its start entry: got %d, want a failed finish", got)
	}
}

func TestChainWalkScopeWithoutAChain(t *testing.T) {
	t.Parallel()

	entries := []audit.Entry{walkEntry(1), walkEntry(2)}

	after := audit.ChainScope{Record: recordOf(audit.ChainStateOff, 0), Start: 0}
	if got := walkResult(after, entries...); got != 0 {
		t.Fatalf("a scope that began after the chain went off: fails at %d", got)
	}

	before := audit.ChainScope{Record: recordOf(audit.ChainStateOff, 5), Start: 0}
	if got := walkResult(before, entries...); got != 1 {
		t.Fatalf("a scope that began while the chain was on: fails at %d, want 1", got)
	}

	never := audit.ChainScope{Record: audit.ChainRecord{
		ChangedAt: time.Time{}, ChangedBy: "", State: audit.ChainStateOff, Pending: false,
		MigratingAt: time.Time{}, LogReplaced: false,
	}, Start: 0}
	if got := walkResult(never, entries...); got != 1 {
		t.Fatalf("a scope without a chain and an off record without a time: fails at %d, want 1", got)
	}
}

func TestChainWalkEmptyScope(t *testing.T) {
	t.Parallel()

	for _, state := range []audit.ChainState{audit.ChainStateOn, audit.ChainStateOff} {
		w := audit.NewChainWalk(audit.ChainScope{Record: recordOf(state, 0), Start: 0})
		if !w.Finish() || w.Unchained() != 0 {
			t.Fatalf("an empty scope with the chain %s: want a finish with no unchained entry", state)
		}
	}
}

// fakeChain is an in-memory audit.ChainStore over the scopes of one test.
type fakeChain struct {
	scopes map[namespace.Name][]audit.Entry
	rec    audit.ChainRecord
	fail   error
}

func newFakeChain() *fakeChain {
	return &fakeChain{
		scopes: map[namespace.Name][]audit.Entry{},
		rec:    recordOf(audit.ChainStateOn, 0),
		fail:   nil,
	}
}

func (f *fakeChain) ChainRecord(context.Context) (audit.ChainRecord, error) { return f.rec, nil }

func (f *fakeChain) SaveChainRecord(_ context.Context, rec audit.ChainRecord) error {
	f.rec = rec

	return nil
}

// ClaimChainMigration claims the waiting change in one check, so tests of
// MigrateChain keep the same errors as the SQL stores.
func (f *fakeChain) ClaimChainMigration(_ context.Context, now time.Time, lease time.Duration) error {
	if !f.rec.Pending {
		return audit.ErrNoChainChange
	}

	if !f.rec.MigratingAt.IsZero() && now.Sub(f.rec.MigratingAt) < lease {
		return audit.ErrChainMigrating
	}

	f.rec.MigratingAt = now

	return nil
}

// ReleaseChainMigration releases the claim of the fake.
func (f *fakeChain) ReleaseChainMigration(context.Context) error {
	f.rec.MigratingAt = time.Time{}

	return nil
}

func (f *fakeChain) StartChain(ctx context.Context, fence namespace.Fence, e audit.Entry) (bool, error) {
	if skip, err := f.agrees(ctx, fence.Namespace, audit.ChainStateOn); skip || err != nil {
		return false, err
	}

	var digest audit.Digest

	entries := f.scopes[fence.Namespace]
	for i := range entries {
		digest.Add(entries[i], audit.EncodeChanges(entries[i].Changes))
	}

	e.Changes = audit.StartChanges(&digest)

	return f.add(fence.Namespace, e, "")
}

func (f *fakeChain) EndChain(ctx context.Context, fence namespace.Fence, e audit.Entry) (bool, error) {
	if skip, err := f.agrees(ctx, fence.Namespace, audit.ChainStateOff); skip || err != nil {
		return false, err
	}

	entries := f.scopes[fence.Namespace]
	if len(entries) == 0 {
		return false, nil
	}

	return f.add(fence.Namespace, e, entries[len(entries)-1].Hash)
}

func (f *fakeChain) LastEntry(_ context.Context, scope namespace.Name) (audit.Entry, bool, error) {
	var none audit.Entry

	if f.fail != nil {
		return none, false, f.fail
	}

	entries := f.scopes[scope]
	if len(entries) == 0 {
		return none, false, nil
	}

	return entries[len(entries)-1], true, nil
}

// agrees reports whether scope needs no marker for state (see MarkerDone).
func (f *fakeChain) agrees(ctx context.Context, scope namespace.Name, state audit.ChainState) (bool, error) {
	last, found, err := f.LastEntry(ctx, scope)
	if err != nil || !found {
		return !found, err
	}

	return audit.MarkerDone(&last, state), nil
}

// add appends e to scope, hashed with prev.
func (f *fakeChain) add(scope namespace.Name, e audit.Entry, prev string) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}

	e.Sequence = int64(len(f.scopes[scope]) + 1)
	e.PrevHash = prev
	e.Hash = audit.ComputeHash(e, prev)
	f.scopes[scope] = append(f.scopes[scope], e)

	return true, nil
}

// put appends e to acme as the stored state of the fake asks: it hashes
// when the state is on and the last entry is not an end entry.
func (f *fakeChain) put(e audit.Entry) {
	const scope = namespace.Name("acme")

	entries := f.scopes[scope]
	e.Namespace = scope
	e.Sequence = int64(len(entries) + 1)
	e.PrevHash, e.Hash = "", ""

	var prevHash string

	var lastAction string

	if len(entries) > 0 {
		prevHash = entries[len(entries)-1].Hash
		lastAction = entries[len(entries)-1].Action
	}

	if f.rec.State == audit.ChainStateOn && lastAction != audit.ActionEndChain {
		e.PrevHash = prevHash
		e.Hash = audit.ComputeHash(e, prevHash)
	}

	f.scopes[scope] = append(f.scopes[scope], e)
}

// walk answers walkResult over every entry of scope against the record.
func (f *fakeChain) walk(scope namespace.Name) int64 {
	entries := f.scopes[scope]

	var start int64

	for i := range entries {
		if entries[i].Action == audit.ActionStartChain {
			start = entries[i].Sequence
		}
	}

	return walkResult(audit.ChainScope{Record: f.rec, Start: start}, entries...)
}

func TestMigrateChain(t *testing.T) {
	t.Parallel()

	f := newFakeChain()
	acme := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	empty := namespace.Fence{Namespace: "empty", FailoverVersion: 1}
	fences := []namespace.Fence{acme, empty, audit.ServerFence()}

	f.put(walkEntry(0))

	off := audit.ChainChange{At: walkAt(5), By: "alice", To: audit.ChainStateOff}
	if err := audit.MigrateChain(t.Context(), f, fences, off); err != nil {
		t.Fatalf("migrate off: %v", err)
	}

	wantEndInAcmeOnly(t, f)

	f.put(walkEntry(0))

	on := audit.ChainChange{At: walkAt(9), By: "bob", To: audit.ChainStateOn}
	if err := audit.MigrateChain(t.Context(), f, fences, on); err != nil {
		t.Fatalf("migrate on: %v", err)
	}

	f.put(walkEntry(0))

	if f.rec.State != audit.ChainStateOn || f.rec.Pending || len(f.scopes["acme"]) != 5 {
		t.Fatalf("after on: record %+v, acme %d entries, want on with 5", f.rec, len(f.scopes["acme"]))
	}

	if got := f.walk("acme"); got != 0 {
		t.Fatalf("acme after off and on: fails at %d", got)
	}
}

// wantEndInAcmeOnly proves that a migration to off by alice wrote an end
// entry in acme only and stored the state off.
func wantEndInAcmeOnly(t *testing.T, f *fakeChain) {
	t.Helper()

	if f.rec.State != audit.ChainStateOff || f.rec.ChangedBy != "alice" || len(f.scopes["acme"]) != 2 ||
		len(f.scopes["empty"]) != 0 || len(f.scopes[audit.ServerScope]) != 0 {
		t.Fatalf("after off: record %+v, scopes %v, want an end entry in acme only", f.rec, f.scopes)
	}
}

func TestMigrateChainStopsAtAReadError(t *testing.T) {
	t.Parallel()

	f := newFakeChain()
	f.fail = errFakeRead

	change := audit.ChainChange{At: walkAt(1), By: "alice", To: audit.ChainStateOff}
	err := audit.MigrateChain(t.Context(), f, []namespace.Fence{audit.ServerFence()}, change)

	if err == nil || f.rec.State != audit.ChainStateOn {
		t.Fatalf("migrate with a read error: err %v, record %+v, want an error and the old record", err, f.rec)
	}
}

func TestLogAgrees(t *testing.T) {
	t.Parallel()

	f := newFakeChain()
	scopes := []namespace.Name{"acme", "empty"}

	agrees := func(state audit.ChainState) bool {
		t.Helper()

		ok, err := audit.LogAgrees(t.Context(), f, scopes, state)
		if err != nil {
			t.Fatalf("log agrees: %v", err)
		}

		return ok
	}

	if !agrees(audit.ChainStateOn) || !agrees(audit.ChainStateOff) {
		t.Fatal("empty scopes: want agreement with both states")
	}

	f.put(walkEntry(0))

	if !agrees(audit.ChainStateOn) || agrees(audit.ChainStateOff) {
		t.Fatal("a chained tail: want agreement with on only")
	}

	f.rec = recordOf(audit.ChainStateOff, 1)
	f.put(walkEntry(0))

	if agrees(audit.ChainStateOn) || !agrees(audit.ChainStateOff) {
		t.Fatal("an unchained tail: want agreement with off only")
	}

	f.fail = errFakeRead
	if _, err := audit.LogAgrees(t.Context(), f, scopes, audit.ChainStateOn); err == nil {
		t.Fatal("log agrees with a read error: want an error")
	}
}

func TestChainSetEntry(t *testing.T) {
	t.Parallel()

	caller := walkMarker(1, "server:other", "", nil)
	caller.Subject, caller.TokenID, caller.SourceIP, caller.RequestID = "alice", "tok-1", "192.0.2.1", "req-1"

	e := audit.ChainSetEntry(caller, audit.ChainChange{At: walkAt(1), By: "alice", To: audit.ChainStateOff})

	if e.Namespace != audit.ServerScope || e.Action != audit.ActionSetChain || e.Subject != "alice" ||
		e.TokenID != "tok-1" || e.SourceIP != "192.0.2.1" || e.RequestID != "req-1" ||
		e.ValuePath != "/audit/chain/off" || e.Validate() != nil {
		t.Fatalf("set entry: got %+v, want a valid entry in the server scope by alice", e)
	}
}

// errFakeRead is the read failure of the fake chain store.
var errFakeRead = errors.New("read failed")
