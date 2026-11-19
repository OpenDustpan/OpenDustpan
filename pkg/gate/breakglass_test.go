package gate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const bgBroken = `context.unit.nothing == "x"`

// bgNow is the time of the issue of the passes in the tests.
func bgNow() time.Time { return time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC) }

var errDiskGone = errors.New("disk gone")

// bgEnv is a service with a pass store and a clock that the test moves.
type bgEnv struct {
	swEnv

	store *memBreakGlassStore
	clock *time.Time
}

func newBGEnv(t *testing.T) bgEnv {
	t.Helper()

	st := newMemBreakGlassStore()
	now := bgNow()

	e := newBreakGlassSWEnv(t, swSetup{
		units: swUnits{collections: nil, labels: nil}, engineErr: nil, wrap: nil, kinds: fixedKind("production"),
		breakGlass: st, now: func() time.Time { return now },
	})

	return bgEnv{swEnv: e, store: st, clock: &now}
}

// change returns a write change with a break-glass for the unit web.
func (e bgEnv) change(t *testing.T) gate.Change {
	t.Helper()

	c := swChange(t, e.fence)
	c.Units = []identity.Object{bgUnit(t, "web", "ws-test-web")}
	c.Operation = gate.OperationApply
	c.BreakGlass = "audit-1"

	return c
}

func (e bgEnv) issue(t *testing.T, holder string, kind gate.OperationKind, unit identity.Object) {
	t.Helper()

	if err := e.store.Issue(t.Context(), bgPass(t, holder, kind, unit, bgNow())); err != nil {
		t.Fatal(err)
	}
}

func TestBreakGlassErrorPassesOnce(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
	e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

	d, err := e.svc.Evaluate(t.Context(), e.change(t))
	if err != nil {
		t.Fatalf("Evaluate(error gate, valid pass) = %v, want nil", err)
	}

	// The state guard admits only allow and warn, so a passed write is a
	// warning. The gate error stays in the unit results of the record.
	if d.Result != gate.ResultWarn {
		t.Errorf("Decision.Result = %q, want warn: the write passed", d.Result)
	}

	rec := e.newestRecord(t)
	if rec.Result != gate.ResultWarn {
		t.Errorf("Record.Result = %q, want warn", rec.Result)
	}

	hasErr := func(g gate.PolicyResult) bool { return g.Error }
	if len(rec.Units) != 1 || !slices.ContainsFunc(rec.Units[0].Gates, hasErr) {
		t.Errorf("Record.Units = %+v, want the gate error in the unit results", rec.Units)
	}

	if rec.BreakGlass != "audit-1" {
		t.Errorf("Record.BreakGlass = %q, want audit-1: the next record names the break-glass", rec.BreakGlass)
	}

	if e.store.unused() != 0 {
		t.Errorf("unused passes = %d, want 0", e.store.unused())
	}
}

func TestBreakGlassReplayRefuses(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
	e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); err != nil {
		t.Fatal(err)
	}

	_, err := e.svc.Evaluate(t.Context(), e.change(t))
	if !errors.Is(err, gate.ErrRuleFailed) {
		t.Fatalf("second Evaluate = %v, want ErrRuleFailed: a pass is used once", err)
	}

	var refusal *gate.RefusalError
	const noValidMessage = "No valid break-glass pass: it is missing, used or expired."

	if !errors.As(err, &refusal) || !slices.Contains(refusal.Messages, noValidMessage) {
		t.Errorf("refusal = %v, want the message %q", err, noValidMessage)
	}

	// Both records name the attempt. The result tells them apart.
	for _, r := range e.records(t) {
		if r.BreakGlass != "audit-1" {
			t.Errorf("Record.BreakGlass = %q (result %q), want audit-1: a refused attempt is named too",
				r.BreakGlass, r.Result)
		}
	}

	// Both records have the same time (fixed clock), so pick the refused
	// record by the ID that the refusal names.
	if refusal == nil {
		return
	}

	var found bool

	for _, r := range e.records(t) {
		if r.ID != refusal.Record {
			continue
		}

		found = true

		if r.Result != gate.ResultError {
			t.Errorf("refused record Result = %q, want error", r.Result)
		}
	}

	if !found {
		t.Errorf("no record with ID %q: the refusal names a missing record", refusal.Record)
	}
}

