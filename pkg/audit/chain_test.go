package audit_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// fakeSwitch is an in-memory audit.ChainSwitch. It keeps the switch
// entries it records and counts the reads of the whole log.
type fakeSwitch struct {
	entries []audit.ChainEntry
	rec     audit.ChainRecord
	records int
	scans   int
	// conflicts is the count of writes to refuse with
	// audit.ErrChainRecordChanged, as when another instance writes first.
	conflicts int
	chained   bool
	on        bool
}

func (f *fakeSwitch) SetChain(on bool) { f.on = on }

func (f *fakeSwitch) HasChainedEntries(context.Context) (bool, error) {
	f.scans++

	return f.chained, nil
}

func (f *fakeSwitch) ChainRecord(context.Context) (audit.ChainRecord, error) { return f.rec, nil }

func (f *fakeSwitch) RecordChain(_ context.Context, w audit.ChainWrite) error {
	if f.conflicts > 0 || w.Read != f.rec {
		f.conflicts--

		return audit.ErrChainRecordChanged
	}

	entries := make([]audit.ChainEntry, 0, len(w.Entries))

	for i := range w.Entries {
		e := w.Entries[i]
		if w.Now != nil {
			e.Entry.At = w.Now()
		}

		if err := e.Entry.Validate(); err != nil {
			return err
		}

		entries = append(entries, e)
	}

	f.entries = append(f.entries, entries...)
	f.rec = w.Record
	f.records++

	return nil
}

// newFake answers a fake switch with rec and a log that holds chained
// entries when chained.
func newFake(rec audit.ChainRecord, chained bool) *fakeSwitch {
	return &fakeSwitch{entries: nil, rec: rec, records: 0, scans: 0, conflicts: 0, chained: chained, on: true}
}

// clockAt answers a clock that always answers t.
func clockAt(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// wantSwitch is one switch entry a start must write: its value path and
// whether it holds a hash.
type wantSwitch struct {
	path    string
	chained bool
}

// resolveCase is one start of TestResolveChain.
type resolveCase struct {
	name     string
	setting  audit.ChainSetting
	recorded audit.ChainState
	want     []wantSwitch
	chained  bool
	wantOn   bool
	wantWarn bool
}

func TestResolveChain(t *testing.T) {
	t.Parallel()

	on := wantSwitch{path: "/audit/chain/on", chained: true}
	off := wantSwitch{path: "/audit/chain/off", chained: true}
	startOff := wantSwitch{path: "/audit/chain/off", chained: false}

	cases := []resolveCase{
		{
			name: "a new installation starts off", setting: audit.ChainAuto, recorded: audit.ChainUnknown,
			want: []wantSwitch{startOff}, chained: false, wantOn: false, wantWarn: false,
		},
		{
			name: "chained entries keep the chain on", setting: audit.ChainAuto, recorded: audit.ChainUnknown,
			want: []wantSwitch{on}, chained: true, wantOn: true, wantWarn: false,
		},
		{
			name: "turning off on purpose is a switch and warns", setting: audit.ChainOff, recorded: audit.ChainUnknown,
			want: []wantSwitch{on, off}, chained: true, wantOn: false, wantWarn: true,
		},
		{
			name: "a recorded off stays off and warns", setting: audit.ChainAuto, recorded: audit.ChainStateOff,
			want: nil, chained: true, wantOn: false, wantWarn: true,
		},
		{
			name: "turning on is a switch", setting: audit.ChainOn, recorded: audit.ChainStateOff,
			want: []wantSwitch{on}, chained: false, wantOn: true, wantWarn: false,
		},
		{
			name: "on with no change writes nothing", setting: audit.ChainOn, recorded: audit.ChainStateOn,
			want: nil, chained: true, wantOn: true, wantWarn: false,
		},
		{
			name:     "off on a new installation writes the start only",
			setting:  audit.ChainOff,
			recorded: audit.ChainUnknown,
			want:     []wantSwitch{startOff},
			chained:  false,
			wantOn:   false,
			wantWarn: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runResolveCase(t, tc)
		})
	}
}

