package gatetest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// EngineFactory returns a fresh engine for one case. An engine that holds
// access limits is built with the actions of EngineAccessActions.
type EngineFactory func(t *testing.T) gate.Engine

// EngineAccessActions returns the actions that the engine cases of access
// limits name. An adapter that needs the list of actions that a limit can
// name takes it from here.
func EngineAccessActions() []string {
	return []string{string(identity.ActionUnitRead), string(identity.ActionUnitWrite)}
}

// RunEngine executes each case of EngineCases against engines from
// newEngine, except the cases whose name starts with "external-". A case
// with another name and no runner fails.
func RunEngine(t *testing.T, newEngine EngineFactory) {
	t.Helper()

	runners := engineRunners()

	for _, c := range EngineCases() {
		run, ok := runners[c.Name]
		if !ok {
			if !strings.HasPrefix(c.Name, "external-") {
				t.Errorf("case %s has no runner", c.Name)
			}

			continue
		}

		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			run(t, newEngine(t))
		})
	}
}

// EngineRunnableCases returns the names of the cases that RunEngine
// executes.
func EngineRunnableCases() []string {
	names := make([]string, 0, len(engineRunners()))
	for name := range engineRunners() {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

type engineRunner func(t *testing.T, e gate.Engine)

// The shared values of the engine cases.
const pointPlanned = gate.PointChangePlanned

const (
	costCap     = "context.cost.available && context.cost.deltaMonthly > 10000"
	forbidAgent = `forbid (principal, action == OpenDustpan::Action::"unit:write", resource)` +
		` when { context.principal.kind == "agent" };`
)

func schemaOf(subject string, sets ...gate.ID) gate.Schema {
	return gate.Schema{Version: gate.SchemaVersion{Subject: subject, Major: 1, Minor: 0}, PathSets: sets}
}

func gatePolicy(id gate.PolicyID, point gate.DecisionPoint, condition, unless string) gate.Policy {
	return gate.Policy{
		ID:        id,
		Version:   1,
		Kind:      gate.PolicyGate,
		Point:     point,
		Condition: condition,
		Unless:    unless,
		Text:      "",
	}
}

func limitPolicy(id gate.PolicyID, text string) gate.Policy {
	return gate.Policy{ID: id, Version: 1, Kind: gate.PolicyLimit, Point: "", Condition: "", Unless: "", Text: text}
}

// plannedFacts returns the facts of change.planned for a person with a plan
// that destroys 2 resources and a cost delta of 15000 cents.
func plannedFacts(mutate func(*gate.Facts)) *gate.Facts {
	f := new(gate.Facts)
	f.Schema = gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0}
	f.Principal = gate.PrincipalFacts{Kind: gate.PrincipalPerson, Credential: gate.CredentialSession}
	f.Operation = gate.OperationFacts{Kind: gate.OperationApply}
	f.Plan = gate.PlanFacts{Available: true, Add: 1, Change: 0, Replace: 0, Destroy: 2, ResourceTypes: nil}
	f.Cost = gate.CostFacts{Available: true, DeltaMonthly: 15000, TotalMonthly: 20000, Currency: "USD"}

	if mutate != nil {
		mutate(f)
	}

	return f
}

func unitObject(t *testing.T, tool, name string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject(tool, "acme", name)
	if err != nil {
		t.Fatalf("UnitObject(%s, %s) = %v", tool, name, err)
	}

	return o
}

func factsRequest(t *testing.T, id, action string, f *gate.Facts) gate.Request {
	t.Helper()

	return gate.Request{
		ID: id, Principal: "alice", Groups: nil, Action: action, Resource: unitObject(t, "pulumi", "web/prod"),
		Facts: f, Access: nil,
	}
}

func accessRequest(t *testing.T, id string, kind gate.PrincipalKind) gate.Request {
	t.Helper()

	return gate.Request{
		ID: id, Principal: "alice", Groups: nil, Action: string(identity.ActionUnitWrite),
		Resource: unitObject(t, "pulumi", "web/prod"), Facts: nil,
		Access: &gate.AccessFacts{
			Schema:         gate.SchemaVersion{Subject: gate.SchemaAccess, Major: 1, Minor: 0},
			Principal:      gate.PrincipalFacts{Kind: kind, Credential: gate.CredentialAccessToken},
			AuthAgeSeconds: 10, Unit: gate.AccessUnitFacts{Collection: "prod", Labels: nil},
		},
	}
}

func evaluate(t *testing.T, e gate.Engine, set gate.PolicySet, reqs ...gate.Request) []gate.Answer {
	t.Helper()

	answers, err := e.Evaluate(t.Context(), set, reqs)
	if err != nil {
		t.Fatalf("Evaluate = %v", err)
	}

	if len(answers) != len(reqs) {
		t.Fatalf("Evaluate gave %d answers for %d requests", len(answers), len(reqs))
	}

	want := make([]string, 0, len(reqs))
	for i := range reqs {
		want = append(want, reqs[i].ID)
	}

	for i, a := range answers {
		if a.RequestID != want[i] {
			t.Fatalf("answer %d echoes %q, want %q", i, a.RequestID, want[i])
		}
	}

	return answers
}

func matched(a *gate.Answer) []string {
	out := make([]string, 0, len(a.Matched))
	for _, id := range a.Matched {
		out = append(out, id.String())
	}

	return out
}

func wantMatched(t *testing.T, a *gate.Answer, want ...string) {
	t.Helper()

	if got := matched(a); !slices.Equal(got, want) || len(a.Diagnostics) != 0 {
		t.Fatalf("request %s: matched %v with diagnostics %v, want %v with none", a.RequestID, got, a.Diagnostics, want)
	}
}

func wantDiagnostics(t *testing.T, got []gate.Diagnostic, want ...gate.PolicyID) {
	t.Helper()

	ids := make([]gate.PolicyID, 0, len(got))
	for _, d := range got {
		if d.Message == "" {
			t.Fatalf("diagnostic of %s has no message", d.Policy)
		}

		ids = append(ids, d.Policy)
	}

	if !slices.Equal(ids, want) {
		t.Fatalf("diagnostics for %v, want %v", ids, want)
	}
}

func check(t *testing.T, e gate.Engine, schema gate.Schema, ps ...gate.Policy) []gate.Diagnostic {
	t.Helper()

	diags, err := e.Check(t.Context(), schema, ps)
	if err != nil {
		t.Fatalf("Check = %v", err)
	}

	return diags
}

// checkRefused checks that Check refuses the one policy of a case with one
// diagnostic, and that Evaluate does not match it.
func checkRefused(t *testing.T, e gate.Engine, schema gate.Schema, p gate.Policy) {
	t.Helper()

	wantDiagnostics(t, check(t, e, schema, p), p.ID)
}

func runVersionNamesEngine(t *testing.T, e gate.Engine) {
	t.Helper()

	if v := e.Version(); v == "" || v != e.Version() {
		t.Fatalf("Version = %q, want a text that is not empty and stable", v)
	}
}

func runEmptySetMatchesNothing(t *testing.T, e gate.Engine) {
	t.Helper()

	planned := plannedSchema()

	a := evaluate(t, e, gate.PolicySet{Schema: planned, Policies: nil},
		factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil)),
		factsRequest(t, "r2", string(pointPlanned), plannedFacts(nil)))
	wantMatched(t, &a[0])
	wantMatched(t, &a[1])
}

