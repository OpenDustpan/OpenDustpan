//go:build e2e

package e2e

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
)

// coverageGateConfig holds no gate, no access limit, no approval default and no approval rule: the route
// table needs a gate configuration service, and the coverage gate never
// serves a request.
type coverageGateConfig struct{}

func (coverageGateConfig) ApprovalDefault(context.Context, gate.Scope) (gate.ApprovalDefault, error) {
	return gate.ApprovalDefault{}, gate.ErrNotFound
}

func (coverageGateConfig) PutApprovalDefault(
	context.Context, gate.ApprovalDefault, int64, audit.Entry,
) (gate.ApprovalDefault, error) {
	return gate.ApprovalDefault{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteApprovalDefault(context.Context, gate.Scope, int64, audit.Entry) error {
	return gate.ErrNotFound
}

func (coverageGateConfig) ApprovalRules(context.Context, gate.Scope) ([]gate.ApprovalRule, error) {
	return nil, nil
}

func (coverageGateConfig) ApprovalRule(context.Context, gate.Scope, gate.ID) (gate.ApprovalRule, error) {
	return gate.ApprovalRule{}, gate.ErrNotFound
}

func (coverageGateConfig) PutApprovalRule(
	context.Context, gate.ApprovalRule, int64, audit.Entry,
) (gate.ApprovalRule, error) {
	return gate.ApprovalRule{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteApprovalRule(context.Context, gate.ObjectRef, audit.Entry) error {
	return gate.ErrNotFound
}

func (coverageGateConfig) Gates(context.Context, gate.Scope) ([]gate.Gate, error) {
	return nil, nil
}

func (coverageGateConfig) Gate(context.Context, gate.Scope, gate.ID) (gate.Gate, error) {
	return gate.Gate{}, gate.ErrNotFound
}

func (coverageGateConfig) PutGate(context.Context, gate.Gate, int64, string, audit.Entry) (gate.Gate, error) {
	return gate.Gate{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteGate(context.Context, gate.ObjectRef, string, audit.Entry) error {
	return gate.ErrNotFound
}

func (coverageGateConfig) AccessLimits(context.Context, gate.Scope) ([]gate.AccessLimit, error) {
	return nil, nil
}

func (coverageGateConfig) AccessLimit(context.Context, gate.Scope, gate.ID) (gate.AccessLimit, error) {
	return gate.AccessLimit{}, gate.ErrNotFound
}

func (coverageGateConfig) PutAccessLimit(
	context.Context, gate.AccessLimit, int64, audit.Entry,
) (gate.AccessLimit, error) {
	return gate.AccessLimit{}, gate.ErrNotFound
}

func (coverageGateConfig) DeleteAccessLimit(context.Context, gate.ObjectRef, audit.Entry) error {
	return gate.ErrNotFound
}

// approvalRuleCoveringTest names the test that proves the approval default and
// approval rule routes instead of a CLI: no CLI sets an approval rule.
const approvalRuleCoveringTest = "TestApprovalRules"

// approvalRuleAllowEntries returns the routes of the approval defaults and the
// approval rules of a namespace. The steps of approvalRuleCoveringTest drive
// each one through the real server on each store.
func approvalRuleAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages approval rules. " +
		"The harness step calls it through the real server and checks the answer."

	const (
		defaultPath = "/api/dustpan/v1/namespaces/{namespace}/approval-default"
		rulesPath   = "/api/dustpan/v1/namespaces/{namespace}/approval-rules"
	)

	routes := []struct{ method, path string }{
		{http.MethodGet, defaultPath},
		{http.MethodPut, defaultPath},
		{http.MethodDelete, defaultPath},
		{http.MethodGet, rulesPath},
		{http.MethodGet, rulesPath + "/{id}"},
		{http.MethodPut, rulesPath + "/{id}"},
		{http.MethodDelete, rulesPath + "/{id}"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: approvalRuleCoveringTest, Reason: reason,
		})
	}

	return out
}

// gateObjectCoveringTest names the test that proves the gate and access limit
// routes instead of a CLI: no CLI sets a gate.
const gateObjectCoveringTest = "TestGateRoutes"

// gateObjectAllowEntries returns the routes of the gates and the access limits
// of a namespace, of a collection and of a unit. The steps of
// gateObjectCoveringTest drive each one through the real server on each store.
func gateObjectAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages gates or access limits. " +
		"The harness step calls it through the real server and checks the answer."

	const namespacePath = "/api/dustpan/v1/namespaces/{namespace}"

	bases := []string{
		namespacePath,
		namespacePath + "/collections/{collection}",
		namespacePath + "/units/by-name/{tool}/{name}",
	}

	const item = "/{id}"

	out := make([]AllowEntry, 0, 2*4*len(bases))

	for _, base := range bases {
		for _, kind := range []string{"/gates", "/access-limits"} {
			for _, r := range []struct{ method, suffix string }{
				{http.MethodGet, ""}, {http.MethodGet, item}, {http.MethodPut, item}, {http.MethodDelete, item},
			} {
				out = append(out, AllowEntry{
					Method:       r.method,
					Path:         base + kind + r.suffix,
					CoveringTest: gateObjectCoveringTest,
					Reason:       reason,
				})
			}
		}
	}

	return out
}
