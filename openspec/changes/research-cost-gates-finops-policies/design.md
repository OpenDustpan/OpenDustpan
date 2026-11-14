## Context

Research note only. No code, no routes, no schema change.

## Decisions

1. **Research before spike.** Write the NEW file docs/research/cost-gates-finops.md with primary sources, then open a spike issue for the minimal cost gate slice. The owner reviews the note.

## Structure

No structure change. No new type.

## Risks

- A gate that needs pull estimator data sees available false before push data exists; the spike uses push data only.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Research note only, no runtime change |  |  |

## Change impact (computed from main)

Tests:

- `e2e/claim_gate_test.go` names `gate.Engine`
- `e2e/jobs_api_test.go` names `gate.Engine`
- `e2e/pulumi_state_write_gate_test.go` names `gate.Engine`

ADRs:

- `docs/adr/0259-cost-estimates-start-with-the-push-route.md` names `cost.Estimator`
- `docs/adr/0144-cost-port-and-budgets.md` names `cost.Estimator`
- `docs/adr/0114-the-policy-port-and-its-default-adapters.md` names `gate.Engine`
- `docs/adr/0110-the-catalog-a-gateway-of-declarative-requests.md` names `gate.Engine`
- `docs/adr/0335-gate-decisions-deny-gates-held-locks-and-the-engine-port.md` names `gate.Engine`
