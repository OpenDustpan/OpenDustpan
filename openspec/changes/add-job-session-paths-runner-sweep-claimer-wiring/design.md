## Context

Sweep work flows from durable operations through the runner protocol to the blob store. A mixed purpose risks job credentials on sweep hosts. An unbounded report risks blob store exhaustion.

## Decisions

1. **Claim by stored purpose.** The claimer takes the pool ID and reads the purpose of the pool in the claim transaction. It claims only runner.SweepKind(pool) operations.
2. **Bounded streamed report.** The upload streams to the blob store with the data key of the namespace. It checks line, record and byte caps and the header scope subset. A cut stream, a cap breach or a second upload drops the staged blob.
3. **Idempotent finish.** Finish closes the session once. A second finish answers the stored result. A finish with a lost claim answers 409.
4. **Installation pool only.** The pool service checks that a pool with the sweep purpose has no namespace, at create and at update (Owner decision, 13 Nov 2026 (queue P14)).

## Structure

Unexported sweepClaimer struct, built only by NewSweepClaimer with the store, pool and blob ports. The constructor rejects a nil port.

## Risks

- A pool purpose that changes during a poll: the claim reads the purpose in its transaction.
- A stream cut by a crash leaves a partial blob: the session drops the staged blob on any end that is not finish.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| session auth | The handler checks the session proof, claim ownership and open session before it accepts a chunk or finish. | Send report chunks with a runner key proof and no session proof; the server answers bad-proof. |
| purpose check | The claim transaction checks that the pool purpose matches the operation kind. | Poll a jobs pool for sweep work and a sweeps pool for job work; both get 204. |
| report bound | The stream counts bytes, records, line length and scopes and refuses over a cap. | Upload a line above 64 KiB, and a scope outside the target; the server stops the stream and fails the sweep. |
| pool service | The service refuses a namespace pool with the sweep purpose before it stores the pool. | Create and update a namespace pool with the sweep purpose; each answers 422 and stores nothing. |