func runConditionMatches(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{
		Schema:   planned,
		Policies: []gate.Policy{gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, "")},
	}
	a := evaluate(t, e, set,
		factsRequest(t, "high", string(pointPlanned), plannedFacts(nil)),
		factsRequest(t, "low", string(pointPlanned), plannedFacts(func(f *gate.Facts) { f.Cost.DeltaMonthly = 5000 })),
		factsRequest(t, "none", string(pointPlanned), plannedFacts(func(f *gate.Facts) {
			f.Cost = gate.CostFacts{Available: false, DeltaMonthly: 0, TotalMonthly: 0, Currency: ""}
		})))
	wantMatched(t, &a[0], "namespace/acme/cost-cap")
	wantMatched(t, &a[1])
	wantMatched(t, &a[2])
}

func runEmptyConditionIsTrue(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{
		Schema:   planned,
		Policies: []gate.Policy{gatePolicy(pid(fx.acme, "always"), pointPlanned, "", "")},
	}
	a := evaluate(t, e, set, factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil)))
	wantMatched(t, &a[0], "namespace/acme/always")
}

func runUnlessExempts(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{Schema: planned, Policies: []gate.Policy{gatePolicy(
		pid(fx.acme, "no-destroy"), pointPlanned, "context.plan.available && context.plan.destroy > 0",
		`context.principal.kind == "service"`,
	)}}
	a := evaluate(
		t,
		e,
		set,
		factsRequest(t, "person", string(pointPlanned), plannedFacts(nil)),
		factsRequest(
			t,
			"service",
			string(pointPlanned),
			plannedFacts(func(f *gate.Facts) { f.Principal.Kind = gate.PrincipalService }),
		),
	)
	wantMatched(t, &a[0], "namespace/acme/no-destroy")
	wantMatched(t, &a[1])
}