func TestBreakGlassExpiryRefuses(t *testing.T) {
	t.Parallel()

	for name, after := range map[string]time.Duration{
		"at 15 minutes":    gate.BreakGlassPassWindow,
		"after 15 minutes": gate.BreakGlassPassWindow + time.Second,
	} {
		e := newBGEnv(t)
		e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
		e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

		*e.clock = bgNow().Add(after)

		if _, err := e.svc.Evaluate(t.Context(), e.change(t)); !errors.Is(err, gate.ErrRuleFailed) {
			t.Errorf("%s: Evaluate = %v, want ErrRuleFailed", name, err)
		}
	}

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
	e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

	*e.clock = bgNow().Add(gate.BreakGlassPassWindow - time.Second)

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); err != nil {
		t.Errorf("just before 15 minutes: Evaluate = %v, want nil", err)
	}
}

func TestBreakGlassBadReasonRefuses(t *testing.T) {
	t.Parallel()

	web := bgUnit(t, "web", "ws-test-web")

	for name, reason := range map[string]string{
		"empty":         "",
		"501 chars":     strings.Repeat("a", 501),
		"newline":       "line one\nline two",
		"tab":           "a\tb",
		"NUL":           "a\x00b",
		"escape":        "a\x1b[31mb",
		"DEL":           "a\x7fb",
		"C1 control":    "a\u0085b",
		"bidi override": "a\u202eb",
		"line sep":      "a\u2028b",
		"invalid UTF-8": "a\xffb",
	} {
		_, err := gate.NewBreakGlassPass(
			reason,
			gate.OperationApply,
			"alice",
			web,
			bgNow(),
		)
		if !errors.Is(
			err,
			gate.ErrInvalid,
		) {
			t.Errorf("%s: NewBreakGlassPass = %v, want ErrInvalid", name, err)
		}
	}

	_, err := gate.NewBreakGlassPass(
		strings.Repeat("é", 500),
		gate.OperationApply,
		"alice",
		web,
		bgNow(),
	)
	if err != nil {
		t.Errorf("500 characters: NewBreakGlassPass = %v, want nil", err)
	}

	noID, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	ns := identity.NamespaceObject("acme")

	for name, tc := range map[string]struct {
		kind   gate.OperationKind
		holder string
		unit   identity.Object
		at     time.Time
	}{
		"unknown kind":        {"explode", "alice", web, bgNow()},
		"empty kind":          {"", "alice", web, bgNow()},
		"empty holder":        {gate.OperationApply, "", web, bgNow()},
		"holder with newline": {gate.OperationApply, "al\nice", web, bgNow()},
		"unit without its ID": {gate.OperationApply, "alice", noID, bgNow()},
		"object that no unit": {gate.OperationApply, "alice", ns, bgNow()},
		"zero issue time":     {gate.OperationApply, "alice", web, time.Time{}},
	} {
		_, err = gate.NewBreakGlassPass(
			"reason",
			tc.kind,
			tc.holder,
			tc.unit,
			tc.at,
		)
		if !errors.Is(
			err,
			gate.ErrInvalid,
		) {
			t.Errorf("%s: NewBreakGlassPass = %v, want ErrInvalid", name, err)
		}
	}
}

