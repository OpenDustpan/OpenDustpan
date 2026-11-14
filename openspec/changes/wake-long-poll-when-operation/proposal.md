Issue: #972, #979.

## Why

**Persona: runner operator.** As a Runner operator I run pools that claim plans and applies. I need a waiting runner to claim new work at once when it starts on the same instance, and I need clear 429 answers when a runner or session polls too fast, so jobs start fast and a hot runner does not overload the server.

The user path this change must make work:

1. **API.** Runner sends POST /runner/v1/poll and waits up to 50 s for work.
2. **API.** Operator starts a job; a waiting runner of the pool claims it within 1 s when the start commits on the same instance.
3. **Docs page.** Operator reads the runner API reference for poll wait, 429 rate-limited, Retry-After, and per-instance count.
4. **E2e.** Operator runs a runner against the real server and watches claim latency and 429 behavior.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Poll waits and finds no work | 204 with empty body: no work before the 50 s deadline. Send the next poll. | `POST /runner/v1/poll` |
| Rate limit above 10 polls or 50 session requests each second | 429 rate-limited with Retry-After: 1: too many polls or session requests. Wait 1 second and retry. | `POST /runner/v1/poll` |

Evidence for the claims above:

- Poll waits on a fixed interval with no other wake. Proof: internal/api/runnerapi/protocol.go:396: pause := time.NewTimer(h.interval)
- Runner protocol error path writes 429 answers and sets Retry-After on 429 and 503. Proof: internal/api/runnerapi/errors.go
- Runner API reference path exists in this repo. Proof: docs/reference/runner-api.md
- Durable package path exists in this repo. Proof: pkg/durable

## What Changes

- Instance-local wake signal per durable kind, fired after commit on both start paths, and poll wait on signal with Claim at once, 5 s timer, and 50 s deadline.
- Per-runner poll limiter at 10 polls each second on POST /runner/v1/poll.
- Per-session limiter at 50 requests each second on session paths keyed by session ID.
- Bounded limiter state with 5 minute idle eviction and 429 rate-limited with Retry-After: 1.
- Runner API reference states the per-instance count.

Out of scope:

- Wake across instances such as PostgreSQL LISTEN and NOTIFY.
- Wake on release or lease expiry.
- Limits shared across instances.
- Limits on the management API.
- Per-IP guard for unauthenticated floods.

Depends on:

- runners
- add-job-session-paths-runner-token-issuer
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A runner that polls fast sees 429 and honors Retry-After: 1 instead of a fixed sleep.
- A poll that waits 50 s with no work is normal and not an error.
- Per-instance counts surprise operators with several instances behind a balancer, so the docs state it.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (owner-tagged: decision 5).

