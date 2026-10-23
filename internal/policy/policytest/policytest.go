// Package policytest is the conformance suite for policy.Engine adapters.
// Each adapter runs it against a real engine in its own test: an OPA
// server, the Cedar library, or a webhook receiver in the test.
//
//	func TestEngineConformance(t *testing.T) {
//		t.Parallel()
//		policytest.Run(t, kinds, func(t *testing.T, p policytest.Policy) policy.Engine {
//			return newEngineWith(t, p)
//		})
//	}
//
// The adapter writes each policy of the fixture set in its own language.
// The suite comes before the first adapter. A reference engine in memory,
// in the tests of this package, runs it.
package policytest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/policy"
)

// Policy names one policy of the fixture set. Each adapter writes it in the
// language of its engine.
type Policy string

// The fixture set. The documents of the suite are JSON objects with the
// field blueprint.name, as the document of the kind catalog.request has.
const (
	// PolicyAllowAll allows every input.
	PolicyAllowAll Policy = "allow-all"
	// PolicyDenyBlueprint denies an input whose blueprint.name is
	// ForbiddenBlueprint, with the reason ForbiddenReason, and allows every
	// other input.
	PolicyDenyBlueprint Policy = "deny-blueprint"
	// PolicyApproveDBA asks for 2 approvals by the group dba for every
	// input.
	PolicyApproveDBA Policy = "approve-dba"
	// PolicySlow answers after SlowFor, which is after the deadline of the
	// suite.
	PolicySlow Policy = "slow"
	// PolicyFailingDeny holds two rules: a deny rule whose condition fails
	// to run for every input, for example a condition that divides by a
	// field that the document does not have, and an approve rule of
	// DBAApprovals by DBAGroup that matches every input. A rule that fails
	// to run is never a pass, so the engine must return an error, not the
	// approve of the other rule and not an allow. An adapter turns on the
	// strict errors of its engine for this: for example OPA with
	// strict-builtin-errors, and Cedar with each policy error in the
	// diagnostics counted.
	PolicyFailingDeny Policy = "failing-deny"
)

// The values of the fixture set.
const (
	ForbiddenBlueprint = "forbidden"
	ForbiddenReason    = "this blueprint is not allowed"
	DBAGroup           = "dba"
	DBAApprovals       = 2
	// Deadline is the deadline that the suite gives to each call of a slow
	// engine.
	Deadline = 200 * time.Millisecond
	// SlowFor is how long PolicySlow takes to answer.
	SlowFor = 5 * time.Second
)

// Factory returns an engine that holds only the policy p, for the kinds
// that the adapter decides.
type Factory func(t *testing.T, p Policy) policy.Engine

// Run executes the suite. kinds are the kinds that the adapter decides. The
// suite expects policy.ErrUnsupportedKind for each other kind.
func Run(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	if len(kinds) == 0 {
		t.Fatal("the adapter decides no kind")
	}

	cases := []struct {
		name string
		run  func(t *testing.T, kinds []policy.Kind, newEngine Factory)
	}{
		{"an allow-all policy allows each kind", testAllowAll},
		{"a deny names its reason", testDeny},
		{"an approve gives the approvals and the groups", testApprove},
		{"a slow engine is ErrUnavailable, not a deny", testSlow},
		{"a canceled context is an error, not a deny", testCanceled},
		{"a deny rule that fails to run is an error, not an allow", testFailingDeny},
		{"an input that is not valid is ErrInvalidInput", testInvalidInput},
		{"a kind that the adapter does not decide is ErrUnsupportedKind", testUnsupportedKind},
		{"each decision is valid and names its engine", testDecisionValid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, kinds, newEngine)
		})
	}
}

func input(kind policy.Kind, blueprint string) policy.Input {
	return policy.Input{
		Kind:          kind,
		SchemaVersion: 1,
		Document:      []byte(`{"blueprint":{"name":"` + blueprint + `","version":1}}`),
	}
}

func decide(t *testing.T, e policy.Engine, in policy.Input) policy.Decision {
	t.Helper()

	d, err := e.Decide(t.Context(), in)
	if err != nil {
		t.Fatalf("Decide(%s, %s) = %v", in.Kind, in.Document, err)
	}

	return d
}