// runResolveCase resolves tc against a fake switch and checks the result,
// the record, the switch entries and the warning.
func runResolveCase(t *testing.T, tc resolveCase) {
	t.Helper()

	// A record holds whether the log was ever chained; without a record the
	// resolve reads the log.
	rec := audit.ChainRecord{State: tc.recorded, EverChained: tc.recorded != audit.ChainUnknown && tc.chained}
	f := newFake(rec, tc.chained)

	var logs bytes.Buffer

	ports := audit.ChainPorts{Switch: f, Logger: slog.New(slog.NewTextHandler(&logs, nil))}

	got, err := audit.ResolveChain(t.Context(), ports, tc.setting, clockAt(time.Unix(0, 0)))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if got != tc.wantOn || f.on != tc.wantOn {
		t.Fatalf("chain: got %v (switch %v), want %v", got, f.on, tc.wantOn)
	}

	wantState := audit.ChainStateOff
	if tc.wantOn {
		wantState = audit.ChainStateOn
	}

	if f.rec.State != wantState {
		t.Fatalf("recorded state: got %q, want %q", f.rec.State, wantState)
	}

	checkRecordReads(t, f, tc)
	checkSwitches(t, f, tc.want)

	if warned := strings.Contains(logs.String(), "audit chain is off"); warned != tc.wantWarn {
		t.Fatalf("warning: got %v, want %v (%s)", warned, tc.wantWarn, logs.String())
	}
}

// checkRecordReads proves that only a resolve without a record read the
// whole log, that the switch entries and the state went in one call, and
// that the record notes a log that was ever chained.
func checkRecordReads(t *testing.T, f *fakeSwitch, tc resolveCase) {
	t.Helper()

	wantScans := 0
	if tc.recorded == audit.ChainUnknown {
		wantScans = 1
	}

	if f.scans != wantScans {
		t.Fatalf("reads of the whole log: got %d, want %d", f.scans, wantScans)
	}

	if f.records > 1 {
		t.Fatalf("record calls: got %d, want at most one", f.records)
	}

	before := tc.recorded == audit.ChainStateOn || (tc.recorded == audit.ChainUnknown && tc.chained)
	wantEver := tc.chained || tc.wantOn || before != tc.wantOn

	if f.rec.EverChained != wantEver {
		t.Fatalf("ever chained: got %v, want %v", f.rec.EverChained, wantEver)
	}
}

// checkSwitches proves f holds the switch entries want, in order.
func checkSwitches(t *testing.T, f *fakeSwitch, want []wantSwitch) {
	t.Helper()

	if len(f.entries) != len(want) {
		t.Fatalf("switch entries: got %d, want %d", len(f.entries), len(want))
	}

	for i, w := range want {
		e := f.entries[i]
		if e.Entry.Kind != audit.KindChange || e.Entry.Namespace != audit.ServerScope ||
			e.Entry.Action != audit.ActionSetChain || e.Entry.ValuePath != w.path || e.Chained != w.chained {
			t.Fatalf("switch entry %d: got %+v (chained %v), want %s chained %v",
				i, e.Entry, e.Chained, w.path, w.chained)
		}
	}
}

func TestResolveChainReadsAgainOnce(t *testing.T) {
	t.Parallel()

	off := audit.ChainRecord{State: audit.ChainStateOff, EverChained: false}

	// One write that another instance changed first: the start reads the
	// record again and records the switch.
	f := newFake(off, false)
	f.conflicts = 1

	ports := audit.ChainPorts{Switch: f, Logger: nil}

	on, err := audit.ResolveChain(t.Context(), ports, audit.ChainOn, clockAt(walkAt(0, 0)))
	if err != nil || !on || f.records != 1 {
		t.Fatalf("resolve after one change: on %v, err %v, records %d, want on, nil, 1", on, err, f.records)
	}

	// Two such writes fail the start.
	f = newFake(off, false)
	f.conflicts = 2
	ports.Switch = f

	_, err = audit.ResolveChain(t.Context(), ports, audit.ChainOn, clockAt(walkAt(0, 0)))
	if !errors.Is(err, audit.ErrChainRecordChanged) {
		t.Fatalf("resolve after two changes: got %v, want ErrChainRecordChanged", err)
	}

	if f.records != 0 {
		t.Fatalf("resolve after two changes: %d records, want 0", f.records)
	}
}

// fakeChecker is an audit.RestoreChecker that answers results, or err.
type fakeChecker struct {
	err     error
	results []audit.CheckResult
}

func (c fakeChecker) CheckRestored(context.Context, []namespace.Name) ([]audit.CheckResult, error) {
	return c.results, c.err
}

