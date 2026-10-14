package audit_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// fakeSwitch is an in-memory audit.ChainSwitch and audit.ServerStore. It
// records the chain state each server entry was appended under, and counts
// the reads of the whole log.
type fakeSwitch struct {
	appended []audit.Entry
	chainAt  []bool
	rec      audit.ChainRecord
	scans    int
	chained  bool
	on       bool
}

func (f *fakeSwitch) SetChain(on bool) { f.on = on }

func (f *fakeSwitch) HasChainedEntries(context.Context) (bool, error) {
	f.scans++

	return f.chained, nil
}

func (f *fakeSwitch) ChainRecord(context.Context) (audit.ChainRecord, error) { return f.rec, nil }

func (f *fakeSwitch) SaveChainRecord(_ context.Context, rec audit.ChainRecord) error {
	f.rec = rec

	return nil
}

func (f *fakeSwitch) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	f.appended = append(f.appended, e)
	f.chainAt = append(f.chainAt, f.on)

	return e, nil
}

// resolveCase is one start of TestResolveChain.
type resolveCase struct {
	name      string
	setting   audit.ChainSetting
	recorded  audit.ChainState
	wantEntry string
	chained   bool
	wantOn    bool
	wantWarn  bool
}

func TestResolveChain(t *testing.T) {
	t.Parallel()

	cases := []resolveCase{
		{
			name: "a new installation starts off", setting: audit.ChainAuto, recorded: audit.ChainUnknown,
			wantEntry: "", chained: false, wantOn: false, wantWarn: false,
		},
		{
			name: "chained entries keep the chain on", setting: audit.ChainAuto, recorded: audit.ChainUnknown,
			wantEntry: "", chained: true, wantOn: true, wantWarn: false,
		},
		{
			name: "turning off on purpose is audited and warns", setting: audit.ChainOff, recorded: audit.ChainUnknown,
			wantEntry: "/audit/chain/off", chained: true, wantOn: false, wantWarn: true,
		},
		{
			name: "a recorded off stays off and warns", setting: audit.ChainAuto, recorded: audit.ChainStateOff,
			wantEntry: "", chained: true, wantOn: false, wantWarn: true,
		},
		{
			name: "turning on is audited", setting: audit.ChainOn, recorded: audit.ChainStateOff,
			wantEntry: "/audit/chain/on", chained: false, wantOn: true, wantWarn: false,
		},
		{
			name: "on with no change writes nothing", setting: audit.ChainOn, recorded: audit.ChainStateOn,
			wantEntry: "", chained: false, wantOn: true, wantWarn: false,
		},
		{
			name: "off on a new installation writes nothing", setting: audit.ChainOff, recorded: audit.ChainUnknown,
			wantEntry: "", chained: false, wantOn: false, wantWarn: false,
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
// the recorded state, the change entry and the warning.
func runResolveCase(t *testing.T, tc resolveCase) {
	t.Helper()

	// A record holds whether the log was ever chained; without a record the
	// resolve reads the log.
	rec := audit.ChainRecord{State: tc.recorded, EverChained: tc.recorded != audit.ChainUnknown && tc.chained}
	f := &fakeSwitch{appended: nil, chainAt: nil, rec: rec, scans: 0, chained: tc.chained, on: true}

	var logs bytes.Buffer

	ports := audit.ChainPorts{Switch: f, Server: f, Logger: slog.New(slog.NewTextHandler(&logs, nil))}

	on, err := audit.ResolveChain(t.Context(), ports, tc.setting, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if on != tc.wantOn || f.on != tc.wantOn {
		t.Fatalf("chain: got %v (switch %v), want %v", on, f.on, tc.wantOn)
	}

	wantState := audit.ChainStateOff
	if tc.wantOn {
		wantState = audit.ChainStateOn
	}

	if f.rec.State != wantState {
		t.Fatalf("recorded state: got %q, want %q", f.rec.State, wantState)
	}

	checkRecordReads(t, f, tc)

	checkChangeEntry(t, f, tc.wantEntry)

	if warned := strings.Contains(logs.String(), "audit chain is off"); warned != tc.wantWarn {
		t.Fatalf("warning: got %v, want %v (%s)", warned, tc.wantWarn, logs.String())
	}
}

// checkRecordReads proves that only a resolve without a record read the
// whole log, and that the record notes a log that was ever chained.
func checkRecordReads(t *testing.T, f *fakeSwitch, tc resolveCase) {
	t.Helper()

	wantScans := 0
	if tc.recorded == audit.ChainUnknown {
		wantScans = 1
	}

	if f.scans != wantScans {
		t.Fatalf("reads of the whole log: got %d, want %d", f.scans, wantScans)
	}

	if wantEver := tc.chained || tc.wantOn || tc.wantEntry != ""; f.rec.EverChained != wantEver {
		t.Fatalf("ever chained: got %v, want %v", f.rec.EverChained, wantEver)
	}
}

// checkChangeEntry proves f holds one chained change entry with the value
// path want, or no entry when want is empty.
func checkChangeEntry(t *testing.T, f *fakeSwitch, want string) {
	t.Helper()

	if want == "" {
		if len(f.appended) != 0 {
			t.Fatalf("change entries: got %d, want none", len(f.appended))
		}

		return
	}

	if len(f.appended) != 1 {
		t.Fatalf("change entries: got %d, want 1", len(f.appended))
	}

	e := f.appended[0]
	if e.Kind != audit.KindChange || e.Namespace != audit.ServerScope || e.ValuePath != want ||
		e.Action != audit.ActionSetChain {
		t.Fatalf("change entry: got %+v, want a change in the server scope with %s", e, want)
	}

	if !f.chainAt[0] {
		t.Fatal("the change entry was appended with the chain off")
	}
}

func TestRecheckChainAfterARestore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		rec      audit.ChainRecord
		chained  bool
		wantEver bool
		wantWarn bool
	}{
		{
			name:     "a chained log on a chain that is off warns",
			rec:      audit.ChainRecord{State: audit.ChainStateOff, EverChained: false},
			chained:  true,
			wantEver: true,
			wantWarn: true,
		},
		{
			name:     "a chained log on a chain that is on is quiet",
			rec:      audit.ChainRecord{State: audit.ChainStateOn, EverChained: false},
			chained:  true,
			wantEver: true,
			wantWarn: false,
		},
		{
			name:     "an unchained log changes nothing",
			rec:      audit.ChainRecord{State: audit.ChainStateOff, EverChained: false},
			chained:  false,
			wantEver: false,
			wantWarn: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeSwitch{appended: nil, chainAt: nil, rec: tc.rec, scans: 0, chained: tc.chained, on: true}

			var logs bytes.Buffer

			ports := audit.ChainPorts{Switch: f, Server: f, Logger: slog.New(slog.NewTextHandler(&logs, nil))}

			if err := audit.RecheckChain(t.Context(), ports); err != nil {
				t.Fatalf("recheck: %v", err)
			}

			if f.rec.State != tc.rec.State || f.rec.EverChained != tc.wantEver || len(f.appended) != 0 {
				t.Fatalf("record %+v with %d entries, want state %q, ever %v and no entry",
					f.rec, len(f.appended), tc.rec.State, tc.wantEver)
			}

			if warned := strings.Contains(logs.String(), "audit chain is off"); warned != tc.wantWarn {
				t.Fatalf("warning: got %v, want %v", warned, tc.wantWarn)
			}
		})
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

func TestChainWalk(t *testing.T) {
	t.Parallel()

	chainedAt := func(seq int64, prev string) audit.Entry {
		e := walkEntry(seq)
		e.PrevHash = prev
		e.Hash = audit.ComputeHash(e, prev)

		return e
	}
	unchainedAt := func(seq int64) audit.Entry {
		e := walkEntry(seq)
		e.PrevHash, e.Hash = "", ""

		return e
	}

	one := chainedAt(1, "")
	two := unchainedAt(2)
	three := chainedAt(3, "")

	// The chain went off between entries 1 and 2.
	offAt := []time.Time{walkEntry(1).At}

	walk := audit.NewChainWalk(offAt)
	for _, e := range []audit.Entry{one, two, three, chainedAt(4, three.Hash)} {
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
			entries: []audit.Entry{one, two, chainedAt(3, one.Hash)},
		},
		{name: "an unchained entry with a link", entries: []audit.Entry{one, withPrev(unchainedAt(2), one.Hash)}},
		{name: "a changed field", entries: []audit.Entry{withSubject(one, "mallory")}},
	}

	for _, tc := range bad {
		w := audit.NewChainWalk(offAt)
		ok := true

		for _, e := range tc.entries {
			ok = w.Step(e)
			if !ok {
				break
			}
		}

		if ok {
			t.Fatalf("%s: the walk accepts it", tc.name)
		}
	}

	// Without the change entry, an entry without a hash after a chained
	// entry fails: someone removed its hash.
	stripped := audit.NewChainWalk(nil)
	if !stripped.Step(one) || stripped.Step(two) {
		t.Fatal("the walk accepts an unchained entry after a chained one with no change entry")
	}

	// A log that starts with the chain off needs no change entry.
	fresh := audit.NewChainWalk(nil)
	if !fresh.Step(unchainedAt(1)) {
		t.Fatal("the walk refuses an unchained first entry")
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

// walkEntry returns an entry of acme with sequence seq and no chain.
func walkEntry(seq int64) audit.Entry {
	return audit.Entry{
		At:         time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC),
		Namespace:  "acme",
		Subject:    "alice",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:read-secrets",
		Object:     "opentofu:acme/prod",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
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