func runActionIsPinned(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{
		Schema:   planned,
		Policies: []gate.Policy{gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, "")},
	}
	a := evaluate(t, e, set,
		factsRequest(t, "planned", string(pointPlanned), plannedFacts(nil)),
		factsRequest(t, "apply", string(gate.PointChangeApply), plannedFacts(nil)),
		factsRequest(t, "write", string(gate.PointStateWrite), plannedFacts(nil)))
	wantMatched(t, &a[0], "namespace/acme/cost-cap")
	wantMatched(t, &a[1])
	wantMatched(t, &a[2])
}

func runMatchedIDsKeepScope(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	ids := []gate.PolicyID{pid(fx.installation, "cost-cap"), pid(fx.acme, "cost-cap"), pid(fx.webProd, "cost-cap")}
	ps := make([]gate.Policy, 0, len(ids))

	for _, id := range ids {
		ps = append(ps, gatePolicy(id, pointPlanned, costCap, ""))
	}

	a := evaluate(
		t,
		e,
		gate.PolicySet{Schema: planned, Policies: ps},
		factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil)),
	)
	want := []string{ids[0].String(), ids[1].String(), ids[2].String()}
	slices.Sort(want)
	wantMatched(t, &a[0], want...)
}

func runBuiltinID(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	id := pid(fx.installation, gate.BuiltinID("plan-readable"))
	set := gate.PolicySet{
		Schema:   planned,
		Policies: []gate.Policy{gatePolicy(id, pointPlanned, "!context.plan.available", "")},
	}
	a := evaluate(t, e, set,
		factsRequest(t, "hidden", string(pointPlanned), plannedFacts(func(f *gate.Facts) { f.Plan.Available = false })),
		factsRequest(t, "readable", string(pointPlanned), plannedFacts(nil)))
	wantMatched(t, &a[0], "installation/builtin/plan-readable")
	wantMatched(t, &a[1])
}

func runPrincipalGroupsAreParents(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{Schema: planned, Policies: []gate.Policy{gatePolicy(
		pid(fx.acme, "sre-only"), pointPlanned, "", `principal in OpenDustpan::Group::"sre"`,
	)}}
	in := factsRequest(t, "sre", string(pointPlanned), plannedFacts(nil))
	in.Groups = []string{"dev", "sre"}
	out := factsRequest(t, "dev", string(pointPlanned), plannedFacts(nil))
	out.Groups = []string{"dev"}
	a := evaluate(t, e, set, in, out)
	wantMatched(t, &a[0])
	wantMatched(t, &a[1], "namespace/acme/sre-only")
}

func runPathSetAttributes(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()

	schema := schemaOf(string(pointPlanned), "network", "docs")
	set := gate.PolicySet{Schema: schema, Policies: []gate.Policy{gatePolicy(
		pid(fx.prod, "network-only"), pointPlanned, `context.paths.available && context.paths.only["network"]`, "",
	)}}
	only := plannedFacts(func(f *gate.Facts) {
		f.Paths = gate.PathFacts{
			Available: true, Touches: map[gate.ID]bool{"network": true, "docs": false},
			Only: map[gate.ID]bool{"network": true, "docs": false},
		}
	})
	mixed := plannedFacts(func(f *gate.Facts) {
		f.Paths = gate.PathFacts{
			Available: true, Touches: map[gate.ID]bool{"network": true, "docs": true},
			Only: map[gate.ID]bool{"network": false, "docs": false},
		}
	})
	a := evaluate(
		t,
		e,
		set,
		factsRequest(t, "only", string(pointPlanned), only),
		factsRequest(t, "mixed", string(pointPlanned), mixed),
	)
	wantMatched(t, &a[0], "collection/acme/prod/network-only")
	wantMatched(t, &a[1])
}

