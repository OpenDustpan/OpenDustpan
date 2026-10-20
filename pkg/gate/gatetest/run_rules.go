package gatetest

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

// ruleRunners returns the runners of the cases of the rules across scopes.
func ruleRunners() map[string]runner {
	return map[string]runner{
		"final-gate-of-wider-scope-shadow":    once(caseFinalShadow),
		"final-gate-of-unrelated-collection":  once(caseFinalUnrelated),
		"non-final-gate-of-wider-scope":       once(caseNonFinalWider),
		"final-gate-over-narrower-gate":       once(caseFinalOverNarrower),
		"gate-rule-must-resolve":              once(caseGateRuleResolves),
		"gate-path-sets-must-resolve":         once(caseGatePathSetsResolve),
		"final-gate-weaker-needs-reason":      once(caseFinalWeaker),
		"gate-state-not-stored":               once(caseGateState),
		"rule-of-wider-scope-shadow":          once(caseRuleWiderShadow),
		"rule-fields-must-be-valid":           once(caseRuleFieldsValid),
		"rule-over-narrower-rule":             once(caseRuleOverNarrower),
		"rule-named-by-gate-delete":           once(caseRuleNamedDelete),
		"rule-of-final-gate-needs-reason":     once(caseRuleOfFinalGate),
		"path-set-of-wider-scope-shadow":      once(casePathSetWiderShadow),
		"path-set-over-narrower-path-set":     once(casePathSetOverNarrower),
		"path-set-read-delete":                once(casePathSetReadDelete),
		"path-set-of-final-gate-needs-reason": once(casePathSetOfFinalGate),
		"ownership-path-sets-must-resolve":    once(caseOwnershipResolves),
		"concurrent-shadow":                   once(caseConcurrentShadow),
	}
}

// placement makes the collections and the units, and reports false, with a
// note, when the Domain cannot.
func (e *env) placement() bool {
	e.t.Helper()

	if _, ok := e.h.Domain.(Placement); !ok {
		e.t.Log("the Domain does not implement Placement: the checks that need a unit in a collection are skipped")

		return false
	}

	e.place()

	return true
}

func (e *env) gates() gate.Objects[gate.Gate] { return e.h.Store.Gates() }

// gate returns a sample gate of scope with the fields that mutate sets.
func (e *env) gate(scope gate.Scope, id gate.ID, mutate func(g *gate.Gate)) gate.Gate {
	e.t.Helper()

	g := sample[gate.Gate](e.t, scope, id, "one")
	if mutate != nil {
		mutate(&g)
	}

	return g
}

func final(g *gate.Gate) { g.Final = true }

// putGate stores g with the fence and the entry of its scope.
func (e *env) putGate(g gate.Gate, prev int64) (int64, error) {
	e.t.Helper()

	v, err := e.gates().Put(e.t.Context(), g, prev, e.fence(g.Scope), e.entry(g.Scope))
	if err != nil {
		return 0, fmt.Errorf("put gate: %w", err)
	}

	return v, nil
}

// mustPutGate stores g and fails the test on an error.
func (e *env) mustPutGate(g gate.Gate, prev int64) int64 {
	e.t.Helper()

	v, err := e.putGate(g, prev)
	mustNil(e.t, "Put("+g.PolicyID().String()+")", err)

	return v
}

// mustPutRule stores a sample approval rule and fails the test on an error.
func (e *env) mustPutRule(scope gate.Scope, id gate.ID) int64 {
	e.t.Helper()

	r := sample[gate.ApprovalRule](e.t, scope, id, "one")
	v, err := e.h.Store.ApprovalRules().Put(e.t.Context(), r, 0, e.fence(scope), e.entry(scope))
	mustNil(e.t, "Put(rule "+string(id)+")", err)

	return v
}

// mustPutPathSet stores a sample path set and fails the test on an error.
func (e *env) mustPutPathSet(scope gate.Scope, id gate.ID) int64 {
	e.t.Helper()

	p := sample[gate.PathSet](e.t, scope, id, "one")
	v, err := e.h.Store.PathSets().Put(e.t.Context(), p, 0, e.fence(scope), e.entry(scope))
	mustNil(e.t, "Put(path set "+string(id)+")", err)

	return v
}

