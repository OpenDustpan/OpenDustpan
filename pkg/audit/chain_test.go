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
// records the chain state each server entry was appended under.
type fakeSwitch struct {
	appended []audit.Entry
	chainAt  []bool
	state    audit.ChainState
	chained  bool
	on       bool
}

func (f *fakeSwitch) SetChain(on bool) { f.on = on }

func (f *fakeSwitch) HasChainedEntries(context.Context) (bool, error) { return f.chained, nil }

func (f *fakeSwitch) ChainState(context.Context) (audit.ChainState, error) { return f.state, nil }

func (f *fakeSwitch) SaveChainState(_ context.Context, s audit.ChainState) error {
	f.state = s

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

	f := &fakeSwitch{appended: nil, chainAt: nil, state: tc.recorded, chained: tc.chained, on: true}

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

	if f.state != wantState {
		t.Fatalf("recorded state: got %q, want %q", f.state, wantState)
	}

	checkChangeEntry(t, f, tc.wantEntry)

	if warned := strings.Contains(logs.String(), "audit chain is off"); warned != tc.wantWarn {
		t.Fatalf("warning: got %v, want %v (%s)", warned, tc.wantWarn, logs.String())
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

	walk := audit.NewChainWalk()
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
		w := audit.NewChainWalk()
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