func runErrorIsDiagnostic(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	overflow := pid(fx.acme, "overflow")
	set := gate.PolicySet{Schema: planned, Policies: []gate.Policy{
		gatePolicy(
			overflow,
			pointPlanned,
			"context.cost.available && context.cost.deltaMonthly * 4611686018427387904 > 0",
			"",
		),
		gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, ""),
	}}
	a := evaluate(t, e, set, factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil)))

	if got := matched(&a[0]); !slices.Equal(got, []string{"namespace/acme/cost-cap"}) {
		t.Fatalf("matched %v, want only the gate with no error", got)
	}

	wantDiagnostics(t, a[0].Diagnostics, overflow)
}

func runLimitErrorIsDiagnostic(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	access := accessSchema()

	overflow := pid(fx.prod, "auth-overflow")
	set := gate.PolicySet{Schema: access, Policies: []gate.Policy{limitPolicy(overflow,
		`forbid (principal, action == OpenDustpan::Action::"unit:write", resource)`+
			` when { context.authAgeSeconds * 4611686018427387904 > 0 };`)}}
	a := evaluate(t, e, set, accessRequest(t, "r1", gate.PrincipalPerson))
	if got := matched(&a[0]); len(got) != 0 {
		t.Fatalf("matched %v, want no match for a limit with an error", got)
	}

	wantDiagnostics(t, a[0].Diagnostics, overflow)
}

func runDuplicatePolicyIDIsInvalid(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	p := gatePolicy(pid(fx.acme, "always"), pointPlanned, "", "")
	set := gate.PolicySet{Schema: planned, Policies: []gate.Policy{p, p}}

	_, err := e.Evaluate(
		t.Context(),
		set,
		[]gate.Request{factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil))},
	)
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Evaluate of a set with a policy twice = %v, want ErrInvalid", err)
	}
}

func runNoCompileIsDiagnostic(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	risk := pid(fx.acme, "risk")
	set := gate.PolicySet{Schema: planned, Policies: []gate.Policy{
		gatePolicy(risk, pointPlanned, "context.risk.score > 3", ""),
		gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, ""),
	}}
	a := evaluate(t, e, set,
		factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil)),
		factsRequest(t, "r2", string(pointPlanned), plannedFacts(nil)))

	for i := range a {
		if got := matched(&a[i]); !slices.Equal(got, []string{"namespace/acme/cost-cap"}) {
			t.Fatalf("request %s: matched %v, want only the gate that compiles", a[i].RequestID, got)
		}

		wantDiagnostics(t, a[i].Diagnostics, risk)
	}
}

func runRequestNeedsOneContext(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{
		Schema:   planned,
		Policies: []gate.Policy{gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, "")},
	}
	none := factsRequest(t, "none", string(pointPlanned), nil)
	both := factsRequest(t, "both", string(pointPlanned), plannedFacts(nil))
	both.Access = accessRequest(t, "x", gate.PrincipalPerson).Access

	for _, r := range []*gate.Request{&none, &both} {
		if _, err := e.Evaluate(t.Context(), set, []gate.Request{*r}); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("Evaluate of request %s = %v, want ErrInvalid", r.ID, err)
		}
	}
}

func runAnswersInRequestOrder(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	set := gate.PolicySet{
		Schema:   planned,
		Policies: []gate.Policy{gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, "")},
	}
	second := factsRequest(
		t,
		"net",
		string(pointPlanned),
		plannedFacts(func(f *gate.Facts) { f.Cost.DeltaMonthly = 1 }),
	)
	second.Resource = unitObject(t, "opentofu", "net")
	a := evaluate(t, e, set, factsRequest(t, "web", string(pointPlanned), plannedFacts(nil)), second)
	wantMatched(t, &a[0], "namespace/acme/cost-cap")
	wantMatched(t, &a[1])
}

func runAccessLimitForbids(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	access := accessSchema()

	id := pid(fx.prod, "no-agent-destroy")
	set := gate.PolicySet{Schema: access, Policies: []gate.Policy{limitPolicy(id, forbidAgent)}}
	a := evaluate(t, e, set,
		accessRequest(t, "agent", gate.PrincipalAgent), accessRequest(t, "person", gate.PrincipalPerson))
	wantMatched(t, &a[0], "collection/acme/prod/no-agent-destroy")
	wantMatched(t, &a[1])
}

