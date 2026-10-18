# 131. The runner protocol

Status: proposed · 2026-10-18 · adds to ADR 101 decisions 2, 13 and 15

## Context

[ADR 101](0101-two-executors-write-back-and-runners.md) decision 15 fixes
the shape of the runner protocol: `/runner/v1`, HTTP and JSON, long polling
of `Claimer.Claim`, claims with an expiry, and runners of the two releases
before the server release. It fixes the ports that the protocol calls:
`job.Claimer` and `job.Recorder`.

ADR 164 (draft) asks the protocol for the claim of a scan, and for the
upload of a scan report as a stream under the claim.

The project owner decided on 17 October 2026:

- The runner opens every connection, over HTTPS. The server never connects
  to a runner.
- HTTP and JSON under `/runner/v1`, long polling, leases with `SKIP
  LOCKED`, two releases of skew between the server and a runner.
  WebSockets may come later.
- A cancel sends `SIGINT`, then `SIGKILL`.

The label "(owner)" names a decision of the owner. The other decisions are
proposals of this design. The Go types of the messages are in
`pkg/runner/protocol`, so the HTTP adapter, the runner and the launcher
share one shape.

## Decision

1. **Two callers, two credentials.** The runner (the coordinator) calls the
   paths of the runner, with a proof of its runner key. The launcher calls
   the paths of the session, with a proof of the key of its job session
   (ADR 133). A runner key cannot write a step, a log or a plan. A session
   key cannot claim work. Each request carries the header
   `Dustpan-Release` with the release of the sender (ADR 132).
2. **The paths of the runner.**

   | Method and path | Body | Answer |
   | --- | --- | --- |
   | `POST /runner/v1/join` | `JoinRequest` | `JoinResponse` |
   | `POST /runner/v1/rotate` | `RotateRequest` | 204 |
   | `POST /runner/v1/heartbeat` | `Heartbeat` | `HeartbeatResponse` |
   | `POST /runner/v1/poll` | `PollRequest` | 200 `Work`, or 204 |
   | `POST /runner/v1/claims/{operation}/handoff` | none | `HandoffResponse` |
   | `POST /runner/v1/claims/{operation}/renew` | none | `RenewResponse` |
   | `POST /runner/v1/claims/{operation}/release` | `ReleaseRequest` | 204 |

   The join is the one request with no proof of an existing key. The join
   token and the signature of the new key authenticate it.
3. **The paths of the session.**

   | Method and path | Body | Answer |
   | --- | --- | --- |
   | `POST /runner/v1/session` | `SessionRequest` | `SessionResponse` |
   | `POST /runner/v1/session/renew` | none | `RenewResponse` |
   | `GET /runner/v1/session/source` | none | the raw git pack |
   | `GET /runner/v1/session/source/objects/{oid}` | none | a Git LFS object |
   | `GET /runner/v1/session/plan/parts/{part}` | none | a part of the plan |
   | `POST /runner/v1/session/credentials` | `CredentialsRequest` | `CredentialsResponse` |
   | `POST /runner/v1/session/steps/{step}/begin` | none | 204 |
   | `POST /runner/v1/session/steps/{step}/end` | `StepEnd` | 204 |
   | `PUT /runner/v1/session/steps/{step}/logs/{seq}` | a chunk | 204 |
   | `PUT /runner/v1/session/plan/uploads/{part}` | a part | 204 |
   | `POST /runner/v1/session/plan/commit` | `PlanCommit` | 204 |
   | `PUT /runner/v1/session/scan-report` | JSON Lines | 204 |
   | `POST /runner/v1/session/finish` | `Finish` | 204 |

   The session exchange is the one request of the launcher with no proof
   of an existing key. The handoff token and the signature of the new key
   authenticate it.
4. **Each path calls one port.** The HTTP adapter of the protocol is a thin
   layer over the ports of the job model and of this design:
   - The poll calls `job.Claimer.Claim` for a pool for jobs, and the claim
     of a scan for a pool for scans. The server takes the kinds from the
     pool (ADR 130 decision 3).
   - The renewals call `job.Claimer.Renew`, and the release
     `job.Claimer.Release`. The finish calls `job.Claimer.Finish`.
   - The step, log and plan paths call `job.Recorder`. The plan upload
     stages each part in a blob of the session. The commit calls
     `Recorder.PutPlan` with the staged parts in one call, as ADR 102
     decision 5 needs.
   - The source paths read a source snapshot (ADR 135).
   - The scan report path streams the report to the blob store and ends
     the claim of the scan, as ADR 164 decision 12 says.

   So the protocol adds no rule about claims, cancel, steps, logs or plans.
   A difference between the protocol and write-back is a bug in one
   adapter.
5. **The long poll (owner).** A poll waits for work for at most 50 seconds
   (`protocol.MaxPollWait`), below the idle timeout of common proxies. The
   server tries `Claimer.Claim` at once, then each time that a durable
   operation of a kind of the pool starts on this instance, and at least
   every 5 seconds for operations that other instances start. The claim
   uses the `SKIP LOCKED` query of the durable adapter, so two runners
   never get one operation. A poll with no work answers 204. A runner with
   no free claim slot does not poll. A draining runner does not poll.