// recheckCase is one restore of TestRecheckChainAfterARestore.
type recheckCase struct {
	check       fakeChecker
	name        string
	rec         audit.ChainRecord
	wantInvalid int
	chained     bool
	wantEver    bool
	wantWarn    bool
	wantError   bool
}

func TestRecheckChainAfterARestore(t *testing.T) {
	t.Parallel()

	valid := fakeChecker{
		err: nil,
		results: []audit.CheckResult{
			{Checked: 1, BadSequence: 0, Unchained: 0, Valid: true},
			{Checked: 1, BadSequence: 0, Unchained: 0, Valid: true},
		},
	}
	stripped := fakeChecker{
		err: nil,
		results: []audit.CheckResult{
			{Checked: 1, BadSequence: 0, Unchained: 0, Valid: true},
			{Checked: 1, BadSequence: 1, Unchained: 0, Valid: false},
		},
	}
	failed := fakeChecker{err: io.ErrUnexpectedEOF, results: nil}
	off := audit.ChainRecord{State: audit.ChainStateOff, EverChained: false}

	cases := []recheckCase{
		{
			name: "a chained log on a chain that is off warns", rec: off, check: valid,
			chained: true, wantEver: true, wantWarn: true, wantInvalid: 0, wantError: false,
		},
		{
			name:        "a chained log on a chain that is on is quiet",
			rec:         audit.ChainRecord{State: audit.ChainStateOn, EverChained: false},
			check:       valid,
			chained:     true,
			wantEver:    true,
			wantWarn:    false,
			wantInvalid: 0,
			wantError:   false,
		},
		{
			name: "an unchained log changes nothing", rec: off, check: valid,
			chained: false, wantEver: false, wantWarn: false, wantInvalid: 0, wantError: false,
		},
		{
			name: "a restored log that fails the check logs an error", rec: off, check: stripped,
			chained: false, wantEver: false, wantWarn: false, wantInvalid: 1, wantError: true,
		},
		{
			name: "a check that cannot read counts every scope", rec: off, check: failed,
			chained: false, wantEver: false, wantWarn: false, wantInvalid: 2, wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRecheckCase(t, tc)
		})
	}
}

// runRecheckCase rechecks tc against a fake switch and checks the switch
// entry, the count of failed scopes and the logs.
func runRecheckCase(t *testing.T, tc recheckCase) {
	t.Helper()

	f := newFake(tc.rec, tc.chained)

	var logs bytes.Buffer

	ports := audit.ChainPorts{Switch: f, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	restore := audit.ChainRestore{
		Checker:   tc.check,
		Now:       clockAt(walkAt(0, 0)),
		BackupID:  "bkp-1",
		Requester: "alice",
		Scopes:    []namespace.Name{audit.ServerScope, "acme"},
	}

	invalid, err := audit.RecheckChain(t.Context(), ports, restore)
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}

	if invalid != tc.wantInvalid {
		t.Fatalf("failed scopes: got %d, want %d", invalid, tc.wantInvalid)
	}

	checkRestoreSwitch(t, f, tc.rec)
	checkRecheckLogs(t, logs.String(), tc)
}

// checkRestoreSwitch proves that f holds one chained switch entry for the
// state of rec by alice that marks the restore of bkp-1.
func checkRestoreSwitch(t *testing.T, f *fakeSwitch, rec audit.ChainRecord) {
	t.Helper()

	// A restore ends the restored timeline with one chained switch entry
	// for the recorded state, so the record notes a chained log. The entry
	// names the requester and marks the restore of the backup.
	if f.rec.State != rec.State || !f.rec.EverChained || len(f.entries) != 1 ||
		!f.entries[0].Chained || f.entries[0].Entry.ValuePath != "/audit/chain/"+string(rec.State) ||
		f.entries[0].Entry.Subject != "alice" {
		t.Fatalf("record %+v with entries %+v, want state %q, ever and one chained switch entry",
			f.rec, f.entries, rec.State)
	}

	if got := f.entries[0].Entry.Changes; len(got) != 1 || got[0].Key != audit.ChangeRestore || got[0].New != "bkp-1" {
		t.Fatalf("switch entry changes: got %+v, want restore = bkp-1", got)
	}
}