func caseFinalShadow(t *testing.T, e *env) {
	t.Helper()

	e.mustPutGate(e.gate(e.fx.installation, "no-destroy", final), 0)

	for _, scope := range []gate.Scope{e.fx.acme, e.fx.prod, e.fx.webProd} {
		_, err := e.putGate(e.gate(scope, "no-destroy", nil), 0)
		wantErrText(t, "Put("+scope.Key()+")", err, gate.ErrFinal, "installation/no-destroy")
	}

	if !e.placement() {
		return
	}

	e.mustPutGate(e.gate(e.fx.prod, "prod-final", final), 0)

	_, err := e.putGate(e.gate(e.fx.webProd, "prod-final", nil), 0)
	wantErrText(t, "Put(web/prod, final gate of its collection)", err, gate.ErrFinal, "prod-final")

	_, err = e.putGate(e.gate(e.fx.acmeNet, "prod-final", nil), 0)
	mustNil(t, "Put(net, a unit that prod does not hold)", err)
}

func caseFinalUnrelated(t *testing.T, e *env) {
	t.Helper()

	if !e.placement() {
		t.Skip("the Domain does not implement Placement")
	}

	e.mustPutGate(e.gate(e.fx.staging, "no-destroy", final), 0)
	e.mustPutGate(e.gate(e.fx.webProd, "no-destroy", nil), 0)
}

func caseNonFinalWider(t *testing.T, e *env) {
	t.Helper()

	e.mustPutGate(e.gate(e.fx.acme, "cost-cap", nil), 0)
	e.mustPutGate(e.gate(e.fx.webProd, "cost-cap", nil), 0)

	e.mustPutGate(e.gate(e.fx.webProd, "other-cap", nil), 0)
	e.mustPutGate(e.gate(e.fx.acme, "other-cap", nil), 0)
}

func caseFinalOverNarrower(t *testing.T, e *env) {
	t.Helper()

	e.mustPutGate(e.gate(e.fx.webProd, "no-destroy", nil), 0)

	_, err := e.putGate(e.gate(e.fx.acme, "no-destroy", final), 0)
	wantErrText(t, "Put(final at acme)", err, gate.ErrFinal, e.fx.webProd.Key())

	v := e.mustPutGate(e.gate(e.fx.acme, "no-destroy", nil), 0)

	_, err = e.putGate(e.gate(e.fx.acme, "no-destroy", final), v)
	wantErrText(t, "Put(acme gate made final)", err, gate.ErrFinal, e.fx.webProd.Key())

	if !e.placement() {
		return
	}

	e.mustPutGate(e.gate(e.fx.webProd, "unit-gate", nil), 0)
	e.mustPutGate(e.gate(e.fx.acmeNet, "net-gate", nil), 0)

	_, err = e.putGate(e.gate(e.fx.prod, "unit-gate", final), 0)
	wantErrText(t, "Put(final at prod, unit in prod)", err, gate.ErrFinal, e.fx.webProd.Key())

	e.mustPutGate(e.gate(e.fx.prod, "net-gate", final), 0)
}

func caseGateRuleResolves(t *testing.T, e *env) {
	t.Helper()

	names := func(rule gate.ID) func(g *gate.Gate) {
		return func(g *gate.Gate) { g.Outcome = gate.ApprovalOutcome(rule) }
	}

	_, err := e.putGate(e.gate(e.fx.acme, "needs-dba", names("dba-review")), 0)
	wantErr(t, "Put(rule unknown)", err, gate.ErrInvalid)

	e.mustPutRule(e.fx.prod, "dba-prod")

	_, err = e.putGate(e.gate(e.fx.acme, "needs-dba", names("dba-prod")), 0)
	wantErr(t, "Put(rule of a narrower scope)", err, gate.ErrInvalid)

	e.mustPutGate(e.gate(e.fx.prod, "needs-dba", names("dba-prod")), 0)

	e.mustPutRule(e.fx.acme, "dba-review")
	e.mustPutGate(e.gate(e.fx.acme, "needs-dba", names("dba-review")), 0)
	e.mustPutGate(e.gate(e.fx.prod, "needs-dba-too", names("dba-review")), 0)
}