6. **Work holds no secret.** `Work` names the claim, the job and the phase
   or the scan, the image and the service account of the unit or of the
   scan target, and the limits of the launcher (ADR 134). The launcher gets
   the steps and the credentials through its session, never through the
   runner.
7. **Who renews a claim.** Before the launcher opens its session, the
   runner renews the claim (`/claims/{operation}/renew`). After the
   session opens, the launcher renews it (`/session/renew`), and a renewal
   by the runner fails with `claim-lost`. The holder renews after a third
   of the lease of the pool (ADR 130 decision 7). A runner that sees no
   session after the handoff token expired, for example a pod that waits
   for a node, asks for a new handoff token, or releases the claim.
8. **Heartbeats.** A runner sends a heartbeat every 15 seconds, also while
   it polls. The answer lists the claims of the runner that a cancel stops,
   the claims that are no longer live, whether the key must rotate and
   whether the runner drains. A runner that sees a lost claim ends its
   launcher and deletes its Job or sandbox.
9. **Cancel (owner).** A renewal during a cancel answers
   `canceled: true`. The launcher then sends `SIGINT` to the process group
   of the step. After the grace period of the step, it sends `SIGKILL`.
   The grace period is 10 minutes for a step that changes resources and 10
   seconds for other steps (ADR 101 decision 13). The launcher then
   uploads the last log chunks and calls the finish. A cancel reaches the
   launcher within one renewal interval: 30 seconds with the default lease.
   During a cancel, a step start answers `canceled`, and the log and finish
   paths still accept writes (ADR 101 decision 2).
10. **Logs.** The launcher sends the output of each step in chunks of at
    most 1 MiB, with numbers from 0 and the header `Dustpan-Content-SHA256`.
    It sends a chunk at least every 2 seconds while the step writes output,
    so the console shows the log while the step runs. It masks the secret
    values of the session before it sends a chunk (ADR 134). A retry of a
    chunk with the same number and hash changes nothing. A known number
    with another hash fails with `conflict`.
11. **Plans and outputs.** After the last step that names an output, the
    launcher uploads each part of the plan, then commits the plan in one
    call. An apply phase reads the part `plan-apply` through its session.
    The launcher checks the SHA-256 of the part against the hash in the
    assignment, and refuses another hash (ADR 102 decision 7).
12. **Errors.** Each answer with a status of 400 or more has the body
    `{"code": "...", "message": "..."}` (`protocol.Error`). The codes and
    what the caller does:

    | Code | Status | The caller |
    | --- | --- | --- |
    | `invalid` | 400 | stops: a bug or a bad setting. |
    | `bad-proof` | 401 | checks its clock, then stops. |
    | `key-expired` | 401 | stops and shows the command to join again. |
    | `retired` | 403 | stops. |
    | `token-used`, `token-expired` | 401 | the runner asks for a new handoff token. |
    | `not-found` | 404 | stops the claim. |
    | `claim-lost` | 409 | stops the step at once and reports nothing more. |
    | `canceled` | 409 | stops the step with its grace period. |
    | `conflict` | 409 | stops the claim: two writers. |
    | `source-not-ready` | 409 | waits for `Retry-After`. |
    | `plan-stale` | 409 | stops: the server failed the phase. |
    | `version-unsupported` | 426 | stops and shows the range of releases. |
    | `rate-limited`, `unavailable` | 429, 503 | waits for `Retry-After`. |

    A network error or a status of 500 or more retries with exponential
    backoff from 1 second to 30 seconds, with jitter, until the claim
    expires.
13. **Every request is safe to repeat.** A join with a used token fails
    and changes nothing. A heartbeat and a renewal change nothing twice. A
    step start or end of a claim records once. A log chunk has its number.
    A plan commit of a phase with a plan fails with `conflict`. A finish of
    a finished phase fails with `claim-lost`. So a proof that an attacker
    replays inside its window can only repeat a request that the server
    already did (ADR 133).
14. **Limits.** A JSON body has at most 4 MiB. A log chunk has at most
    1 MiB, a part of a plan at most 256 MiB, and a scan report at most the
    limit of ADR 164. The server answers `rate-limited` to a runner above
    10 polls each second, and to a session above 50 requests each second.

## Consequences

- The protocol needs no inbound port in the network of the team. A proxy
  that allows outbound HTTPS to the server is enough.
- A runner that the server cannot reach is never a problem for the server.
  A runner that cannot reach the server loses its claims after one lease,
  and the takeover rules of ADR 100 decision 9 apply.
- Each phase makes one session, a handful of step records and one log
  chunk for each 1 MiB of output or each 2 seconds. A phase of one hour
  with steady output makes about 1,800 chunks.
- A cancel takes up to one renewal interval to reach the launcher. A later
  push channel, for example WebSockets, can make it shorter.

## Open questions

- WebSockets or HTTP/2 server push for cancels and for the log stream. The
  owner allows them later. The interval of a renewal is the delay of a
  cancel until then.
- Does a log chunk every 2 seconds put too many small blobs in the blob
  store for a long phase? ADR 102 asks whether the server joins the chunks
  of a closed log.
- ADR 164 proposes `PUT /runner/v1/scans/{id}/report` for the scan report.
  This ADR proposes the path under the session, because the session
  authenticates the launcher, not the runner. The two designs must agree
  on one path.
