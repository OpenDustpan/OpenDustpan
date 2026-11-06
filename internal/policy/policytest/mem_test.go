package policytest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/policy"
	"opendustpan.org/opendustpan/internal/policy/policytest"
)

var errUnknownPolicy = errors.New("unknown fixture policy")

// memEngine is the reference engine: each policy of the fixture set in Go.
type memEngine struct {
	policy policytest.Policy
	kinds  []policy.Kind
}

func (e memEngine) Decide(ctx context.Context, in policy.Input) (policy.Decision, error) {
	if err := in.Validate(); err != nil {
		return policy.Decision{}, err
	}

	if !slices.Contains(e.kinds, in.Kind) {
		return policy.Decision{}, fmt.Errorf("%w: %s", policy.ErrUnsupportedKind, in.Kind)
	}

	if err := ctx.Err(); err != nil {
		return policy.Decision{}, fmt.Errorf("%w: %w", policy.ErrUnavailable, err)
	}

	d := policy.Decision{Effect: policy.EffectAllow, Reasons: nil, Approvals: nil, Engine: "mem", PolicyVersion: "1"}

	switch e.policy {
	case policytest.PolicyAllowAll:
		return d, nil
	case policytest.PolicyDenyBlueprint:
		return denyBlueprint(d, in)
	case policytest.PolicyApproveDBA:
		d.Effect = policy.EffectApprove
		d.Approvals = &policy.Approvals{
			Rule: policytest.DBARule, Groups: []string{policytest.DBAGroup}, Count: policytest.DBAApprovals,
		}

		return d, nil
	case policytest.PolicySlow:
		return slow(ctx, d)
	case policytest.PolicyFailingDeny:
		return failingDeny(d, in)
	default:
		return policy.Decision{}, fmt.Errorf("%w: %q", errUnknownPolicy, e.policy)
	}
}

// document is the part of a fixture document that the reference engine
// reads.
type document struct {
	Blueprint blueprintName `json:"blueprint"`
}

type blueprintName struct {
	Name string `json:"name"`
}

func denyBlueprint(d policy.Decision, in policy.Input) (policy.Decision, error) {
	var doc document

	if err := json.Unmarshal(in.Document, &doc); err != nil {
		return policy.Decision{}, fmt.Errorf("%w: %w", policy.ErrInvalidInput, err)
	}

	if doc.Blueprint.Name == policytest.ForbiddenBlueprint {
		d.Effect = policy.EffectDeny
		d.Reasons = []string{policytest.ForbiddenReason}
	}

	return d, nil
}

// failingDeny runs the two rules of PolicyFailingDeny in order. The deny
// rule reads blueprint.weight, which no fixture document has, so it fails
// to run, and the engine stops with an error before the approve rule.
func failingDeny(d policy.Decision, in policy.Input) (policy.Decision, error) {
	var doc map[string]map[string]any

	if err := json.Unmarshal(in.Document, &doc); err != nil {
		return policy.Decision{}, fmt.Errorf("%w: %w", policy.ErrInvalidInput, err)
	}

	weight, ok := doc["blueprint"]["weight"].(float64)
	if !ok {
		return policy.Decision{}, fmt.Errorf("%w: rule 1 reads blueprint.weight, which the document lacks",
			policy.ErrRuleFailed)
	}

	if 100/weight > 1 {
		d.Effect = policy.EffectDeny

		return d, nil
	}

	d.Effect = policy.EffectApprove
	d.Approvals = &policy.Approvals{
		Rule: policytest.DBARule, Groups: []string{policytest.DBAGroup}, Count: policytest.DBAApprovals,
	}

	return d, nil
}

func slow(ctx context.Context, d policy.Decision) (policy.Decision, error) {
	timer := time.NewTimer(policytest.SlowFor)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return policy.Decision{}, fmt.Errorf("%w: %w", policy.ErrUnavailable, ctx.Err())
	case <-timer.C:
		return d, nil
	}
}

func TestMemEngineConformance(t *testing.T) {
	t.Parallel()

	for _, kinds := range [][]policy.Kind{
		{policy.KindSelfServiceRequest, policy.KindJobStart},
		{policy.KindJobStart},
	} {
		policytest.Run(t, kinds, func(_ *testing.T, p policytest.Policy) policy.Engine {
			return memEngine{policy: p, kinds: kinds}
		})
	}
}