func caseGatePathSetsResolve(t *testing.T, e *env) {
	t.Helper()

	reads := func(g *gate.Gate) { g.Reads = []gate.ID{"network"} }

	_, err := e.putGate(e.gate(e.fx.acme, "network-only", reads), 0)
	wantErr(t, "Put(path set unknown)", err, gate.ErrInvalid)

	e.mustPutPathSet(e.fx.prod, "network")

	_, err = e.putGate(e.gate(e.fx.acme, "network-only", reads), 0)
	wantErr(t, "Put(path set of a narrower scope)", err, gate.ErrInvalid)

	e.mustPutGate(e.gate(e.fx.prod, "network-only", reads), 0)

	e.mustPutPathSet(e.fx.acme, "shared")
	e.mustPutGate(e.gate(e.fx.acme, "shared-only", func(g *gate.Gate) { g.Reads = []gate.ID{"shared"} }), 0)
}

func caseFinalWeaker(t *testing.T, e *env) {
	t.Helper()

	base := func(g *gate.Gate) {
		g.Final = true
		g.Mode = gate.ModeEnforce
		g.Unless = `principal.kind == "agent"`
	}

	weaker := map[string]func(g *gate.Gate){
		"switch to evaluate": func(g *gate.Gate) { g.Mode = gate.ModeEvaluate },
		"clear of final":     func(g *gate.Gate) { g.Final = false },
		"decision point":     func(g *gate.Gate) { g.DecisionPoint = gate.PointChangeApply },
		"condition":          func(g *gate.Gate) { g.Condition = "context.plan.destroy > 5" },
		"unless":             func(g *gate.Gate) { g.Unless = "" },
		"outcome":            func(g *gate.Gate) { g.Outcome = gate.OutcomeWarn },
		"on error":           func(g *gate.Gate) { g.OnError = gate.OnErrorSkip },
	}

	id := gate.ID("no-destroy")
	v := e.mustPutGate(e.gate(e.fx.acme, id, base), 0)

	for name, change := range weaker {
		next := e.gate(e.fx.acme, id, func(g *gate.Gate) { base(g); change(g) })

		_, err := e.putGate(next, v)
		wantErr(t, "Put("+name+", no reason)", err, gate.ErrFinal)
	}

	// A change that is not weaker needs no reason.
	same := e.gate(e.fx.acme, id, func(g *gate.Gate) { base(g); g.Message = "a new text" })
	v = e.mustPutGate(same, v)

	err := e.gates().
		Delete(t.Context(), gate.ObjectRef{Scope: e.fx.acme, ID: id, Version: v}, e.acme, e.entry(e.fx.acme))
	wantErr(t, "Delete(final, no reason)", err, gate.ErrFinal)

	for name, change := range weaker {
		next := e.gate(e.fx.acme, id, func(g *gate.Gate) { base(g); change(g) })

		var perr error

		v, perr = e.gates().Put(t.Context(), next, v, e.acme, e.reasonEntry(e.fx.acme, "incident 42: "+name))
		mustNil(t, "Put("+name+", reason)", perr)

		// The next round starts from the final gate again.
		v, perr = e.gates().
			Put(t.Context(), e.gate(e.fx.acme, id, base), v, e.acme, e.reasonEntry(e.fx.acme, "restore"))
		mustNil(t, "Put(restore)", perr)
	}

	err = e.gates().Delete(t.Context(), gate.ObjectRef{Scope: e.fx.acme, ID: id, Version: v}, e.acme,
		e.reasonEntry(e.fx.acme, "retired"))
	mustNil(t, "Delete(final, reason)", err)

	assertReasonKept(t, e)
}

