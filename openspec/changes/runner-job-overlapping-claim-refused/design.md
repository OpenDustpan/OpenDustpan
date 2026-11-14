## Context

Job A holds an accepted claim on a workspace and applies. Job C registers a claim on the same workspace. The claim record, the register route, and the fence come from the dependency item. This change adds the overlap refusal, the blocked status, and the release of the block. No code lands before the owner accepts the concurrent-applies RFC as an ADR.

## Decisions

1. **Refusal is a state of the claim record.** A refused claim stores the refused state with the blocking claim ID and the overlapping instance address. The overlap check and the insert run in one fenced transaction under a lock on the workspace claims, so two parallel inserts cannot both be accepted. Registration is idempotent by job ID and claim ID from the dependency item. A blocked job stays blocked after the blocker ends. The claim and the job use stable IDs: the workspace ID and the claim ID.

## Structure

An unexported type claimSet has one constructor used by the registration function. The constructor checks the overlap inside the fenced transaction. No caller stores an accepted claim that overlaps.

## Risks

- The refusal omits the blocking claim ID, so the developer cannot tell which team blocks the apply.
- A blocked job starts the apply and calls the provider.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Store | The overlap check and the insert run in one fenced transaction under the workspace claims lock. | Two parallel overlapping registrations run at once; at most one claim is accepted. |
| Service | The apply start checks for an accepted claim of the job and denies the apply without one, including a stale runner whose lease was lost. | A runner with a refused claim, or with a lost lease, starts the apply phase; the server denies it with 409. |
| HTTP handler | The refusal read checks the job read action on the job. | A caller without the action reads a refused claim; the server denies the read with no claim content. |
