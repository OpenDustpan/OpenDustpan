## Context

Jobs run on runners for a stack and a workspace. The owning changes implement counts, resets, limits and gates. This change owns only the destroy fresh check and the composed step-6 run through runner jobs.

## Decisions

1. **Destroy fresh check.** Destroy approval checks a fresh authentication in the window that holds. An access token never gives a fresh step and gets 403 mfa.fresh-step-required with no change. A person with no enrolled factor gets the same 403 with the enrollment page and each way to enroll the first factor. Break-glass sessions meet the same destroy fresh rule and stay outside limits only.
2. **Composed step-6 run.** The composed run starts a plan job and an apply job on runners for a stack and a workspace. It exercises the owning behaviors together: wait in awaiting-approval, reset on new plan, forbid refusal with its decision record, and enforce gate refusal. It adds no second count, void, limit or gate rule.

## Structure

Unexported destroyFresh type with constructor newDestroyFresh that checks the fresh window; only the constructor builds a valid destroy approval.

## Risks

- Bypass of the fresh step for a destroy approval allows a destructive apply
- Second definition of counts, voids, limits or gates drifts from the owning change

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service checks a fresh authentication before it records a destroy approval; token callers fail closed with no state change. | Send a destroy approval on an access token and with no fresh step; remove the service check and show the test still gets 403. |
| HTTP handler | Handler declares authorization actions for approve and apply, masks the plan diff, and returns the 403 and 409 errors of the owning checks without change. | Call approve without a fresh step and read the plan-apply part; remove the handler check and show the handler test gets 403. |