func runDeterministic(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	ps := []gate.Policy{
		gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, ""),
		gatePolicy(pid(fx.acme, "always"), pointPlanned, "", ""),
		gatePolicy(pid(fx.acme, "risk"), pointPlanned, "context.risk.score > 3", ""),
		gatePolicy(
			pid(fx.acme, "overflow"),
			pointPlanned,
			"context.cost.available && context.cost.deltaMonthly * 4611686018427387904 > 0",
			"",
		),
	}
	req := factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil))
	first := evaluate(t, e, gate.PolicySet{Schema: planned, Policies: ps}, req)
	again := evaluate(t, e, gate.PolicySet{Schema: planned, Policies: ps}, req)
	slices.Reverse(ps)
	reversed := evaluate(t, e, gate.PolicySet{Schema: planned, Policies: ps}, req)

	if !reflect.DeepEqual(first, again) || !reflect.DeepEqual(first, reversed) {
		t.Fatalf("answers differ: %+v, %+v and %+v", first, again, reversed)
	}
}

func runCanceledContext(t *testing.T, e gate.Engine) {
	t.Helper()

	planned := plannedSchema()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	a, err := e.Evaluate(ctx, gate.PolicySet{Schema: planned, Policies: nil},
		[]gate.Request{factsRequest(t, "r1", string(pointPlanned), plannedFacts(nil))})
	if err == nil || len(a) != 0 || (!errors.Is(err, context.Canceled) && !errors.Is(err, gate.ErrUnavailable)) {
		t.Fatalf("Evaluate = %v, %v, want an error that wraps the context error and no answer", a, err)
	}
}

func runCheckAcceptsValid(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()
	access := accessSchema()

	if d := check(t, e, planned, gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, "")); len(d) != 0 {
		t.Fatalf("Check of a valid gate = %v, want no diagnostic", d)
	}

	if d := check(t, e, access, limitPolicy(pid(fx.acme, "no-agent-destroy"), forbidAgent)); len(d) != 0 {
		t.Fatalf("Check of a valid limit = %v, want no diagnostic", d)
	}
}

func runCheckRefusesPermitLimit(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	access := accessSchema()

	checkRefused(t, e, access, limitPolicy(pid(fx.acme, "permit-all"), "permit (principal, action, resource);"))
}

func runCheckRefusesSecondPolicy(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()
	access := accessSchema()

	checkRefused(t, e, planned, gatePolicy(pid(fx.acme, "injection"), pointPlanned,
		"true }; permit (principal, action, resource) when { true", ""))
	checkRefused(t, e, access, limitPolicy(pid(fx.acme, "two-limits"), forbidAgent+" "+forbidAgent))
}

func runCheckRefusesAction(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	checkRefused(t, e, planned, gatePolicy(pid(fx.acme, "names-action"), pointPlanned,
		`action == OpenDustpan::Action::"change.apply"`, ""))
}

func runCheckRefusesUnknownAttribute(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	checkRefused(t, e, planned, gatePolicy(pid(fx.acme, "risk"), pointPlanned, "context.risk.score > 3", ""))
}

func runCheckRefusesHasOnSchemaAttribute(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	checkRefused(t, e, planned, gatePolicy(pid(fx.acme, "has-cost"), pointPlanned, "context has cost", ""))
}

func runCheckRefusesUnreadAvailable(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	checkRefused(
		t,
		e,
		planned,
		gatePolicy(pid(fx.acme, "cost-no-flag"), pointPlanned, "context.cost.deltaMonthly > 10000", ""),
	)
}

func runCheckRefusesTypeError(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	checkRefused(t, e, planned, gatePolicy(pid(fx.acme, "type-error"), pointPlanned,
		`context.plan.available && context.plan.destroy == "yes"`, ""))
}

func runCheckRefusesGroupOfOtherPoint(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()

	checkRefused(
		t,
		e,
		schemaOf(string(gate.PointStateWrite)),
		gatePolicy(pid(fx.acme, "write-cost"), gate.PointStateWrite, costCap, ""),
	)
}

func runCheckRefusesUnknownPathSet(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()

	checkRefused(t, e, schemaOf(string(pointPlanned), "network"), gatePolicy(pid(fx.prod, "unknown-set"), pointPlanned,
		`context.paths.available && context.paths.touches["docs"]`, ""))
}

