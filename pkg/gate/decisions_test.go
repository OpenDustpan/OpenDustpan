package gate_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

func planOn(t *testing.T, eng *gate.Engine, who string, facts gate.Facts) gate.Record {
	t.Helper()

	rec, err := eng.Planned(t.Context(), gate.PlannedRequest{
		Subject: subject, Principal: gate.Principal{ID: who, Groups: []string{"dev"}}, Facts: facts,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	return rec
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// Decision 1: defaults at namespace, collection and unit; the strictest wins;
// off when none is set; an unless carves out an exemption.
func TestApprovalDefaults(t *testing.T) {
	t.Parallel()

	eng := gate.NewEngine()

	if rec := planOn(t, eng, "alice", goodFacts()); rec.Result != gate.ResultAllow || len(rec.Approvals) != 0 {
		t.Fatalf("no default: %+v, want allow with no approval (no hidden minimum)", rec)
	}

	must(t, eng.Registry.PutDefault(gate.ApprovalDefault{
		ID: "ns", Scope: gate.Scope{Level: gate.LevelNamespace, Name: "acme"}, Count: 1,
	}))
	must(t, eng.Registry.PutDefault(gate.ApprovalDefault{
		ID: "coll", Scope: prodScope, Count: 2,
		Unless: `context.unit.labels.contains("auto=ok") && context.plan.delete == 0`,
	}))

	rec := planOn(t, eng, "alice", goodFacts())
	if len(rec.Approvals) != 1 || rec.Approvals[0].Need != 2 || rec.Approvals[0].Rule != "default" {
		t.Fatalf("strictest: %+v, want one requirement of 2", rec.Approvals)
	}

	exempt := goodFacts()
	exempt.Unit.Labels = []string{"auto=ok"}

	// The exemption is in the collection default only: the namespace default still holds.
	rec = planOn(t, eng, "alice", exempt)
	if len(rec.Approvals) != 1 || rec.Approvals[0].Need != 1 {
		t.Fatalf("exempt from the collection default: %+v, want need 1", rec.Approvals)
	}

	if err := eng.Registry.PutDefault(
		gate.ApprovalDefault{ID: "inst", Scope: gate.Scope{Level: gate.LevelInstallation}, Count: 1},
	); !errors.Is(
		err,
		gate.ErrInvalid,
	) {
		t.Fatalf("installation default: err = %v, want ErrInvalid", err)
	}
}

// Decision 2: a gate carves out its own exemption with unless.
func TestUnlessExemption(t *testing.T) {
	t.Parallel()

	eng := gate.NewEngine()
	g := gateOf("a/no-delete", "context.plan.delete > 0", gate.OutcomeDeny)
	g.Unless = `context.unit.labels.contains("sandbox=true")`

	must(t, eng.Registry.PutGate(g))

	facts := goodFacts()
	facts.Plan.Delete = 1

	if rec := planOn(t, eng, "alice", facts); rec.Result != gate.ResultDeny {
		t.Fatalf("delete: %s, want deny", rec.Result)
	}

	facts.Unit.Labels = []string{"sandbox=true"}

	if rec := planOn(t, eng, "alice", facts); rec.Result != gate.ResultAllow {
		t.Fatalf("exempt delete: %s, want allow", rec.Result)
	}
}

// Decision 4: an owner rule takes paths and Cedar conditions.
func TestOwnerRuleConditions(t *testing.T) {
	t.Parallel()

	eng := gate.NewEngine()
	must(t, eng.Registry.PutGate(gateOf("a/owners", "true", gate.ApprovalOutcome("owners"))))
	must(t, eng.Registry.PutOwnerRule(gate.OwnerRule{
		ID:     "net-cidr",
		Scope:  prodScope,
		Paths:  []string{"modules/network/**"},
		When:   `context.change.inputKeys.contains("cidr") && context.operation == "apply" && context.plan.replace == 0`,
		Groups: []string{"netops"},
		Count:  2,
	}))

	cases := []struct {
		name   string
		mut    func(*gate.Facts)
		wantAp int
	}{
		{"all conditions", func(*gate.Facts) {}, 1},
		{"other path", func(f *gate.Facts) { f.Change.Paths = []string{"modules/app/x.tf"} }, 0},
		{"input not changed", func(f *gate.Facts) { f.Change.InputKeys = []string{"region"} }, 0},
		{"replace in plan", func(f *gate.Facts) { f.Plan.Replace = 1 }, 0},
		{"other operation", func(f *gate.Facts) { f.Operation = "destroy" }, 0},
	}

	for _, tc := range cases {
		facts := goodFacts()
		tc.mut(&facts)

		rec := planOn(t, eng, "alice", facts)
		if len(rec.Approvals) != tc.wantAp {
			t.Errorf("%s: approvals = %+v, want %d", tc.name, rec.Approvals, tc.wantAp)
		}
	}
}

// Decision 5: a window of 0 asks every time.
func TestFreshAuthWindow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		window         int64
		age            int64
		reauth, wantOK bool
	}{
		{0, 5, false, false}, {0, 5, true, true}, {900, 60, false, true}, {900, 901, false, false}, {900, 901, true, true},
	} {
		eng := gate.NewEngine()
		w := tc.window

		must(
			t,
			eng.Registry.PutRule(gate.ApprovalRule{Name: "r", Groups: []string{"dev"}, Count: 1, FreshAuthSeconds: &w}),
		)
		must(t, eng.Registry.PutGate(gateOf("a/r", "true", gate.ApprovalOutcome("r"))))

		rec := planOn(t, eng, "alice", goodFacts())
		_, err := eng.Approve(t.Context(), rec.ID, gate.Approver{
			Principal: gate.Principal{
				ID:     "bob",
				Groups: []string{"dev"},
			},
			AuthAgeSeconds:     tc.age,
			ReauthenticatedNow: tc.reauth,
		})

		if (err == nil) != tc.wantOK || (err != nil && !errors.Is(err, gate.ErrFreshAuth)) {
			t.Errorf("%+v: err = %v", tc, err)
		}
	}
}

// Decision 6: a warn blocks auto-apply; an evaluate warn does not.
func TestWarnBlocksAutoApply(t *testing.T) {
	t.Parallel()

	eng := gate.NewEngine()

	if rec := planOn(t, eng, "alice", goodFacts()); !rec.AutoApply {
		t.Fatalf("no gate: autoApply = false, want true")
	}

	w := gateOf("a/warn", "true", gate.OutcomeWarn)
	w.Mode = gate.ModeEvaluate

	must(t, eng.Registry.PutGate(w))

	if rec := planOn(t, eng, "alice", goodFacts()); !rec.AutoApply {
		t.Fatalf("evaluate warn: autoApply = false, want true")
	}

	w.Mode = gate.ModeEnforce
	must(t, eng.Registry.PutGate(w))

	rec := planOn(t, eng, "alice", goodFacts())
	if rec.AutoApply || rec.Result != gate.ResultAllow {
		t.Fatalf("enforced warn: %+v, want allow without autoApply", rec)
	}
}
