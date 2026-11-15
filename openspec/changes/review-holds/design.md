## Context

Fan-out turns one Terragrunt run-all request into one change order with one plan job and one apply job per unit. The review gate must hold across all units with one rule in the job start path.

## Decisions

1. **Gate at job start.** Enforce the apply gate in the job start path, not in the shim. The shim only creates plan jobs and apply jobs in gated state. The start function loads the stored approval and signature state for the current plan revision and fails closed on load error. This makes an ungated apply impossible even when the shim is wrong.
2. **Revision-bound idempotent apply request.** Treat the apply request as the approved state of ADR 190, not a second approval type. The NEW route stores the person apply request bound to one plan revision and checks it against the stored approvals and signatures. A new plan revision voids pending starts. Already running jobs keep running and carry the old revision mark. A duplicate apply request for the same revision returns the stored result.

## Structure

Unexported type applyGate with constructor newApplyGate(changeOrderID) that loads the stored apply request and signature state; only the constructor can build a go decision, and start fails closed on load error.

## Risks

- Shim starts an apply job before the apply request.
- Runner retries an apply call after rejection and causes a duplicate apply.
- A new plan revision lands after the apply request and causes a stale approval apply.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler checks the stored apply request for the current plan revision and refuses start with the gated error. | Send apply job start before the apply request direct to the handler and check refusal with zero provider apply calls. |
| Service | Service checks the approve grant on the change order and the signature state before it stores the apply request. | Send the apply request without the approve grant and with a self approval and check refusal. |
| Store | Store transaction starts an apply job only when the stored apply request names the current plan revision. | Race two apply requests and a new plan revision against job start and check only the current revision starts once. |