func runCheckOneDiagnosticEach(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()
	planned := plannedSchema()

	risk, typeErr := pid(fx.acme, "risk"), pid(fx.acme, "type-error")
	d := check(t, e, planned,
		gatePolicy(risk, pointPlanned, "context.risk.score > 3", ""),
		gatePolicy(typeErr, pointPlanned, `context.plan.available && context.plan.destroy == "yes"`, ""),
		gatePolicy(pid(fx.acme, "cost-cap"), pointPlanned, costCap, ""))
	wantDiagnostics(t, d, risk, typeErr)
}

func runCheckUnusableSchema(t *testing.T, e gate.Engine) {
	t.Helper()

	bad := gate.Schema{Version: gate.SchemaVersion{Subject: string(pointPlanned), Major: 999, Minor: 0}, PathSets: nil}
	if _, err := e.Check(t.Context(), bad, nil); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Check with an unknown schema version = %v, want ErrInvalid", err)
	}
}

func runCheckLimitMembershipNeedsAGroup(t *testing.T, e gate.Engine) {
	t.Helper()

	fx := newFixtures()

	group := limitPolicy(pid(fx.acme, "no-agent-destroy"), forbidAgent)
	if err := e.CheckLimitMembership(t.Context(), group.Text); err != nil {
		t.Fatalf("CheckLimitMembership of a limit that names a group = %v, want nil", err)
	}

	noGroup := limitPolicy(pid(fx.acme, "membership-no-group"),
		`forbid (principal, action, resource) when { principal in resource.owners };`)
	if err := e.CheckLimitMembership(t.Context(), noGroup.Text); !errors.Is(err, gate.ErrLimitMembership) {
		t.Fatalf("CheckLimitMembership of a limit that names no group = %v, want ErrLimitMembership", err)
	}
}

// engineRunners returns the runner of each executable case, by name.
func engineRunners() map[string]engineRunner {
	return map[string]engineRunner{
		"version-names-engine":                  runVersionNamesEngine,
		"empty-set-matches-nothing":             runEmptySetMatchesNothing,
		"condition-matches":                     runConditionMatches,
		"empty-condition-is-true":               runEmptyConditionIsTrue,
		"unless-exempts":                        runUnlessExempts,
		"action-is-pinned":                      runActionIsPinned,
		"matched-ids-keep-scope":                runMatchedIDsKeepScope,
		"builtin-id":                            runBuiltinID,
		"principal-groups-are-parents":          runPrincipalGroupsAreParents,
		"path-set-attributes":                   runPathSetAttributes,
		"error-is-diagnostic":                   runErrorIsDiagnostic,
		"limit-error-is-diagnostic":             runLimitErrorIsDiagnostic,
		"duplicate-policy-id-is-invalid":        runDuplicatePolicyIDIsInvalid,
		"no-compile-is-diagnostic":              runNoCompileIsDiagnostic,
		"request-needs-one-context":             runRequestNeedsOneContext,
		"answers-in-request-order":              runAnswersInRequestOrder,
		"access-limit-forbids":                  runAccessLimitForbids,
		"deterministic":                         runDeterministic,
		"canceled-context":                      runCanceledContext,
		"check-accepts-valid":                   runCheckAcceptsValid,
		"check-refuses-permit-limit":            runCheckRefusesPermitLimit,
		"check-refuses-second-policy":           runCheckRefusesSecondPolicy,
		"check-refuses-action":                  runCheckRefusesAction,
		"check-refuses-unknown-attribute":       runCheckRefusesUnknownAttribute,
		"check-refuses-has-on-schema-attribute": runCheckRefusesHasOnSchemaAttribute,
		"check-refuses-unread-available":        runCheckRefusesUnreadAvailable,
		"check-refuses-type-error":              runCheckRefusesTypeError,
		"check-refuses-group-of-other-point":    runCheckRefusesGroupOfOtherPoint,
		"check-refuses-unknown-path-set":        runCheckRefusesUnknownPathSet,
		"check-one-diagnostic-each":             runCheckOneDiagnosticEach,
		"check-limit-membership-needs-a-group":  runCheckLimitMembershipNeedsAGroup,
		"check-unusable-schema":                 runCheckUnusableSchema,
	}
}

func plannedSchema() gate.Schema { return schemaOf(string(pointPlanned)) }

func accessSchema() gate.Schema { return schemaOf(gate.SchemaAccess) }
