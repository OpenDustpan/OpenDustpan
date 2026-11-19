package gate_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
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

	if d.Result != gate.ResultError {
		t.Errorf("Decision.Result = %q, want error: the gate still errs", d.Result)
	}

	rec := e.newestRecord(t)
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

	named := 0

	for _, r := range e.records(t) {
		if r.BreakGlass != "" {
			named++
		}
	}

	if named != 1 {
		t.Errorf("records that name a break-glass = %d, want 1: the refusal names none", named)
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
		bgPass(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"), time.Now()),
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
