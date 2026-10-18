package gate_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

var prodScope = gate.Scope{Level: gate.LevelCollection, Name: "acme/prod"}

func goodFacts() gate.Facts {
	return gate.Facts{
		Operation: "apply", Unit: gate.UnitFact{Collection: "prod", Labels: []string{"tier=core"}},
		Plan: gate.PlanCounts{Update: 1}, Cost: gate.CostFact{DeltaMonthly: 15000, Available: true},
		Change: gate.ChangeFact{Paths: []string{"modules/network/main.tf"}, InputKeys: []string{"region", "cidr"}},
	}
}

func gateOf(id, cond string, out gate.Outcome) gate.Gate {
	return gate.Gate{
		ID: id, Scope: prodScope, DecisionPoint: gate.PointChangePlanned, Condition: cond,
		Outcome: out, Message: id, Mode: gate.ModeEnforce,
	}
}

func TestOwnersParseAndMatch(t *testing.T) {
	t.Parallel()

	secs, err := gate.ParseOwners(
		[]byte("# c\n\n[network] @group:netops 2 modules/network/** infra/*.tf\n[docs] @group:writers 1 docs/**\n"),
	)
	if err != nil || len(secs) != 2 || secs[0].Count != 2 || secs[0].Group != "netops" {
		t.Fatalf("parse: %+v, %v", secs, err)
	}

	for path, want := range map[string]bool{
		"modules/network/main.tf": true, "modules/network/a/b.tf": true, "infra/x.tf": true,
		"infra/a/x.tf": false, "modules/app/main.tf": false, "modules/network": true,
	} {
		if got := secs[0].Matches([]string{path}); got != want {
			t.Errorf("Matches(%q) = %v, want %v", path, got, want)
		}
	}

	for _, bad := range []string{"[x] @user:bob 1 a", "[x] @group:g 0 a", "x @group:g 1 a", "[x] @group:g 1"} {
		if _, err := gate.ParseOwners([]byte(bad)); !errors.Is(err, gate.ErrInvalid) {
			t.Errorf("ParseOwners(%q): err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestSaveRefusals(t *testing.T) {
	t.Parallel()

	reg := gate.NewRegistry()

	// A closing brace cannot add a second policy.
	inj := gateOf("acme/inject", `true }; forbid (principal, action, resource) when { true`, gate.OutcomeDeny)
	if err := reg.PutGate(inj); !errors.Is(err, gate.ErrInvalid) {
		t.Errorf("injection: err = %v, want ErrInvalid", err)
	}

	// A permit in a limit set is refused.
	err := reg.PutLimit(gate.Limit{ID: "acme/p", Scope: prodScope, Policy: "permit (principal, action, resource);"})
	if !errors.Is(err, gate.ErrInvalid) {
		t.Errorf("permit limit: err = %v, want ErrInvalid", err)
	}

	// Final: a gate cannot move to another scope.
	fin := gateOf("acme/fin", "true", gate.OutcomeWarn)
	fin.Final = true

	if err := reg.PutGate(fin); err != nil {
		t.Fatal(err)
	}

	fin.Scope = gate.Scope{Level: gate.LevelUnit, Name: "acme/prod/net"}
	if err := reg.PutGate(fin); !errors.Is(err, gate.ErrFinal) {
		t.Errorf("final: err = %v, want ErrFinal", err)
	}

	for _, bad := range []gate.Gate{
		gateOf("a", "true", "approve"), gateOf("a", "true ??", gate.OutcomeDeny),
		{ID: "a", Condition: "true", Outcome: gate.OutcomeDeny, DecisionPoint: gate.PointChangePlanned, Mode: "dry"},
	} {
		if err := reg.PutGate(bad); !errors.Is(err, gate.ErrInvalid) {
			t.Errorf("gate %+v: err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestCombining(t *testing.T) {
	t.Parallel()

	sub := subject
	who := gate.Principal{ID: "alice"}
	zero := int64(0)
	rule := gate.ApprovalRule{Name: "sec", Groups: []string{"security"}, Count: 2, FreshAuthSeconds: &zero}

	run := func(gates ...gate.Gate) gate.Record {
		eng := gate.NewEngine()
		if err := eng.Registry.PutRule(rule); err != nil {
			t.Fatal(err)
		}

		for _, g := range gates {
			if err := eng.Registry.PutGate(g); err != nil {
				t.Fatal(err)
			}
		}

		rec, err := eng.Planned(t.Context(), gate.PlannedRequest{Subject: sub, Principal: who, Facts: goodFacts()})
		if err != nil {
			t.Fatal(err)
		}

		return rec
	}

	if r := run(
		gateOf("a/deny", "true", gate.OutcomeDeny),
		gateOf("a/ap", "true", gate.ApprovalOutcome("sec")),
	); r.Result != gate.ResultDeny {
		t.Errorf("deny and approval: result = %s, want deny", r.Result)
	}

	if r := run(
		gateOf("a/ap", "true", gate.ApprovalOutcome("sec")),
		gateOf("a/ap2", "true", gate.ApprovalOutcome("sec")),
	); r.Result != gate.ResultRequireApproval ||
		len(r.Approvals) != 1 {
		t.Errorf("same rule twice: %+v, want one requirement", r)
	}

	if r := run(gateOf("a/w", "true", gate.OutcomeWarn)); r.Result != gate.ResultAllow || len(r.Matched) != 1 {
		t.Errorf("warn: %+v, want allow with the warn recorded", r)
	}

	if r := run(gateOf("a/no", "false", gate.OutcomeDeny)); r.Result != gate.ResultAllow || len(r.Matched) != 0 {
		t.Errorf("no match: %+v", r)
	}

	// Approval rule with fresh authentication and a count of two people.
	eng := gate.NewEngine()
	if err := eng.Registry.PutRule(rule); err != nil {
		t.Fatal(err)
	}

	if err := eng.Registry.PutGate(gateOf("a/ap", "true", gate.ApprovalOutcome("sec"))); err != nil {
		t.Fatal(err)
	}

	rec, err := eng.Planned(t.Context(), gate.PlannedRequest{Subject: sub, Principal: who, Facts: goodFacts()})
	if err != nil {
		t.Fatal(err)
	}

	p1 := gate.Principal{ID: "p1", Groups: []string{"security"}}
	if _, err = eng.Approve(t.Context(), rec.ID, gate.Approver{Principal: p1}); !errors.Is(err, gate.ErrFreshAuth) {
		t.Fatalf("no fresh auth: err = %v, want ErrFreshAuth", err)
	}

	if _, err = eng.Approve(t.Context(), rec.ID, gate.Approver{Principal: p1, ReauthenticatedNow: true}); err != nil {
		t.Fatal(err)
	}

	if _, err = eng.Approve(
		t.Context(),
		rec.ID,
		gate.Approver{Principal: p1, ReauthenticatedNow: true},
	); !errors.Is(
		err,
		gate.ErrNotApprover,
	) {
		t.Fatalf("same person twice: err = %v, want ErrNotApprover", err)
	}

	if _, err = eng.Clear(t.Context(), rec.ID); !errors.Is(err, gate.ErrNotCleared) {
		t.Fatalf("one of two: err = %v, want ErrNotCleared", err)
	}

	p2 := gate.Principal{ID: "p2", Groups: []string{"security"}}
	if _, err = eng.Approve(t.Context(), rec.ID, gate.Approver{Principal: p2, ReauthenticatedNow: true}); err != nil {
		t.Fatal(err)
	}

	if _, err = eng.Clear(t.Context(), rec.ID); err != nil {
		t.Fatalf("two of two: %v", err)
	}
}

// TestCedarFit records which conditions Cedar can and cannot say, and what
// the schema validator catches at save. The results go in the findings.
func TestCedarFit(t *testing.T) {
	t.Parallel()

	v, err := gate.NewValidator()
	if err != nil {
		t.Fatalf("validator: %v", err)
	}

	cases := []struct {
		name, cond string
		run        bool // evaluates to true on goodFacts
		saveOK     bool // type check at save passes
	}{
		{"number compare", "context.cost.deltaMonthly > 10000", true, true},
		{"glob on one string", `context.change.paths.contains("modules/network/main.tf")`, true, true},
		{"set equality", `context.change.inputKeys == ["cidr", "region"]`, true, true},
		{"containsAll", `context.change.inputKeys.containsAll(["cidr"])`, true, true},
		{"subset of an allowlist", `["cidr", "region", "zone"].containsAll(context.change.inputKeys)`, true, true},
		{"containsAny", `context.change.paths.containsAny(["modules/network/main.tf", "x"])`, true, true},
		{"principal in group", `principal in Group::"dev"`, false, true},
		{"resource attribute", `resource.collection == "prod"`, true, true},
		{"unknown fact", "context.risk.score > 3", false, false},
		{"type error", `context.cost.deltaMonthly > "high"`, false, false},
		{"unknown action fact", "context.cost.nope > 1", false, false},
		// A "has" guard on a fact that the schema does not name passes the type check
		// and never matches: the gate fails open and says nothing.
		{"guard on unknown fact", `context has risk && context.risk.score > 3`, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := gateOf("t/"+strings.ReplaceAll(tc.name, " ", "-"), tc.cond, gate.OutcomeWarn)

			saveErr := v.CheckGate(g)
			if (saveErr == nil) != tc.saveOK {
				t.Errorf("type check = %v, want ok = %v", saveErr, tc.saveOK)
			}

			eng := gate.NewEngine()
			if err := eng.Registry.PutGate(g); err != nil {
				t.Fatal(err)
			}

			rec, err := eng.Planned(t.Context(), gate.PlannedRequest{
				Subject: subject, Principal: gate.Principal{ID: "alice", Groups: []string{"ops"}}, Facts: goodFacts(),
			})
			if err != nil {
				t.Logf("run: %v", err)

				return
			}

			if got := len(rec.Matched) == 1; got != tc.run {
				t.Errorf("matched = %v, want %v", got, tc.run)
			}
		})
	}

	// There is no quantifier: "every changed path is under modules/".
	g := gateOf("t/forall", `context.change.paths.all(p, p like "modules/*")`, gate.OutcomeWarn)
	if err := gate.NewRegistry().PutGate(g); !errors.Is(err, gate.ErrInvalid) {
		t.Errorf("quantifier: err = %v, want a parse error", err)
	}
}

// BenchmarkGateEvaluation measures one Planned call for one unit: parse of
// each gate, fact document, entities, Cedar evaluation, combine and record.
func BenchmarkGateEvaluation(b *testing.B) {
	all := []gate.Gate{
		gateOf("a/cost", "context.cost.deltaMonthly > 10000", gate.OutcomeWarn),
		gateOf("a/owners", "true", gate.ApprovalOutcome("owners")),
		gateOf("a/deletes", "context.plan.delete > 0 && resource.collection == \"prod\"", gate.OutcomeDeny),
	}

	for _, n := range []int{0, 1, 3} {
		b.Run(fmt.Sprintf("gates=%d", n), func(b *testing.B) {
			eng := gate.NewEngine()

			for _, g := range all[:n] {
				if err := eng.Registry.PutGate(g); err != nil {
					b.Fatal(err)
				}
			}

			req := gate.PlannedRequest{
				Subject: subject, Principal: gate.Principal{ID: "alice", Groups: []string{"dev"}}, Facts: goodFacts(),
				Owners: []byte("[network] @group:netops 1 modules/network/**\n"),
			}

			b.ReportAllocs()

			for b.Loop() {
				if _, err := eng.Planned(b.Context(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