func TestBreakGlassDenyStillRefuses(t *testing.T) {
	t.Parallel()

	web := bgUnit(t, "web", "ws-test-web")

	for name, tc := range map[string]struct {
		extra func(t *testing.T, e bgEnv)
		want  error
	}{
		"a deny next to a gate in error": {
			extra: func(t *testing.T, e bgEnv) {
				t.Helper()

				e.putGate(t, newSWGate(atUnit, "no-apply", "", gate.OutcomeDeny, "no"))
			},
			want: gate.ErrDenied,
		},
		"an approval next to a gate in error": {
			extra: func(t *testing.T, e bgEnv) {
				t.Helper()

				e.putRule(t)
				e.putGate(t, newSWGate(atUnit, "needs-two", "", gate.ApprovalOutcome("two-eyes"), "review"))
			},
			want: gate.ErrRuleFailed,
		},
		// An approval gate whose message does not render still matched with
		// an approval: a pass must not pass it.
		"an approval whose message does not render": {
			extra: func(t *testing.T, e bgEnv) {
				t.Helper()

				e.putRule(t)
				e.putGate(t, newSWGate(atUnit, "needs-two", "", gate.ApprovalOutcome("two-eyes"), "review {{unit.nothing}}"))
			},
			want: gate.ErrRuleFailed,
		},
		// A matched gate whose message names an absent fact is a gate in
		// error, but it still matched with a deny: a pass must not pass it.
		"a deny whose message does not render": {
			extra: func(t *testing.T, e bgEnv) {
				t.Helper()

				e.putGate(t, newSWGate(atUnit, "no-writes", "", gate.OutcomeDeny, "no writes to {{unit.nothing}}"))
			},
			want: gate.ErrRuleFailed,
		},
	} {
		e := newBGEnv(t)
		e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
		tc.extra(t, e)
		e.issue(t, "alice", gate.OperationApply, web)

		if _, err := e.svc.Evaluate(t.Context(), e.change(t)); !errors.Is(err, tc.want) {
			t.Errorf("%s: Evaluate = %v, want %v", name, err, tc.want)
		}

		if e.store.unused() != 1 || e.store.consumes.Load() != 0 {
			t.Errorf(
				"%s: unused = %d, consumes = %d, want 1 and 0: no pass is used",
				name,
				e.store.unused(),
				e.store.consumes.Load(),
			)
		}
	}

	// A deny alone, with no gate in error, also refuses and uses no pass.
	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "no-apply", "", gate.OutcomeDeny, "no"))
	e.issue(t, "alice", gate.OperationApply, web)

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); !errors.Is(err, gate.ErrDenied) || e.store.unused() != 1 {
		t.Errorf("deny alone: Evaluate = %v, unused = %d, want ErrDenied and 1", err, e.store.unused())
	}
}

func TestBreakGlassEngineFailureStillRefuses(t *testing.T) {
	t.Parallel()

	st := newMemBreakGlassStore()
	e := newBreakGlassSWEnv(t, swSetup{
		units: swUnits{collections: nil, labels: nil}, engineErr: errEngineDown, wrap: nil,
		kinds: fixedKind("production"), breakGlass: st, now: nil,
	})
	e.putGate(t, newSWGate(atUnit, "any", "", gate.OutcomeDeny, "x"))

	err := st.Issue(
		t.Context(),
		bgPass(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"), bgNow()),
	)
	if err != nil {
		t.Fatal(err)
	}

	c := swChange(t, e.fence)
	c.Units = []identity.Object{bgUnit(t, "web", "ws-test-web")}
	c.Operation, c.BreakGlass = gate.OperationApply, "audit-1"

	if _, err := e.svc.Evaluate(t.Context(), c); !errors.Is(err, gate.ErrUnavailable) || st.consumes.Load() != 0 {
		t.Fatalf("Evaluate = %v, consumes = %d, want ErrUnavailable and 0", err, st.consumes.Load())
	}
}

func TestBreakGlassWrongUnitOrKindRefuses(t *testing.T) {
	t.Parallel()

	web := bgUnit(t, "web", "ws-test-web")

	for name, p := range map[string]struct {
		holder string
		kind   gate.OperationKind
		unit   identity.Object
	}{
		"another unit":                 {"alice", gate.OperationApply, bgUnit(t, "other", "ws-test-other")},
		"another kind":                 {"alice", gate.OperationDestroy, web},
		"another holder":               {"bob", gate.OperationApply, web},
		"a name that looks like an ID": {"alice", gate.OperationApply, bgUnit(t, "web", "web")},
		"a prefix of the unit ID":      {"alice", gate.OperationApply, bgUnit(t, "web", "ws-test-we")},
	} {
		e := newBGEnv(t)
		e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
		e.issue(t, p.holder, p.kind, p.unit)

		if _, err := e.svc.Evaluate(t.Context(), e.change(t)); !errors.Is(err, gate.ErrRuleFailed) {
			t.Errorf("%s: Evaluate = %v, want ErrRuleFailed", name, err)
		}

		if e.store.unused() != 1 {
			t.Errorf("%s: the pass of another key was used", name)
		}
	}
}

func TestBreakGlassChangeShapeRefuses(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))

	two := e.change(t)
	two.Units = append(two.Units, bgUnit(t, "other", "ws-test-other"))

	noID := e.change(t)
	noID.Units = []identity.Object{swUnit(t, "web")}

	badKind := e.change(t)
	badKind.Operation = "explode"

	for name, c := range map[string]gate.Change{"two units": two, "unit with no ID": noID, "unknown kind": badKind} {
		if _, err := e.svc.Evaluate(t.Context(), c); !errors.Is(err, gate.ErrInvalid) {
			t.Errorf("%s: Evaluate = %v, want ErrInvalid", name, err)
		}
	}

	if e.store.consumes.Load() != 0 || len(e.records(t)) != 0 {
		t.Error("a change of a bad shape used a pass or wrote a record")
	}
}