// checkRecheckLogs proves that logs hold the warning and the error that tc
// wants.
func checkRecheckLogs(t *testing.T, logs string, tc recheckCase) {
	t.Helper()

	if warned := strings.Contains(logs, "audit chain is off"); warned != tc.wantWarn {
		t.Fatalf("warning: got %v, want %v", warned, tc.wantWarn)
	}

	if logged := strings.Contains(logs, "level=ERROR"); logged != tc.wantError {
		t.Fatalf("error log: got %v, want %v in %q", logged, tc.wantError, logs)
	}
}

func TestParseChainSetting(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]audit.ChainSetting{"": audit.ChainAuto, "auto": audit.ChainAuto, "on": audit.ChainOn, "off": audit.ChainOff} {
		got, err := audit.ParseChainSetting(raw)
		if err != nil || got != want {
			t.Fatalf("parse %q: got %q, %v, want %q", raw, got, err, want)
		}
	}

	if _, err := audit.ParseChainSetting("yes"); !errors.Is(err, audit.ErrInvalidChainSetting) {
		t.Fatalf("parse yes: got %v, want ErrInvalidChainSetting", err)
	}
}

// walkStep is the time between two entries of the walk tests, longer than
// audit.ChainSwitchLag.
const walkStep = 10 * time.Second

// walkAt answers the time of sequence seq, plus d. Sequence 0 is at a fixed
// time, and sequence n is n times walkStep later.
func walkAt(seq int64, d time.Duration) time.Time {
	base := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)

	return base.Add(time.Duration(seq)*walkStep + d)
}

// walkChained returns entry seq linked to prev, with its hash.
func walkChained(seq int64, prev string) audit.Entry {
	e := walkEntry(seq)
	e.PrevHash = prev
	e.Hash = audit.ComputeHash(e, prev)

	return e
}

// walkOK reports whether the walk over points accepts every entry.
func walkOK(points []audit.ChainPoint, entries ...audit.Entry) bool {
	w := audit.NewChainWalk(audit.ChainTimeline{Points: points, Recorded: len(points) > 0, Broken: false})

	for i := range entries {
		if !w.Step(entries[i]) {
			return false
		}
	}

	return true
}

func TestChainWalk(t *testing.T) {
	t.Parallel()

	one := walkChained(1, "")
	two := walkEntry(2)
	three := walkChained(3, "")
	four := walkChained(4, three.Hash)

	// The chain started on, went off just after entry 1, and on again just
	// after entry 2.
	timeline := []audit.ChainPoint{
		{At: walkAt(0, 0), On: true},
		{At: walkAt(1, time.Second), On: false},
		{At: walkAt(2, time.Second), On: true},
	}

	walk := audit.NewChainWalk(audit.ChainTimeline{Points: timeline, Recorded: true, Broken: false})
	for _, e := range []audit.Entry{one, two, three, four} {
		if !walk.Step(e) {
			t.Fatalf("entry %d does not match", e.Sequence)
		}
	}

	if walk.Unchained() != 1 {
		t.Fatalf("unchained: got %d, want 1", walk.Unchained())
	}

	bad := []struct {
		name    string
		entries []audit.Entry
	}{
		{name: "a gap", entries: []audit.Entry{one, three}},
		{
			name:    "a link to the hash before an unchained entry",
			entries: []audit.Entry{one, two, walkChained(3, one.Hash)},
		},
		{name: "an unchained entry with a link", entries: []audit.Entry{one, withPrev(walkEntry(2), one.Hash)}},
		{name: "a changed field", entries: []audit.Entry{withSubject(one, "mallory")}},
		{name: "a removed hash while the chain was on", entries: []audit.Entry{walkEntry(1)}},
		{name: "a removed hash after the chain went on again", entries: []audit.Entry{one, two, walkEntry(3)}},
	}

	for _, tc := range bad {
		if walkOK(timeline, tc.entries...) {
			t.Fatalf("%s: the walk accepts it", tc.name)
		}
	}
}

func TestChainWalkSwitchLag(t *testing.T) {
	t.Parallel()

	// The chain started off and went on at entry 2.
	timeline := []audit.ChainPoint{{At: walkAt(0, 0), On: false}, {At: walkAt(2, 0), On: true}}

	late := walkEntry(3)
	late.At = walkAt(2, audit.ChainSwitchLag/2)

	if !walkOK(timeline, walkEntry(1), walkEntry(2), late) {
		t.Fatal("the walk refuses an unchained entry within the lag after the switch to on")
	}

	tooLate := walkEntry(3)
	tooLate.At = walkAt(2, 2*audit.ChainSwitchLag)

	if walkOK(timeline, walkEntry(1), walkEntry(2), tooLate) {
		t.Fatal("the walk accepts an unchained entry after the lag")
	}
}