func testAllowAll(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicyAllowAll)

	for _, k := range kinds {
		if d := decide(t, e, input(k, "postgres")); d.Effect != policy.EffectAllow {
			t.Fatalf("Decide(%s) = %q, want allow", k, d.Effect)
		}
	}
}

func testDeny(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	if !slices.Contains(kinds, policy.KindCatalogRequest) {
		t.Skip("the adapter does not decide catalog requests")
	}

	e := newEngine(t, PolicyDenyBlueprint)

	d := decide(t, e, input(policy.KindCatalogRequest, ForbiddenBlueprint))
	if d.Effect != policy.EffectDeny || !slices.Contains(d.Reasons, ForbiddenReason) {
		t.Fatalf("Decide(forbidden) = %+v, want deny with %q", d, ForbiddenReason)
	}

	if d := decide(t, e, input(policy.KindCatalogRequest, "postgres")); d.Effect != policy.EffectAllow {
		t.Fatalf("Decide(postgres) = %q, want allow", d.Effect)
	}
}

func testApprove(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicyApproveDBA)

	d := decide(t, e, input(kinds[0], "postgres"))
	if d.Effect != policy.EffectApprove || d.Approvals == nil {
		t.Fatalf("Decide() = %+v, want approve", d)
	}

	if d.Approvals.Count != DBAApprovals || !slices.Equal(d.Approvals.Groups, []string{DBAGroup}) {
		t.Fatalf("approvals = %+v, want %d by %q", *d.Approvals, DBAApprovals, DBAGroup)
	}
}

func testSlow(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicySlow)

	ctx, cancel := context.WithTimeout(t.Context(), Deadline)
	defer cancel()

	start := time.Now()

	d, err := e.Decide(ctx, input(kinds[0], "postgres"))
	if !errors.Is(err, policy.ErrUnavailable) {
		t.Fatalf("Decide() = %+v, %v, want ErrUnavailable", d, err)
	}

	if took := time.Since(start); took > 4*Deadline {
		t.Fatalf("Decide() took %v after a deadline of %v", took, Deadline)
	}
}

func testCanceled(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicyAllowAll)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if d, err := e.Decide(ctx, input(kinds[0], "postgres")); err == nil {
		t.Fatalf("Decide(canceled) = %+v, want an error", d)
	}
}

func testFailingDeny(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicyFailingDeny)

	for _, k := range kinds {
		d, err := e.Decide(t.Context(), input(k, "postgres"))
		if err == nil {
			t.Fatalf("Decide(%s) = %+v, want an error for the rule that fails to run", k, d)
		}

		if !errors.Is(err, policy.ErrRuleFailed) && !errors.Is(err, policy.ErrUnavailable) {
			t.Fatalf("Decide(%s) = %v, want ErrRuleFailed or ErrUnavailable", k, err)
		}
	}
}

func testInvalidInput(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicyAllowAll)

	bad := input(kinds[0], "postgres")
	bad.SchemaVersion = 0

	if _, err := e.Decide(t.Context(), bad); !errors.Is(err, policy.ErrInvalidInput) {
		t.Fatalf("Decide(version 0) = %v, want ErrInvalidInput", err)
	}
}

func testUnsupportedKind(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	e := newEngine(t, PolicyAllowAll)

	for _, k := range []policy.Kind{policy.KindCatalogRequest, policy.KindJobStart} {
		if slices.Contains(kinds, k) {
			continue
		}

		if _, err := e.Decide(t.Context(), input(k, "postgres")); !errors.Is(err, policy.ErrUnsupportedKind) {
			t.Fatalf("Decide(%s) = %v, want ErrUnsupportedKind", k, err)
		}
	}
}

func testDecisionValid(t *testing.T, kinds []policy.Kind, newEngine Factory) {
	t.Helper()

	for _, p := range []Policy{PolicyAllowAll, PolicyDenyBlueprint, PolicyApproveDBA} {
		e := newEngine(t, p)

		for _, k := range kinds {
			d := decide(t, e, input(k, ForbiddenBlueprint))
			if err := d.Validate(); err != nil {
				t.Fatalf("policy %s, kind %s: %v", p, k, err)
			}

			if d.Engine == "" {
				t.Fatalf("policy %s, kind %s: the decision names no engine", p, k)
			}
		}
	}
}
