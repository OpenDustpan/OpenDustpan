## Context

A plain OpenTofu run has no associated claim record. The server serializes it against runner jobs with the existing whole-unit lock, queues runner jobs behind it in FIFO order, shows the holder, and starts the queued job after release, expiry, or force-unlock.

## Decisions

1. **Plain run maps to strict lock.** Treat a TFE-compatible lock request with no associated claim record from the claim model as the strict case whole-unit lock with reason local-without-wrapper stored on the workspace lock record.
2. **Reuse lock machinery, add reason only.** Reuse the existing workspace lock lifetime, holder check, force-unlock fence, last-release record, and the FIFO whole-unit lock queue of strict-case-takes-turns; add no new queue, no new TTL, and no new renewal rule; add only the reason field plus a REST read of the live holder and the wait record.

## Structure

Unexported field strictCause on the workspace lock record set only by constructor acquireStrictLock which requires proof of no associated claim record and a holder identity.

## Risks

- A plain run that never unlocks blocks runner jobs in the queue until expiry or operator force-unlock.
- History that omits the holder hides why a runner job waited.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler looks up the associated claim record from the claim model and routes a lock with no associated claim to the strict whole-unit lock path with reason local-without-wrapper. | Send a lock with an associated claim and a lock with none; only the second takes the strict path. |
| service | Service checks the active claim state and the active lock row in one fenced transaction before it grants a plain lock or queues a runner job behind a plain holder. | Race a plain lock against an active claim; the loser gets the conflict error and no row changes. |
| port adapter | Store holds the holder identity and reason on the lock row and enforces holder match on unlock and upload. | Upload state with a different lock ID after expiry or fence; the store refuses it. |