func TestChainWalkShortOffWindow(t *testing.T) {
	t.Parallel()

	// The chain was on, went off at entry 2, and on again a quarter of the
	// lag later. An instance could still hold the state off for the lag.
	timeline := []audit.ChainPoint{
		{At: walkAt(0, 0), On: true},
		{At: walkAt(2, 0), On: false},
		{At: walkAt(2, audit.ChainSwitchLag/4), On: true},
	}

	within := walkEntry(2)
	within.At = walkAt(2, audit.ChainSwitchLag/2)

	if !walkOK(timeline, walkChained(1, ""), within) {
		t.Fatal("the walk refuses an unchained entry within the lag after a short off window")
	}

	after := walkEntry(2)
	after.At = walkAt(2, audit.ChainSwitchLag+audit.ChainSwitchLag/2)

	if walkOK(timeline, walkChained(1, ""), after) {
		t.Fatal("the walk accepts an unchained entry after the lag of a short off window")
	}
}

func TestChainWalkWithoutATimeline(t *testing.T) {
	t.Parallel()

	// A store that never recorded a switch: an unchained entry matches only
	// before the first chained entry of its scope.
	if !walkOK(nil, walkEntry(1), walkChained(2, "")) {
		t.Fatal("the walk refuses an unchained first entry")
	}

	if walkOK(nil, walkChained(1, ""), walkEntry(2)) {
		t.Fatal("the walk accepts an unchained entry after a chained one")
	}
}

func TestChainPointFromPath(t *testing.T) {
	t.Parallel()

	at := walkAt(0, 0)

	if p, ok := audit.ChainPointFromPath(at, "/audit/chain/on"); !ok || !p.On {
		t.Fatalf("on: got %+v, %v", p, ok)
	}

	if p, ok := audit.ChainPointFromPath(at, "/audit/chain/off"); !ok || p.On {
		t.Fatalf("off: got %+v, %v", p, ok)
	}

	if _, ok := audit.ChainPointFromPath(at, "/audit/other"); ok {
		t.Fatal("another path reads as a point")
	}
}

func withPrev(e audit.Entry, prev string) audit.Entry {
	e.PrevHash = prev

	return e
}

func withSubject(e audit.Entry, subject string) audit.Entry {
	e.Subject = subject

	return e
}

// walkEntry returns an entry of acme with sequence seq, at walkAt(seq, 0),
// and no chain.
func walkEntry(seq int64) audit.Entry {
	return audit.Entry{
		At:         walkAt(seq, 0),
		Namespace:  "acme",
		Subject:    "alice",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:read-secrets",
		Object:     "opentofu:acme/prod",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "test",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "req",
		PrevHash:   "",
		Hash:       "",
		Sequence:   seq,
	}
}

// walkSwitch returns the switch entry of state at seq of the server scope,
// without a hash.
func walkSwitch(seq int64, state audit.ChainState, at time.Time) audit.Entry {
	e := walkEntry(seq)
	e.Namespace, e.At = audit.ServerScope, at
	e.Kind, e.Action, e.Object = audit.KindChange, audit.ActionSetChain, audit.ServerScope.String()
	e.ValuePath = "/audit/chain/" + string(state)

	return e
}

// walkChainedSwitch returns walkSwitch with its hash.
func walkChainedSwitch(seq int64, state audit.ChainState, at time.Time) audit.Entry {
	e := walkSwitch(seq, state, at)
	e.Hash = audit.ComputeHash(e, "")

	return e
}

// serverLink numbers es from 1 as entries of the server scope and chains
// every entry whose hash is set: it links to the entry before it, or to an
// empty hash after an entry without a hash.
func serverLink(entries ...audit.Entry) []audit.Entry {
	es := make([]audit.Entry, len(entries))
	copy(es, entries)

	prev := ""

	for i := range es {
		es[i].Namespace, es[i].Sequence = audit.ServerScope, int64(i+1)

		if es[i].Hash == "" {
			prev = ""

			continue
		}

		es[i].PrevHash = prev
		es[i].Hash = audit.ComputeHash(es[i], prev)
		prev = es[i].Hash
	}

	return es
}

