## Context

The end of a delegation is a permission cut. The check lives in stored state that is resolved at each use, not in a cached allow verdict. The record, `Resolve` and the end causes come from `person-delegates-dusty`. The refusal entry comes from `every-step-audited`. The work that carries a delegation (jobs, triggers, schedules and Dusty runs) comes from changes that are a dependency to be written (see the proposal).

Five points have no ADR or RFC line that settles them. They are the open questions of the proposal. This design holds for each answer.

## Decisions

1. **Stored state, resolved at each use.** Each evaluation of delegated work calls `Resolve` of `person-delegates-dusty` in the same transaction as the work claim or the write. A sentinel `ErrEnded` with its cause refuses the work. The service holds no allow cache (RFC 121 decisions 16 and 31). This change adds no stored field and no route.
2. **One stop reason.** The stop reason of work that an end stopped is `delegation-ended` (RFC 121 decision 33). The cause of the end (`revoked`, `expired` and the others) stays on the delegation row and in the entry.
3. **A confirmed phase follows the setting.** At the next call with a job token, the check reads the effective value of the setting of ADR 354 decision 2. With the recommended value, a phase that the person confirmed finishes, no new phase starts and a phase that was not confirmed stops. The setting shape is open question 1.
4. **Fail closed.** A store error during `Resolve` refuses the request and writes the refusal when it can. In a database gap the answer is 503 with `Retry-After: 5` (ADR 298 decision 2).
5. **The agent cannot revoke.** The revoke action has the agent class `never` (RFC 121 decision 20 (e)). The check lives where the class lives (RFC 121 slice 1); this change adds a test that a delegated request to the revoke routes is refused.

## Structure

The work types that carry a delegation get no field that decides liveness. They hold the delegation ID only, and the only way to a live value is `Resolve`. A conformance test of RFC 121 decision 31 fails when a starter type has no delegation field. That test is part of the change that makes work carry the delegation, not of this change.

## Risks

- A runner keeps a claim across the end and writes one more step. The claim resolves the delegation at each write boundary, and the boundary refuses.
- A scheduled start happens between the end and the next read. The start resolves the delegation inside the start transaction and refuses.
- A confirmed phase finishes after the end. This is the owner's choice (ADR 354 decision 2) and the setting can change it. The work list shows it.
- A trigger that fires on every minute writes an entry for each refusal until it is disabled. Open question 4 asks if the entries collapse.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The service calls `Resolve` in the same transaction as the claim, holds no allow cache, and refuses when the delegation has ended or the store returns an error. | End the delegation between the claim read and the tool write. The write is refused and the refusal names the delegation and the cause. |
| HTTP handler | The handler checks the delegation at the point of use for the object of the request and fails closed on a store error. | Send one agent request after the end with a valid credential. The handler answers 403 `delegation.ended` and writes the refusal entry. With the database blocked, it answers 503. |
| port adapter | The store adapter maps an ended row to the sentinel `ErrEnded` with its cause, and a store error to a refusal. It stores no verdict. | Replay the bytes of an earlier allow answer after the end. The adapter returns `ErrEnded` and the request is refused. |
| revoke routes | The agent class `never` refuses a request of an agent to the revoke routes. | Send a revoke request with the credential of an agent. It is refused, no row changes and a denial entry exists. |
