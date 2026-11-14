---
version: 2
---

Issue: #972, #979.

## Why

**Persona: runner operator.** As a Runner operator I run pools that claim plans and applies. I need a waiting runner to claim new work at once when it starts on the same instance, and I need clear 429 answers when a runner or session polls too fast, so jobs start fast and a hot runner does not overload the server.

The user path this change must make work:

1. **API.** Runner sends poll on ANY /runner/v1 and waits up to 50 s for work.
2. **API.** Operator starts a job; a waiting runner of the pool claims it within 1 s when the start commits on the same instance.
3. **Docs page.** Operator reads the runner API reference for poll wait, 429 rate-limited, Retry-After, and per-instance count.
4. **E2e.** Operator runs a runner against the real server and watches claim latency and 429 behavior.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Poll waits and finds no work | 204 with empty body: no work before the 50 s deadline. Send the next poll. | `ANY /runner/v1` |
| Rate limit above 10 polls or 50 session requests each second | 429 rate-limited with Retry-After: 1: too many polls or session requests. Wait 1 second and retry with a new proof. | `ANY /runner/v1` |

Evidence for the claims above:

- Poll waits on a fixed interval with no other wake. Proof: internal/api/runnerapi/protocol.go:396: pause := time.NewTimer(h.interval)
- Runner protocol error path writes 429 answers and sets Retry-After on 429 and 503. Proof: internal/api/runnerapi/errors.go:205
- Runner API reference path exists in this repo. Proof: docs/reference/runner-api.md
- Durable package path exists in this repo. Proof: pkg/durable

## What Changes

- New wake behavior on the existing route ANY /runner/v1: instance-local wake signal per durable kind, fired after commit on both start paths, and poll wait on signal with Claim at once, 5 s timer, and 50 s deadline.
- Per-runner poll limiter at 10 polls each second on the existing poll path under ANY /runner/v1.
- Per-session limiter at 50 requests each second on session paths under ANY /runner/v1 keyed by session ID, after the session-paths change merges.
- Bounded limiter state with 5 minute idle eviction and 429 rate-limited with Retry-After: 1.
- Runner API reference states the per-instance count.

Out of scope:

- Wake across instances such as PostgreSQL LISTEN and NOTIFY.
- Wake on release or lease expiry.
- Limits shared across instances.
- Limits on the management API.
- Per-IP guard for unauthenticated floods.

Depends on:

- add-job-session-paths-runner-token-issuer
- runners
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A runner that polls fast sees 429 and honors Retry-After: 1 instead of a fixed sleep.
- A poll that waits 50 s with no work is normal and not an error.
- Per-instance counts surprise operators with several instances behind a balancer, so the docs state it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Poll waits on a timer of h.interval between Claim tries (`internal/api/runnerapi/protocol.go:396`)
- Runner protocol error path maps ErrLimit to 429 (`internal/api/runnerapi/errors.go:205`)
- Runner API reference exists (`docs/reference/runner-api.md:1`)
- Durable Start is the commit path for operations (`pkg/durable/store.go:38`)
- Poll handler waits for work (`internal/api/runnerapi/protocol.go:348`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (owner-tagged: decision 5).

- Decisions that apply: 5 (current), 13 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "The server tries `Claimer.Claim` at once, then each time that a durable operation of a kind of the pool starts on this instance, and at least every 5 seconds for operations that other instances start."
- Quote: "Limits. A JSON body has at most 4 MiB. A log chunk has at most 1 MiB, a part of a plan at most 256 MiB, and a sweep report at most the limit of ADR 164."
- Quote: "Four paths are not safe to repeat: the rotation, the poll, the handoff and the session exchange (`protocol.NoncePaths`)."
- Effect: Decision 5 is owner-tagged and gives the wake rule. This change follows it: Claim at once, wake on each start of a kind of the pool on the same instance, retry at least every 5 seconds for operations that other instances start. Decision 14 states the size caps in the pack excerpt; the 10 polls and 50 session requests each second are the limits that issues #972 and #979 state as decision 14. This change counts authenticated runner IDs and session IDs per instance. Decision 13 names poll as a nonce path, so a refused poll SHALL not burn a nonce.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- When does a waiting poll retry Claim? ADR 131 decision 5 answers it: Claim at once, then on each start of a kind of the pool on the same instance, and at least every 5 seconds for operations that other instances start. A start of another kind does not wake.
- What are the poll and session rate limits and what about unauthenticated floods? Issues #972 and #979 state the limits as ADR 131 decision 14: above 10 polls each second per runner ID and above 50 requests each second per session ID, counted per instance. Unauthenticated requests fail authentication with 401 and create no limiter entry; a per-IP guard for unauthenticated floods is out of scope and tracked by #670.
- Do the limiter and the one-poll rule share state and which 429 code applies? The limiter and the one-poll rule are independent. A 429 from the limiter does not open or close a poll. A second concurrent poll under the rate gets 429 with the existing one-poll code, not with code rate-limited. A refused poll creates no open poll.
- What covers a lost wake on crash or restart? A lost instance-local signal does not strand work: the 5 s retry for operations that other instances start also bounds a lost wake, a missed publish, or a restart, because a new or still-waiting poll retries Claim on its timer.
- What happens to a waiting poll when its runner is drained, retired, or must-rejoin? Retire ends the wait without work per ADR 133 decision 8. Drain and must-rejoin are distinct per ADR 130 decision 9. A drained runner claims no new work, so its waiting poll keeps waiting without claim until the deadline.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