// build adds es to a new builder and answers the timeline against rec.
func build(rec audit.ChainRecord, es []audit.Entry) audit.ChainTimeline {
	b := audit.NewTimelineBuilder()

	for i := range es {
		if !b.Add(es[i]) {
			break
		}
	}

	return b.Timeline(rec)
}

// noRecord answers the record of an installation that recorded nothing.
func noRecord() audit.ChainRecord {
	return audit.ChainRecord{State: audit.ChainUnknown, EverChained: false}
}

func TestTimelineBuilder(t *testing.T) {
	t.Parallel()

	plain := walkChained(0, "")
	plain.At = walkAt(2, 0)

	got := build(audit.ChainRecord{State: audit.ChainStateOff, EverChained: true}, serverLink(
		walkSwitch(1, audit.ChainStateOff, walkAt(0, 0)),
		walkChainedSwitch(2, audit.ChainStateOn, walkAt(1, 0)),
		plain,
		walkChainedSwitch(4, audit.ChainStateOff, walkAt(3, 0)),
	))
	if got.Broken || len(got.Points) != 3 || got.Points[0].On || !got.Points[1].On || got.Points[2].On {
		t.Fatalf("timeline: got %+v, want off, on, off and not broken", got)
	}

	replayed := serverLink(
		walkChainedSwitch(1, audit.ChainStateOn, walkAt(0, 0)),
		walkChainedSwitch(2, audit.ChainStateOff, walkAt(1, 0)),
	)
	replayed[1].PrevHash = "other-installation"
	replayed[1].Hash = audit.ComputeHash(replayed[1], replayed[1].PrevHash)

	broken := []struct {
		name    string
		entries []audit.Entry
	}{
		{name: "an unhashed switch to off after sequence 1", entries: serverLink(
			walkChainedSwitch(1, audit.ChainStateOn, walkAt(0, 0)),
			walkSwitch(2, audit.ChainStateOff, walkAt(1, 0)),
		)},
		{name: "a switch entry that links to another chain", entries: replayed},
		{name: "a switch entry earlier than the one before it", entries: serverLink(
			walkChainedSwitch(1, audit.ChainStateOn, walkAt(2, 0)),
			walkChainedSwitch(2, audit.ChainStateOff, walkAt(1, 0)),
		)},
		{name: "an unhashed switch to on at sequence 1", entries: serverLink(
			walkSwitch(1, audit.ChainStateOn, walkAt(0, 0)),
		)},
	}

	for _, tc := range broken {
		if got := build(noRecord(), tc.entries); !got.Broken {
			t.Fatalf("%s: timeline is not broken: %+v", tc.name, got)
		}
	}
}

func TestTimelineAgainstTheRecord(t *testing.T) {
	t.Parallel()

	startOff := walkSwitch(1, audit.ChainStateOff, walkAt(0, 0))
	on := walkChainedSwitch(2, audit.ChainStateOn, walkAt(1, 0))
	unknown := walkChainedSwitch(2, audit.ChainStateOn, walkAt(1, 0))
	unknown.ValuePath = "/audit/chain/On"

	cases := []struct {
		name    string
		rec     audit.ChainRecord
		entries []audit.Entry
		broken  bool
	}{
		{
			name: "a fresh installation with the chain off", broken: false,
			rec: audit.ChainRecord{State: audit.ChainStateOff, EverChained: false}, entries: serverLink(startOff),
		},
		{
			name: "a chain that is on with a switch to on", broken: false,
			rec: audit.ChainRecord{State: audit.ChainStateOn, EverChained: true}, entries: serverLink(startOff, on),
		},
		{
			name: "a record that says chained and no hashed switch", broken: true,
			rec: audit.ChainRecord{State: audit.ChainStateOff, EverChained: true}, entries: serverLink(startOff),
		},
		{
			name: "a record that says on and no hashed switch", broken: true,
			rec: audit.ChainRecord{State: audit.ChainStateOn, EverChained: false}, entries: serverLink(startOff),
		},
		{
			name: "a last point that is not the recorded state", broken: true,
			rec: audit.ChainRecord{State: audit.ChainStateOff, EverChained: true}, entries: serverLink(startOff, on),
		},
		{
			name: "a switch entry with an unknown value path", broken: true,
			rec: noRecord(), entries: serverLink(startOff, unknown),
		},
	}

	for _, tc := range cases {
		if got := build(tc.rec, tc.entries); got.Broken != tc.broken {
			t.Fatalf("%s: broken %v, want %v", tc.name, got.Broken, tc.broken)
		}
	}
}

