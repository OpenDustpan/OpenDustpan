## Context

The session surface for job phases. Sweep report and finish stay in other parts.

## Decisions

1. **One checked session claim.** An unexported sessionClaim is made only by newSessionClaim, which checks the proof, the live claim and the job purpose of the pool.
2. **Fence on each write.** Each write checks the claim again in its own transaction.
3. **Chunk bytes keyed by claim and sequence.** The blob key holds the claim and the sequence, so a lost-claim launcher cannot overwrite a counted chunk of a new claim. The counter moves after the bytes.
4. **One plan commit.** The commit stores the plan hash. A second commit answers 409 plan-committed.

## Structure

Unexported type sessionClaim, made only by newSessionClaim.

## Contract

Routes that main declares in pkg/runner/protocol/protocol.go and does not route yet. Each one needs the proof of the session key (action runner:claim) and a live claim:

| Route | Constant | Answer |
| --- | --- | --- |
| `GET /runner/v1/session/source` | PathSessionSource (pkg/runner/protocol/protocol.go:83) | The git pack of the commit of the job. 409 source-not-ready with Retry-After while the snapshot is in build. |
| `GET /runner/v1/session/source/objects/{oid}` | PathSessionSourceObject (pkg/runner/protocol/protocol.go:86) | One LFS object. 404 not-found when it is absent. |
| `GET /runner/v1/session/plan/parts/{part}` | PathSessionPlanPart (pkg/runner/protocol/protocol.go:89) | One part of the plan of an apply phase. |
| `PUT /runner/v1/session/plan/uploads/{part}` | PathSessionPlanUpload (pkg/runner/protocol/protocol.go:107) | Stages one part. At most 8 parts, at most 256 MiB each. A new upload of a part number replaces the part. |
| `POST /runner/v1/session/plan/commit` | PathSessionPlanCommit (pkg/runner/protocol/protocol.go:110) | Stores the staged parts as the plan, once. |

Error codes: the codes of pkg/runner/protocol/protocol.go:198-214 (invalid 400, bad-proof 401, claim-lost 409, source-not-ready 409). One code is NEW: `ErrorPlanCommitted = "plan-committed"`, HTTP 409, for a second commit of the plan of one phase. The caps answer 400 invalid.

Stored data: the plan hash of the first commit, kept with the plan. No new table or column beyond it.

## Risks

- A refused chunk must keep no bytes in the count.
- A second plan commit must not replace the first.
- A re-upload of a part number replaces the part and does not count against the 8 parts.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | The proof check accepts only the session key on session paths. | TestSessionRejectsRunnerKey |
| Service | newSessionClaim checks the live claim and job purpose on each write. | TestSessionWriteAfterTakeover |
| Store adapter | The transaction checks the fence, the sequence and the caps. | TestPlanCommitTwice |

## Diagrams

After this change:

```mermaid
sequenceDiagram
 participant L as Launcher
 participant S as Server
 L->>S: POST /runner/v1/session
 S-->>L: session
 L->>S: renew, source, steps, logs
 L->>S: plan parts, plan-commit
 S-->>L: 200 or 409 plan-committed
```

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[unmanaged]
```

New imports of this change (each one allowed by the module rules):

- `internal/api/runnerapi` imports `pkg/runner`
- `internal/api/runnerapi` imports (read only) `pkg/job`
- `internal/api/runnerapi` imports (read only) `pkg/identity`

States on main in the touched packages:

```mermaid
stateDiagram-v2
open
ignored
pending
resolved
state
queued
dispatched
running
matching
succeeded
failed
canceled
```

States and transitions after this change:

```mermaid
stateDiagram-v2
 [*] --> NEW_ClaimLive
 NEW_ClaimLive --> NEW_SessionOpen: exchange
 NEW_SessionOpen --> NEW_PlanCommitted: plan-commit
 NEW_SessionOpen --> NEW_ClaimLost: takeover or cancel
 NEW_PlanCommitted --> [*]
 NEW_ClaimLost --> [*]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Log chunk size cap | 1 | MiB per chunk | session logs path |