// assertReasonKept checks that the entries of acme hold the reasons, when
// the Domain reads entries.
func assertReasonKept(t *testing.T, e *env) {
	t.Helper()

	got, ok := e.entries("acme")
	if !ok {
		return
	}

	var withReason int

	for i := range got {
		if gate.Reason(got[i]) != "" {
			withReason++
		}
	}

	if withReason == 0 {
		t.Fatalf("no audit entry holds a reason, entries = %d", len(got))
	}
}

func caseGateState(t *testing.T, e *env) {
	t.Helper()

	checked := gate.CheckedWith{
		Engine: "test/2",
		Schema: gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 3},
	}

	g := e.gate(e.fx.acme, "cost-cap", func(g *gate.Gate) {
		g.State = gate.StateInvalid
		g.CheckedWith = checked
	})
	e.mustPutGate(g, 0)

	got, err := e.gates().Get(t.Context(), e.fx.acme, "cost-cap")
	mustNil(t, "Get()", err)

	if got.State != "" {
		t.Fatalf("Get().State = %q, want the empty state", got.State)
	}

	if got.CheckedWith != checked {
		t.Fatalf("Get().CheckedWith = %+v, want %+v", got.CheckedWith, checked)
	}
}

func caseRuleWiderShadow(t *testing.T, e *env) {
	t.Helper()

	e.mustPutRule(e.fx.installation, "dba-review")

	r := sample[gate.ApprovalRule](t, e.fx.acme, "dba-review", "two")
	_, err := e.h.Store.ApprovalRules().Put(t.Context(), r, 0, e.acme, entry("acme"))
	wantErr(t, "Put(rule of a wider scope)", err, gate.ErrFinal)
}

func caseRuleOverNarrower(t *testing.T, e *env) {
	t.Helper()

	e.mustPutRule(e.fx.webProd, "dba-review")

	r := sample[gate.ApprovalRule](t, e.fx.acme, "dba-review", "two")
	_, err := e.h.Store.ApprovalRules().Put(t.Context(), r, 0, e.acme, entry("acme"))
	wantErrText(t, "Put(rule of a narrower scope)", err, gate.ErrInvalid, e.fx.webProd.Key())
}

func caseRuleNamedDelete(t *testing.T, e *env) {
	t.Helper()

	v := e.mustPutRule(e.fx.acme, "dba-review")
	e.mustPutGate(e.gate(e.fx.webProd, "dba-review", func(g *gate.Gate) {
		g.Outcome = gate.ApprovalOutcome("dba-review")
	}), 0)

	err := e.h.Store.ApprovalRules().Delete(t.Context(), gate.ObjectRef{Scope: e.fx.acme, ID: "dba-review", Version: v},
		e.acme, entry("acme"))
	wantErrText(t, "Delete(rule that a gate names)", err, gate.ErrInvalid, e.fx.webProd.Key()+"/dba-review")

	if _, gerr := e.h.Store.ApprovalRules().Get(t.Context(), e.fx.acme, "dba-review"); gerr != nil {
		t.Fatalf("Get(rule after the refused delete) = %v", gerr)
	}

	gv := int64(1)
	mustNil(
		t,
		"Delete(gate)",
		e.gates().Delete(t.Context(), gate.ObjectRef{Scope: e.fx.webProd, ID: "dba-review", Version: gv},
			e.acme, entry("acme")),
	)
	mustNil(t, "Delete(rule)", e.h.Store.ApprovalRules().Delete(t.Context(),
		gate.ObjectRef{Scope: e.fx.acme, ID: "dba-review", Version: v}, e.acme, entry("acme")))
}