- Decisions that apply: 5 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "The server tries `Claimer.Claim` at once, then each time that a durable operation of a kind of the pool starts on this instance, and at least every 5 seconds for operations that other instances start."
- Quote: "Limits. A JSON body has at most 4 MiB. A log chunk has at most 1 MiB, a part of a plan at most 256 MiB, and a sweep report at most the limit of ADR 164."
- Effect: Decision 5 is owner-tagged and gives the wake rule. This change follows it: Claim at once, wake on each start of a kind of the pool on the same instance, retry at least every 5 seconds for operations that other instances start. Decision 14 is not owner-tagged and gives the size limits. The 10 polls and 50 session requests each second come from issues #972 and #979 as the reading of decision 14; this change counts authenticated runner IDs and session IDs per instance.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): ADR 131 decision 5 (owner) says the server tries Claim when a durable operation of a kind of the pool starts on this instance, and 'at least every 5 seconds' for other instances; the change's Requirement 'Wake on same-instance start' limits wake to the poll's own pool kinds but the spec's scenario 'Other kind does not wake' is a provisional choice not stated in the owner-tagged decision, and the proposal's ADR check quotes decision 5 only partially; the 'Changed by' chain and quote must be reviewed, but the wake rule itself matches. Resolution: answered: ADR check quotes decision 5 wake rule verbatim, marks decision 5 owner and decision 14 not owner, and wake requirement limits wake to kinds of the pool with 5 s fallback for other instances.
- R1.2 (adr conflict): ADR 131 decision 14 (owner-tagged via ADR 131 decision 5 scope) sets limits of 10 polls each second per runner and 50 session requests each second; the design's Layers table makes the poll limiter count only authenticated requests after proof, so a runner rate is applied after Claim-path work is already done, contradicting the requirement that the limiter answer before Claim or session work. Resolution: answered: Design orders proof and authentication, then limiter, then one-poll rule and Claim; spec requires 429 before work.
- R1.3 (failure mode): The hub publishes after commit but there is no requirement for a poll that subscribes between a commit and its publish, or for a publish lost on restart or on a crashed request; a poll can miss a wake and wait up to 5 s with no test covering the race between subscribe and publish. The design's risk note says fake clock tests hide this race, but no scenario or test covers it. Resolution: answered: Design adds Claim at once on subscribe and spec adds commit-before-subscribe and lost-signal scenarios with mapped tests.
- R1.4 (missing scenario): No scenario for the one-poll-at-a-time rule combined with the rate limit: the design requires a second concurrent poll to get 429 even under the rate, but the spec has no requirement for it and no task test maps to it. Resolution: answered: Spec adds the one-poll rule requirement with second-concurrent-poll scenario and mapped failing test.
- R1.5 (missing scenario): No scenario for a poll that is rate limited while waiting, or for a poll whose runner is drained, retired or must-rejoin during its 50 s wait; the retire rule of ADR 133 decision 8 ends sessions but the wait behavior is not specified. Resolution: answered: Spec adds retired-ends-wait and drained-keeps-wait requirements with scenarios; must-rejoin covered in considered.
- R1.6 (failure mode): Hostile input: the design's Layers say the port adapter counts only authenticated requests, so an attacker with a valid runner key can still flood polls; no scenario covers a legitimate key used at 10x rate across many runners, and per-instance counts let an attacker multiply the limit by the number of instances behind a balancer. Resolution: answered: Layers count only authenticated IDs, limiter runs before work, and docs state per-instance count so balancer multiplication is visible; multi-runner flood covered by per-ID isolation scenario.
- R1.7 (open question): The idle TTL is provisionally 5 minutes, but the spec says 'the idle TTL' without a value; a session that polls every 6 minutes would be evicted with its window reset, which lets a burst bypass the limit. The eviction interaction with the token bucket state is not specified. Resolution: answered: Spec fixes idle eviction at 5 minutes in requirement text; burst after eviction starts a new window by definition of fixed window.
- R1.8 (missing scenario): No scenario for Retry-After value: the spec does not say how many seconds the header carries or how it is computed from the bucket state. Resolution: answered: Spec fixes Retry-After at 1 second with its scenario.
- R1.9 (wrong claim): The proposal's evidence says 'Runner protocol error path sets Retry-After on 429 and maps one-poll rule to rate-limited' with no line; the issue says writeError sets Retry-After on 429 and 503, so the claim that the one-poll rule maps to rate-limited is not shown by the cited file. Resolution: answered: Evidence no longer claims one-poll maps to rate-limited; it cites errors.go for 429 and Retry-After only.
- R1.10 (scope): Task 3.1 requires a claim within 1 s for both tools, but the proposal's user path and the spec cover only the runner; the e2e step against the real directory is not mapped to a scenario, and the issue acceptance criteria do not include TLS. Resolution: answered: Tasks map e2e step to wake scenario on one instance with real runner; no TLS or both-tools claim.
- R1.11 (open question): The session limiter's key is the session ID, but a session with a new claim after handoff gets a new session key; the change does not say whether the count resets across sessions of one claim. Resolution: answered: Spec states session key is the session ID so a new job session starts a new window.
- R2.1 (adr conflict): design.md Layers table says the limiter counts only authenticated runner IDs after proof, while ADR 131 decision 14 limits polls per runner and the change puts the limiter after proof; the change also cannot answer unauthenticated floods without a per-IP guard, contradicting the stated ordering that limiter runs before Claim work only for authenticated IDs. Quote ADR 131 decision 14: "The server answers `rate-limited` to a runner above 10 polls each second". Owner decides whether unauthenticated floods need a limiter. Resolution: answered: Design orders proof and authentication before the limiter, counts only authenticated IDs, notes unauthenticated floods get 401 with no limiter entry, and moves per-IP guard out of scope.
- R2.2 (adr conflict): proposal.md ADR check marks ADR 131 as owner-tagged via decision 5 but the decision 14 limits (which the change implements) are not owner-tagged; the owner-tag rule and the quoted decision 5 text do not match the proof. Also the quoted decision 5 says 'at least every 5 seconds for operations that other instances start', while the spec 'Keep five second fallback' applies 5 s retry to all waiting polls, which is a different rule for same-instance kinds. Resolution: answered: ADR check quotes decision 5 wake rule and decision 14 limits text, marks decision 5 owner and decision 14 not owner, and the fallback requirement now names operations that other instances start.
- R2.3 (failure mode): No requirement covers a publish lost on a crash, restart or failed commit acknowledgment; the signal is instance-local memory, so a restart loses wakes. The 5 s fallback is said to cover other instances only. Scenario for restart during wait is missing. Resolution: answered: Spec adds a lost-signal requirement bounded by the 5 s tick with crash and restart scenarios.
- R2.4 (missing scenario): The spec has no scenario for the rate limit combined with a waiting poll: a runner that already holds one open poll and sends another poll within the same second, plus Claim-on-subscribe under limiter refusal. The one-poll rule answer is 429 but the change does not say whether that 429 uses code rate-limited or another code (ADR 131 error table). Resolution: answered: Spec separates limiter 429 rate-limited from one-poll 429 with the existing one-poll code and adds the combined waiting-poll plus limiter scenario.
- R2.5 (open question): Limiter key for a poll is the runner ID, but the spec does not say whether the one-poll rule and the rate limiter share state or whether a 429 from the limiter closes an open poll. The open-question section says 'none' while this is undecided. Resolution: answered: Spec and design state the limiter and the one-poll rule are independent and a limiter 429 opens and closes no poll.
- R2.6 (wrong claim): Proposal evidence cites docs/reference/runner-api.md as needing the per-instance note but the change does not verify the file exists or what it says; the 'Runner API reference exists' claim has no inline check output. Resolution: answered: Evidence now claims only that docs/reference/runner-api.md and pkg/durable paths exist in the pack, with exact paths.
- R2.7 (scope): Tasks 2.2 and 3.2 mix the docs update into the limiter build while the issue #979 acceptance criteria say the 51st session request test lands with #970; the change adds session limits without checking that #970 is merged, although proposal lists #970 as a dependency. Resolution: answered: Tasks gate the 51st session request test on #970 paths and depends_on keeps #970, with the docs update in its own task.
- R2.8 (missing scenario): No scenario for Retry-After on the 204 wait path or for a poll whose runner is drained during its 50 s wait; draining is a runner status in ADR 130 decision 9 but the retire scenario only covers retire. Resolution: answered: Spec adds a drained-runner waiting-poll requirement distinct from retire.
- M.20 (dependency): The dependencies are issue IDs (#881, #970) and not change IDs. Resolution: answered: depends_on now lists change IDs runners and add-job-session-paths-runner-token-issuer instead of issue IDs.

## Open questions

none

Questions considered and answered:

- When does a waiting poll retry Claim? ADR 131 decision 5 answers it: Claim at once, then on each start of a kind of the pool on the same instance, and at least every 5 seconds for operations that other instances start. A start of another kind does not wake.
- What are the poll and session rate limits and what about unauthenticated floods? Issues #972 and #979 with ADR 131 decision 14 answer it: above 10 polls each second per runner ID and above 50 requests each second per session ID, counted per instance. Unauthenticated requests fail authentication with 401 and create no limiter entry; a per-IP guard for unauthenticated floods is out of scope and tracked by #670.
- Do the limiter and the one-poll rule share state and which 429 code applies? The limiter and the one-poll-at-a-time rule are independent. A 429 from the limiter does not open or close a poll. A second concurrent poll under the rate gets 429 with the existing one-poll code, not with code rate-limited. A refused poll creates no open poll.
- What covers a lost wake on crash or restart? A lost instance-local signal does not strand work: the 5 s retry for operations that other instances start also bounds a lost wake, a missed publish, or a restart, because a new or still-waiting poll retries Claim on its timer.
- What happens to a waiting poll when its runner is drained, retired, or must-rejoin? Retire ends the wait without work per ADR 133 decision 8. Drain and must-rejoin are distinct per ADR 130 decision 9. A drained runner claims no new work, so its waiting poll keeps waiting without claim until the deadline.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