| Plan part cap | 256 | MiB per part, at most 8 parts | one session |
| JSON body cap | 4 | MiB | each JSON request on a session path |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph TeamHost[Team host]
 L[Launcher]
 end
 subgraph ServerProcess[Server process]
 H[Session handler]
 S[Session claim service]
 end
 subgraph Storage[Storage]
 DB[(SQL store)]
 B[(Blob store)]
 end
 L -->|session proof| H
 H --> S
 S --> DB
 S --> B
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Team host to server | S | A caller forges or replays a session proof, or signs with a runner key. | boundary check | The handler accepts only the session key and a fresh nonce. | TestSessionRejectsRunnerKey |
| Team host to server | S | A caller forges or replays a session proof, or signs with a runner key. | encode or contain | Token and proof text never enter a log line. | TestSessionProofCanary |
| Team host to server | S | A caller forges or replays a session proof, or signs with a runner key. | limit | Failed proofs count against a window. | TestSessionProofFailureWindow |
| Team host to server | S | A caller forges or replays a session proof, or signs with a runner key. | authorize at point of use | newSessionClaim checks the live claim on each write. | TestSessionWriteAfterTakeover |
| Team host to server | S | A caller forges or replays a session proof, or signs with a runner key. | fail closed | An unknown key kind answers 401 and writes an audit entry. | TestSessionUnknownKeyKindDenied |
| Team host to server | T | A chunk body changes in transit or a part passes its cap. | boundary check | The server compares the body hash after the stream. | TestLogChunkWrongHash |
| Team host to server | T | A chunk body changes in transit or a part passes its cap. | encode or contain | The blob key is built from IDs and numbers only. | TestLogKeyNoPathInput |
| Team host to server | T | A chunk body changes in transit or a part passes its cap. | limit | Chunk, part and session caps refuse the excess. | TestPlanPartAboveCap |
| Team host to server | T | A chunk body changes in transit or a part passes its cap. | authorize at point of use | The write checks the claim in its transaction. | TestLogChunkAfterTakeover |
| Team host to server | T | A chunk body changes in transit or a part passes its cap. | fail closed | A hash mismatch keeps no bytes. | TestLogChunkOutOfOrderKeepsNothing |
| Server to storage | E | A launcher of a lost claim or a sweep pool writes data. | boundary check | The pool purpose is read from the pool, not from the request. | TestSessionRefusesSweepPoolClaim |
| Server to storage | E | A launcher of a lost claim or a sweep pool writes data. | encode or contain | The blob key holds the claim. | TestLogKeyHoldsClaim |
| Server to storage | E | A launcher of a lost claim or a sweep pool writes data. | limit | The session holds a bounded lease. | TestSessionLeaseBound |
| Server to storage | E | A launcher of a lost claim or a sweep pool writes data. | authorize at point of use | newSessionClaim checks the live claim and purpose. | TestSessionWriteAfterTakeover |
| Server to storage | E | A launcher of a lost claim or a sweep pool writes data. | fail closed | A lost claim answers claim-lost and stores nothing. | TestClaimLostStoresNothing |
| Team host to server | R | A launcher denies a second plan commit. | boundary check | The commit names the plan hash. | TestPlanCommitHashChecked |
| Team host to server | R | A launcher denies a second plan commit. | encode or contain | The audit entry holds IDs, never the token. | TestCredentialsAuditNoToken |
| Team host to server | R | A launcher denies a second plan commit. | limit | One commit for each phase. | TestPlanCommitTwice |
| Team host to server | R | A launcher denies a second plan commit. | authorize at point of use | The commit checks the live claim. | TestPlanCommitAfterTakeover |
| Team host to server | R | A launcher denies a second plan commit. | fail closed | The second commit answers 409 and changes nothing. | TestPlanCommitTwice |
| Team host to server | D | A launcher floods chunks or parts to fill storage. | boundary check | Size is checked while the body streams. | TestLogChunkStreamCap |
| Team host to server | D | A launcher floods chunks or parts to fill storage. | encode or contain | A refused upload removes its blob. | TestRefusedUploadRemovesBlob |
| Team host to server | D | A launcher floods chunks or parts to fill storage. | limit | Per-step and per-phase caps in the transaction. | TestLogCapsPerStepAndPhase |
| Team host to server | D | A launcher floods chunks or parts to fill storage. | authorize at point of use | Each write checks the live claim. | TestFloodAfterTakeover |
| Team host to server | D | A launcher floods chunks or parts to fill storage. | fail closed | A cap breach answers invalid and stores nothing. | TestCapBreachStoresNothing |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Session exchange | The handoff token is used twice or the claim is gone. | The launcher stops; the runner takes a new claim. | One session at most. |
| Log chunk write | The server stops after the bytes and before the counter. | The launcher sends the same chunk; the bytes are overwritten. | The chunk is counted once. |
| Plan part upload | The claim is lost after a part is stored and before the commit. | The new claim plans again. | The uncommitted part is not referenced; orphan cleanup removes it. |
| Plan commit | A second commit arrives, or the first answer is lost. | The launcher repeats once and gets 409 plan-committed. | The first commit stands. |