func caseRuleOfFinalGate(t *testing.T, e *env) {
	t.Helper()

	v := e.mustPutRule(e.fx.acme, "dba-review")
	e.mustPutGate(e.gate(e.fx.acme, "no-destroy", func(g *gate.Gate) {
		g.Final = true
		g.Outcome = gate.ApprovalOutcome("dba-review")
	}), 0)

	r := sample[gate.ApprovalRule](t, e.fx.acme, "dba-review", "two")

	_, err := e.h.Store.ApprovalRules().Put(t.Context(), r, v, e.acme, entry("acme"))
	wantErrText(t, "Put(rule of a final gate, no reason)", err, gate.ErrFinal, "no-destroy")

	v, err = e.h.Store.ApprovalRules().Put(t.Context(), r, v, e.acme, e.reasonEntry(e.fx.acme, "new approvers"))
	mustNil(t, "Put(rule of a final gate, reason)", err)

	err = e.h.Store.ApprovalRules().Delete(t.Context(), gate.ObjectRef{Scope: e.fx.acme, ID: "dba-review", Version: v},
		e.acme, entry("acme"))
	wantErr(t, "Delete(rule of a final gate, no reason)", err, gate.ErrFinal)
}

func casePathSetWiderShadow(t *testing.T, e *env) {
	t.Helper()

	e.mustPutPathSet(e.fx.acme, "network")

	p := sample[gate.PathSet](t, e.fx.prod, "network", "two")
	_, err := e.h.Store.PathSets().Put(t.Context(), p, 0, e.acme, entry("acme"))
	wantErrText(t, "Put(path set of a wider scope)", err, gate.ErrInvalid, e.fx.acme.Key())
}

func casePathSetOverNarrower(t *testing.T, e *env) {
	t.Helper()

	e.mustPutPathSet(e.fx.prod, "network")

	p := sample[gate.PathSet](t, e.fx.acme, "network", "two")
	_, err := e.h.Store.PathSets().Put(t.Context(), p, 0, e.acme, entry("acme"))
	wantErrText(t, "Put(path set of a narrower scope)", err, gate.ErrInvalid, e.fx.prod.Key())
}

func casePathSetReadDelete(t *testing.T, e *env) {
	t.Helper()

	v := e.mustPutPathSet(e.fx.acme, "network")

	reads := []gate.ID{"network"}
	gv := e.mustPutGate(e.gate(e.fx.acme, "network-only", func(g *gate.Gate) { g.Reads = reads }), 0)

	d := gate.ApprovalDefault{
		Scope: e.fx.acme, Version: 0, Count: 1, FreshAuth: gate.NoFreshAuth(), Approvers: gate.Approvers{
			Groups: []string{"sre"}, Users: nil, UnitOwners: false,
		},
		Unless: "", Reads: reads,
	}
	dv, err := e.h.Store.Settings().PutApprovalDefault(t.Context(), d, 0, e.acme, entry("acme"))
	mustNil(t, "PutApprovalDefault()", err)

	o := sample[gate.OwnershipRule](t, e.fx.prod, "owners-net", "one")
	o.Reads = reads
	ov, err := e.h.Store.OwnershipRules().Put(t.Context(), o, 0, e.acme, entry("acme"))
	mustNil(t, "Put(ownership rule)", err)

	del := func() error {
		return e.h.Store.PathSets().Delete(t.Context(), gate.ObjectRef{Scope: e.fx.acme, ID: "network", Version: v},
			e.acme, entry("acme"))
	}

	err = del()
	wantErrText(t, "Delete(path set that three objects read)", err, gate.ErrInvalid,
		"network-only", "approval-default", "owners-net")

	if _, gerr := e.h.Store.PathSets().Get(t.Context(), e.fx.acme, "network"); gerr != nil {
		t.Fatalf("Get(path set after the refused delete) = %v", gerr)
	}

	mustNil(
		t,
		"Delete(gate)",
		e.gates().Delete(t.Context(), gate.ObjectRef{Scope: e.fx.acme, ID: "network-only", Version: gv},
			e.acme, entry("acme")),
	)
	wantErrText(t, "Delete(path set that two objects read)", del(), gate.ErrInvalid, "approval-default", "owners-net")

	mustNil(t, "DeleteApprovalDefault()",
		e.h.Store.Settings().DeleteApprovalDefault(t.Context(), e.fx.acme, dv, e.acme, entry("acme")))
	wantErrText(t, "Delete(path set that one object reads)", del(), gate.ErrInvalid, "owners-net")

	mustNil(t, "Delete(ownership rule)", e.h.Store.OwnershipRules().Delete(t.Context(),
		gate.ObjectRef{Scope: e.fx.prod, ID: "owners-net", Version: ov}, e.acme, entry("acme")))
	mustNil(t, "Delete(path set)", del())
}