func TestBreakGlassStoreErrorRefuses(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
	e.store.failWith = errDiskGone

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Evaluate = %v, want ErrUnavailable", err)
	}
}

func TestBreakGlassNoErrorNeedsNoPass(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); err != nil {
		t.Fatalf("Evaluate(no gate in error) = %v, want nil", err)
	}

	if e.store.consumes.Load() != 0 {
		t.Error("a write with no gate in error used a pass")
	}

	if rec := e.newestRecord(t); rec.BreakGlass != "" {
		t.Errorf("Record.BreakGlass = %q, want none: no gate was in error", rec.BreakGlass)
	}
}

func TestBreakGlassSkipPathUsesNoPass(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken-skip", bgBroken, gate.OutcomeDeny, "x").skippingErrors())
	e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); err != nil {
		t.Fatalf("Evaluate(gate in error with onError skip) = %v, want nil", err)
	}

	if e.store.consumes.Load() != 0 || e.store.unused() != 1 {
		t.Error("the skip path used a pass")
	}
}

// noRuleStore is a gate store whose configuration snapshot holds no approval
// rule, like a store that holds a gate from another version whose rule is
// gone.
type noRuleStore struct{ gate.Store }

func (n noRuleStore) Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error) {
	cfg, err := n.Store.Snapshot(ctx, scopes)
	cfg.ApprovalRules = nil

	return cfg, err
}