func TestRestoredTimeline(t *testing.T) {
	t.Parallel()

	startOn := walkChainedSwitch(1, audit.ChainStateOn, walkAt(0, 0))
	startOff := walkSwitch(1, audit.ChainStateOff, walkAt(0, 0))
	off := audit.ChainRecord{State: audit.ChainStateOff, EverChained: false}
	chained := audit.ChainRecord{State: audit.ChainStateOn, EverChained: true}

	// The backup ends on and the target records off: the log of the
	// backup is whole.
	b := audit.NewTimelineBuilder()
	b.Add(serverLink(startOn)[0])

	if got := b.RestoredTimeline(off); got.Broken {
		t.Fatalf("a backup that ends in another state: %+v, want not broken", got)
	}

	// A backup without a hashed switch, and a target that records a
	// chained log: someone removed the hashes.
	b = audit.NewTimelineBuilder()
	b.Add(serverLink(startOff)[0])

	if got := b.RestoredTimeline(chained); !got.Broken {
		t.Fatalf("a backup without a hashed switch: %+v, want broken", got)
	}
}

func TestChainWalkBrokenTimeline(t *testing.T) {
	t.Parallel()

	timeline := audit.ChainTimeline{
		Points:   []audit.ChainPoint{{At: walkAt(0, 0), On: false}},
		Recorded: true,
		Broken:   true,
	}
	if audit.NewChainWalk(timeline).Step(walkEntry(1)) {
		t.Fatal("the walk accepts an entry against a broken timeline")
	}
}

func TestChainSettingString(t *testing.T) {
	t.Parallel()

	if got := audit.ChainSetting(9).String(); got != "9" {
		t.Fatalf("an unknown setting prints %q, want 9", got)
	}
}

func TestChainWalkRecordWithoutSwitches(t *testing.T) {
	t.Parallel()

	walk := audit.NewChainWalk(audit.ChainTimeline{Points: nil, Recorded: true, Broken: false})
	if walk.Step(walkChained(1, "")) {
		t.Fatal("the walk accepts an entry while the switch entries of a recorded chain are gone")
	}
}

func TestChainWalkTimeMustNotGoBack(t *testing.T) {
	t.Parallel()

	// The chain started off and went on after entry 1.
	timeline := []audit.ChainPoint{{At: walkAt(0, 0), On: false}, {At: walkAt(1, time.Second), On: true}}

	two := walkChained(2, "")
	moved := walkEntry(3)
	moved.At = walkAt(1, 500*time.Millisecond)

	if walkOK(timeline, walkEntry(1), two, moved) {
		t.Fatal("the walk accepts an unchained entry earlier than the entry before it")
	}

	back := walkChained(3, "")
	back.At = walkAt(0, time.Second)
	back.Hash = audit.ComputeHash(back, "")

	if walkOK(timeline, walkEntry(1), walkEntry(2), back) {
		t.Fatal("the walk accepts a chained entry earlier than the unchained entry before it")
	}
}

func TestChainWalkNeedsAnOffAfterTheLastChainedEntry(t *testing.T) {
	t.Parallel()

	// The chain was off from the start. A chained entry long after the
	// start, then an unchained one: no switch to off follows the chained
	// entry, so the unchained entry does not fit.
	timeline := make([]audit.ChainPoint, 0, 2)
	timeline = append(timeline, audit.ChainPoint{At: walkAt(0, 0), On: false})

	if walkOK(timeline, walkChained(1, ""), walkEntry(2)) {
		t.Fatal("the walk accepts an unchained entry after a chained one with no switch to off between")
	}

	// With a switch to off after the chained entry, it fits.
	timeline = append(timeline, audit.ChainPoint{At: walkAt(1, time.Second), On: false})

	if !walkOK(timeline, walkChained(1, ""), walkEntry(2)) {
		t.Fatal("the walk refuses an unchained entry after a switch to off")
	}
}