func casePathSetOfFinalGate(t *testing.T, e *env) {
	t.Helper()

	v := e.mustPutPathSet(e.fx.acme, "network")
	e.mustPutGate(e.gate(e.fx.acme, "network-only", func(g *gate.Gate) {
		g.Final = true
		g.Reads = []gate.ID{"network"}
	}), 0)

	p := sample[gate.PathSet](t, e.fx.acme, "network", "two")

	_, err := e.h.Store.PathSets().Put(t.Context(), p, v, e.acme, entry("acme"))
	wantErrText(t, "Put(path set of a final gate, no reason)", err, gate.ErrFinal, "network-only")

	_, err = e.h.Store.PathSets().Put(t.Context(), p, v, e.acme, e.reasonEntry(e.fx.acme, "wider globs"))
	mustNil(t, "Put(path set of a final gate, reason)", err)
}

func caseOwnershipResolves(t *testing.T, e *env) {
	t.Helper()

	o := sample[gate.OwnershipRule](t, e.fx.prod, "owners-net", "one")
	o.Reads = []gate.ID{"network"}

	_, err := e.h.Store.OwnershipRules().Put(t.Context(), o, 0, e.acme, entry("acme"))
	wantErr(t, "Put(path set unknown)", err, gate.ErrInvalid)

	e.mustPutPathSet(e.fx.acme, "network")

	_, err = e.h.Store.OwnershipRules().Put(t.Context(), o, 0, e.acme, entry("acme"))
	mustNil(t, "Put(path set of a wider scope)", err)
}

func caseConcurrentShadow(t *testing.T, e *env) {
	t.Helper()

	const rounds = 12

	for i := range rounds {
		id := gate.ID(fmt.Sprintf("race%d", i))
		wide := e.gate(e.fx.acme, id, final)
		narrow := e.gate(e.fx.webProd, id, nil)

		errs := concurrently(
			func() error { _, err := e.putGate(wide, 0); return err },
			func() error { _, err := e.putGate(narrow, 0); return err },
		)

		committed := 0

		for _, err := range errs {
			if err == nil {
				committed++

				continue
			}

			wantErr(t, "Put("+string(id)+", concurrent)", err, gate.ErrFinal)
		}

		if committed != 1 {
			t.Fatalf(
				"round %d: %d of the final gate and the gate with its ID committed, want 1 (%v)",
				i,
				committed,
				errs,
			)
		}
	}
}

func caseDefaultRoundTrip(t *testing.T, e *env) {
	t.Helper()

	approvers := gate.Approvers{Groups: []string{"sre", "dba"}, Users: []string{"alice"}, UnitOwners: true}

	for _, scope := range []gate.Scope{e.fx.acme, e.fx.prod, e.fx.webProd} {
		fa, err := gate.ParseFreshAuth(120)
		mustNil(t, "ParseFreshAuth()", err)

		d := gate.ApprovalDefault{
			Scope: scope, Version: 0, Count: 2, FreshAuth: fa, Approvers: approvers,
			Unless: `context.change.paths.size() == 0`, Reads: nil,
		}
		s := e.h.Store.Settings()

		v, err := s.PutApprovalDefault(t.Context(), d, 0, e.fence(scope), e.entry(scope))
		mustNil(t, "PutApprovalDefault("+scope.Key()+")", err)

		if v != 1 {
			t.Fatalf("PutApprovalDefault(new) = version %d, want 1", v)
		}

		got, err := s.ApprovalDefault(t.Context(), scope)
		mustNil(t, "ApprovalDefault("+scope.Key()+")", err)

		d.Version = 1
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", d) {
			t.Fatalf("ApprovalDefault(%s) = %+v, want %+v", scope, got, d)
		}

		_, err = s.PutApprovalDefault(t.Context(), d, 0, e.fence(scope), e.entry(scope))
		wantErr(t, "PutApprovalDefault(prev 0, exists)", err, gate.ErrConflict)

		_, err = s.PutApprovalDefault(t.Context(), d, 5, e.fence(scope), e.entry(scope))
		wantErr(t, "PutApprovalDefault(stale prev)", err, gate.ErrConflict)

		d.Count = 3

		next, err := s.PutApprovalDefault(t.Context(), d, v, e.fence(scope), e.entry(scope))
		mustNil(t, "PutApprovalDefault(replace)", err)

		if next != v+1 {
			t.Fatalf("PutApprovalDefault(replace) = version %d, want %d", next, v+1)
		}
	}

	_, err := e.h.Store.Settings().PutApprovalDefault(t.Context(),
		gate.ApprovalDefault{
			Scope: e.fx.globex, Version: 0, Count: 1, FreshAuth: gate.NoFreshAuth(), Approvers: approvers, Unless: "",
			Reads: nil,
		}, 4, e.globex, entry("globex"))
	wantErr(t, "PutApprovalDefault(prev above 0, none)", err, gate.ErrNotFound)

	e.wantEntries("acme", 6)
}