// TestBreakGlassMissingRuleStillRefuses checks that an approval gate whose
// rule is missing fails closed and that a valid pass does not pass it.
func TestBreakGlassMissingRuleStillRefuses(t *testing.T) {
	t.Parallel()

	st := newMemBreakGlassStore()
	now := bgNow()

	e := bgEnv{
		swEnv: newBreakGlassSWEnv(t, swSetup{
			units: swUnits{collections: nil, labels: nil}, engineErr: nil, kinds: fixedKind("production"),
			wrap:       func(s *sqlite.Store) gate.Store { return noRuleStore{Store: s} },
			breakGlass: st, now: func() time.Time { return now },
		}),
		store: st, clock: &now,
	}

	e.putRule(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
	e.putGate(t, newSWGate(atUnit, "needs-gone", "", gate.ApprovalOutcome("two-eyes"), "review"))
	e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

	if _, err := e.svc.Evaluate(t.Context(), e.change(t)); !errors.Is(err, gate.ErrRuleFailed) {
		t.Fatalf("Evaluate = %v, want ErrRuleFailed", err)
	}

	if st.unused() != 1 || st.consumes.Load() != 0 {
		t.Errorf("unused = %d, consumes = %d, want 1 and 0: no pass is used", st.unused(), st.consumes.Load())
	}
}

// wrongPassStore is a hostile store: its Consume uses a pass and returns the
// pass that make gives, whatever the key and the time are.
type wrongPassStore struct {
	*memBreakGlassStore

	make func(t *testing.T) gate.BreakGlassPass
	t    *testing.T
}

func (w wrongPassStore) Consume(
	ctx context.Context, key gate.BreakGlassKey, now time.Time,
) (gate.BreakGlassPass, error) {
	if _, err := w.memBreakGlassStore.Consume(ctx, key, now); err != nil {
		return gate.BreakGlassPass{}, err
	}

	return w.make(w.t), nil
}

// TestBreakGlassHostileStoreRefuses checks that the service checks the key and
// the window of the pass that a store returns: a store that returns a pass of
// another key or an expired pass does not let the write through.
func TestBreakGlassHostileStoreRefuses(t *testing.T) {
	t.Parallel()

	web := bgUnit(t, "web", "ws-test-web")

	for name, mk := range map[string]func(t *testing.T) gate.BreakGlassPass{
		"a pass of another holder": func(t *testing.T) gate.BreakGlassPass {
			t.Helper()

			return bgPass(t, "bob", gate.OperationApply, web, bgNow())
		},
		"a pass of another operation": func(t *testing.T) gate.BreakGlassPass {
			t.Helper()

			return bgPass(t, "alice", gate.OperationDestroy, web, bgNow())
		},
		"a pass of another unit": func(t *testing.T) gate.BreakGlassPass {
			t.Helper()

			return bgPass(t, "alice", gate.OperationApply, bgUnit(t, "other", "ws-test-other"), bgNow())
		},
		"an expired pass": func(t *testing.T) gate.BreakGlassPass {
			t.Helper()

			return bgPass(t, "alice", gate.OperationApply, web, bgNow().Add(-2*gate.BreakGlassPassWindow))
		},
	} {
		st := newMemBreakGlassStore()
		now := bgNow()

		e := bgEnv{
			swEnv: newBreakGlassSWEnv(t, swSetup{
				units: swUnits{collections: nil, labels: nil}, engineErr: nil, wrap: nil,
				kinds:      fixedKind("production"),
				breakGlass: wrongPassStore{memBreakGlassStore: st, make: mk, t: t},
				now:        func() time.Time { return now },
			}),
			store: st, clock: &now,
		}

		e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
		e.issue(t, "alice", gate.OperationApply, web)

		_, err := e.svc.Evaluate(t.Context(), e.change(t))
		if !errors.Is(err, gate.ErrUnavailable) {
			t.Errorf("%s: Evaluate = %v, want ErrUnavailable", name, err)
		}

		var refusal *gate.RefusalError
		if !errors.As(err, &refusal) ||
			!slices.Contains(refusal.Messages, "The break-glass pass could not be checked, so the write is refused.") {
			t.Errorf("%s: refusal = %v, want the message \"The break-glass pass could not be checked...\"", name, err)
		}

		if rec := e.newestRecord(t); rec.BreakGlass != "audit-1" {
			t.Errorf("%s: Record.BreakGlass = %q, want audit-1: the refused record names the attempt",
				name, rec.BreakGlass)
		}
	}
}

// TestBreakGlassWarnMessageFailureStillPasses checks that a matched warn gate
// whose message does not render is a gate in error that a pass lets through:
// only a deny or an approval that matched blocks the pass.
func TestBreakGlassWarnMessageFailureStillPasses(t *testing.T) {
	t.Parallel()

	e := newBGEnv(t)
	e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
	e.putGate(t, newSWGate(atUnit, "careful", "", gate.OutcomeWarn, "take care of {{unit.nothing}}"))
	e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

	d, err := e.svc.Evaluate(t.Context(), e.change(t))
	if err != nil {
		t.Fatalf("Evaluate = %v, want nil: a warn gate in error passes with a pass", err)
	}

	if d.Result != gate.ResultWarn || e.store.unused() != 0 {
		t.Errorf("Result = %q, unused = %d, want warn and 0", d.Result, e.store.unused())
	}
}

// TestBreakGlassBadAuditIDRefuses sends a hostile audit entry ID to the
// service. Each one refuses with ErrInvalid before the store is read, and no
// pass is used.
func TestBreakGlassBadAuditIDRefuses(t *testing.T) {
	t.Parallel()

	for name, id := range map[string]string{
		"newline":       "audit\n1",
		"NUL":           "audit\x001",
		"escape":        "audit\x1b[31m1",
		"bidi override": "audit\u202e1",
		"too long":      strings.Repeat("a", 129),
		"invalid UTF-8": "audit\xff1",
		"space":         "audit 1",
		"slash":         "audit/1",
		"non-ASCII":     "audité",
		"leading dash":  "-audit",
		"leading dot":   ".audit",
	} {
		e := newBGEnv(t)
		e.putGate(t, newSWGate(atUnit, "broken", bgBroken, gate.OutcomeDeny, "x"))
		e.issue(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"))

		c := e.change(t)
		c.BreakGlass = id

		if _, err := e.svc.Evaluate(t.Context(), c); !errors.Is(err, gate.ErrInvalid) {
			t.Errorf("%s: Evaluate = %v, want ErrInvalid", name, err)
		}

		if e.store.unused() != 1 || e.store.consumes.Load() != 0 || len(e.records(t)) != 0 {
			t.Errorf("%s: unused = %d, consumes = %d, records = %d, want 1, 0 and 0",
				name, e.store.unused(), e.store.consumes.Load(), len(e.records(t)))
		}
	}
}
