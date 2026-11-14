## Context

The session surface for job phases. Sweep report and finish stay in other parts.

## Decisions

1. **One checked session claim.** An unexported sessionClaim has the constructor newSessionClaim. It checks the session proof, the live claim and the job purpose of the pool. No session write exists without it.
2. **Fence on each write.** Each write calls the claim check again in its own transaction, so a takeover during an upload stops the next write.
3. **Counters in the store, bytes first.** The server writes the chunk bytes under a key made from the sequence number. Then it moves the counter in a transaction. A crash between the two leaves an unreferenced blob that orphan cleanup removes, and the same chunk sent again overwrites it.
4. **One plan commit with a stored result.** The commit stores the plan hash and an answer. A second commit answers 409 plan-committed and changes nothing.

## Structure

Unexported type sessionClaim, made only by newSessionClaim, which checks proof, live claim and job purpose.

## Risks

- A runner key on a session path, or a session key on a runner path, must fail closed.
- A refused chunk must keep no bytes in the count.
- A second plan commit must not replace the first.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | The proof check accepts only the session key on session paths. | TestSessionRejectsRunnerKey sends a runner-key proof and expects 401 bad-proof. |
| Service | newSessionClaim checks the live claim and the job purpose on each write. | TestSessionRefusesSweepPoolClaim and TestSessionWriteAfterTakeover expect refusal. |
| Store adapter | The transaction checks the fence, the sequence and the caps. | TestLogChunkOutOfOrderKeepsNothing and TestPlanCommitTwice. |