func caseDefaultResolves(t *testing.T, e *env) {
	t.Helper()

	d := gate.ApprovalDefault{
		Scope: e.fx.prod, Version: 0, Count: 1, FreshAuth: gate.NoFreshAuth(), Approvers: gate.Approvers{
			Groups: nil, Users: nil, UnitOwners: false,
		},
		Unless: "", Reads: []gate.ID{"network"},
	}

	_, err := e.h.Store.Settings().PutApprovalDefault(t.Context(), d, 0, e.acme, entry("acme"))
	wantErr(t, "PutApprovalDefault(path set unknown)", err, gate.ErrInvalid)

	e.mustPutPathSet(e.fx.acme, "network")

	_, err = e.h.Store.Settings().PutApprovalDefault(t.Context(), d, 0, e.acme, entry("acme"))
	mustNil(t, "PutApprovalDefault(path set of a wider scope)", err)
}

func caseRuleFieldsValid(t *testing.T, e *env) {
	t.Helper()

	tests := []struct {
		name   string
		mutate func(r *gate.ApprovalRule)
	}{
		{"count zero", func(r *gate.ApprovalRule) { r.Count = 0 }},
		{"count above ten", func(r *gate.ApprovalRule) { r.Count = gate.MaxApprovalCount + 1 }},
		{
			"no approver",
			func(r *gate.ApprovalRule) { r.Approvers = gate.Approvers{Groups: nil, Users: nil, UnitOwners: false} },
		},
		{"empty approver name", func(r *gate.ApprovalRule) {
			r.Approvers = gate.Approvers{Groups: []string{""}, Users: nil, UnitOwners: false}
		}},
		{"window below 60", func(r *gate.ApprovalRule) { r.FreshAuth = gate.FreshAuth{Set: true, Seconds: 59} }},
		{"window above 900", func(r *gate.ApprovalRule) { r.FreshAuth = gate.FreshAuth{Set: true, Seconds: 901} }},
		{"timeout below an hour", func(r *gate.ApprovalRule) { r.Timeout = gate.MinApprovalTimeout - 1 }},
		{"timeout above 30 days", func(r *gate.ApprovalRule) { r.Timeout = gate.MaxApprovalTimeout + 1 }},
	}

	for _, tt := range tests {
		r := sample[gate.ApprovalRule](t, e.fx.acme, "dba-review", "one")
		tt.mutate(&r)

		_, err := e.h.Store.ApprovalRules().Put(t.Context(), r, 0, e.acme, entry("acme"))
		wantErr(t, "Put(rule with "+tt.name+")", err, gate.ErrInvalid)
	}

	rules, err := e.h.Store.ApprovalRules().List(t.Context(), e.fx.acme)
	mustNil(t, "List(after the refused puts)", err)

	if len(rules) != 0 {
		t.Fatalf("List(after the refused puts) = %d rules, want none", len(rules))
	}

	e.wantEntries("acme", 0)
}
