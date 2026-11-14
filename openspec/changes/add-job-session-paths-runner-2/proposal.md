Issue: #982, #1026.

## Why

**Persona: runner operator.** A runner operator runs a pool whose runners claim jobs. The launcher of each claim needs the session paths that move one job phase forward: open the session, renew the claim, read source, get the job token, report steps and logs, and upload and commit the plan. Each path needs a clear cap and a clear error.

The user path this change must make work:

1. **API.** The runner polls and claims a job phase with its runner key. The server takes the purpose from the pool and refuses a pool with the sweep purpose on a job claim.
2. **API.** The launcher opens a job session with the handoff token, then renews the claim through the session renew path.
3. **API.** The launcher reads source, gets the job token, begins and ends steps, streams log chunks, uploads plan parts and commits the plan once.
4. **Docs page.** The runner operator reads docs/reference/runner-api.md for each session path, cap and error code.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The launcher calls a session path with a bad, expired or replayed session proof, or with a runner key. | 401 {"code": "bad-proof", "message": "session proof failed"} | `POST /runner/v1/session/renew` with a fresh proof of the session key |
| The launcher renews or writes after a takeover, cancel or expiry ended the claim. | 409 {"code": "claim-lost", "message": "claim no longer live"} | `POST /runner/v1/session` with a new handoff token to open a session for the next claim |
| The launcher sends a log chunk above 1 MiB, above a session cap, out of order or with a wrong hash. | 400 {"code": "invalid", "message": "log chunk refused"} | `POST /runner/v1/session/renew` to read the claim state, then send the chunk again with the next sequence number |
| The launcher commits the plan a second time for one phase. | 409 {"code": "plan-committed", "message": "plan already committed"}; the stored first commit is unchanged | `POST /runner/v1/session/renew` to confirm the claim, then stop: the first commit stands |

Evidence for the claims above:

- The runner protocol and its session ports exist in the repo. Proof: pkg/runner/store.go exists. The code fact for runner.SweepClaimer shows pkg/runner/protocol/protocol.go:95 and internal/api/runnerapi/runnerapi.go:61.
- The reference page for the runner API exists and must list each session path. Proof: docs/reference/runner-api.md exists in the pack paths.
- Sweep types live in internal/unmanaged, so sweep code stays in other parts. Proof: The code fact for FindingStore shows internal/unmanaged/ports.go:249.

## What Changes

- New session routes under /runner/v1/session for a job claim: POST /runner/v1/session (exchange), POST /runner/v1/session/renew, source and LFS object, credentials, step begin and end, POST /runner/v1/session/logs, plan part upload and POST /runner/v1/session/plan-commit.
- Fail-closed credential split: a runner key on a session path and a session key on a runner path both answer 401 bad-proof.
- Purpose rule: the session paths serve job claims only. A pool with the sweep purpose never gets a job claim.
- Upload caps, log sequence stored before the chunk counts, the Dustpan-Content-SHA256 check, and one plan commit with a stored result.
- Reference entries in docs/reference/runner-api.md for each session path, cap and error code.

Out of scope:

- The sweep report, finish routes and all sweep code.
- The job token issuer itself, from the first part.
- The launcher binary modes, runner pool routes and the runner e2e pool run, in other changes.

Depends on:

- add-job-session-paths-runner
- `add-job-session-paths-runner-token-issuer` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The reference page lists the session paths in call order, so a runner operator finds each step in one read.
- Each refusal names the cap or the rule that failed and the one command that helps.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (current), 4 (current), 7 (current), 10 (current), 11 (current), 12 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Two callers, two credentials."
- Quote: "The paths of the session."
- Quote: "Each path calls one port."
- Quote: "Who renews a claim."
- Quote: "Logs. The launcher sends the output of each step in chunks of at most 1 MiB, with numbers from 0 and the header `Dustpan-Content-SHA256`."
- Quote: "Plans and outputs. After the last step that names an output, the launcher uploads each part of the plan, then commits the plan in one call."
- Effect: The change serves the session paths, caps and errors as decided, with no change to the path table.
- Conflict: none.

**ADR 130: Runner pools, runners and coordinators** (owner-tagged: decision 3, 5).

- Decisions that apply: 3 (changed), 5 (changed).
- Changed by: ADR 164 changes decision 5; ADR 300 changes; ADR 343 changes decision 3. Chain to the end: ADR 164, ADR 300, ADR 343, ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 164, 300, 343.
- Quote: "A pool has a purpose that never changes (owner)."
- Quote: "A pool for sweeps is read-only (owner)."
- Effect: The session paths take the claim kind from the pool purpose and serve job claims only. A sweep-purpose pool is an operator pool, and a namespace pool with that purpose is refused.
- Conflict: none.

**ADR 135: The source pipeline: snapshots of git packs** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 199 extends decision 1; ADR 213 extends decision 6; ADR 220 changes decision 9; ADR 222 changes decision 6; ADR 226 changes decision 6. Chain to the end: ADR 199, ADR 213, ADR 220, ADR 222, ADR 226, ADR 286, ADR 310, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 300, ADR 309, ADR 353, ADR 363, ADR 335, ADR 351, ADR 354, ADR 240, ADR 258, ADR 263, ADR 342, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 338, ADR 303, ADR 336, ADR 297, ADR 358, ADR 366, ADR 365, ADR 328, ADR 359, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 199, 213, 220, 222, 226.
- Quote: "The runner never connects to the forge for the code."
- Effect: The source path reads the snapshot through the session. A failed snapshot fails the phase with the reason of the fetch.
- Conflict: none.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 20 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "The job token and the job-ID claim."
- Effect: The credentials path serves the token that the first part issues. It adds no issue logic.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The change never states the rule of owner decision P14: a namespace pool with the sweep purpose must be refused, and the session paths must not let a sweep purpose reach job claims. The spec in specs/runner-session-paths/spec.md has no requirement for the purpose check, and the design's newSessionClaim check omits the pool purpose rule the proposal relies on. Resolution: answered: A requirement and a scenario refuse a job session for a sweep-purpose pool, from the owner decision P14.
- R1.2 (adr conflict): Decision 1 of ADR 131 says the launcher calls the session paths with a session key and a runner key cannot write a step, log or plan. The spec does not state the fail-closed rule for a runner key on session paths and a session key on runner paths; the risk list names it but no requirement or scenario covers it. Resolution: answered: The Credential split requirement covers both key directions with a scenario each.
- R1.3 (wrong claim): The proposal cites an evidence path pkg/unmanaged as existing, but the change's own open questions report that the path does not exist in the checkout. The sweep scope rests on this claim and the proof is not valid. Resolution: answered: The evidence now says the sweep types are in internal/unmanaged and that pkg/unmanaged does not exist.
- R1.4 (wrong claim): The error table gives POST /runner/v1/session/renew as the command that helps with a bad proof, but this route is not a diagnostic command; the open questions also report the route is absent from the API description. The error table and the docs requirement cannot be built as written. Resolution: answered: The error commands name only the new session paths of this change and the one command that helps.
- R1.5 (missing scenario): Decision 2 and the proposal say a repeated commit fails with the same stored result, but the spec scenario only says the second commit fails without change. The stored result and the status code 409 plan-committed are not specified, and the tasks check 413 over cap, which the protocol ADR does not define for plan parts. Resolution: answered: A scenario fixes 409 plan-committed with the first commit kept. The 413 task check is removed.
- R1.6 (failure mode): The spec covers no crash between the log chunk write and the sequence update. A restart after bytes are stored but before the counter moves can accept a duplicate chunk or lose a chunk, and no requirement fixes the order of the write and the counter. Resolution: answered: The design stores bytes first and moves the counter after. A scenario covers the crash.
- R1.7 (failure mode): A concurrent renew and commit from the runner and the launcher, or a takeover during a plan upload, is not covered. The claim fence check happens once in newSessionClaim, not per write, so a takeover mid-stream can still write. Resolution: answered: The design checks the claim on each write, and a takeover scenario covers it.
- R1.8 (missing scenario): The source read path has no scenario for a snapshot that failed, for an unknown commit, or for an LFS object that is absent. ADR 135 decision 3 says the phase fails with the reason of source.fetch; the spec does not say so. Resolution: answered: A scenario covers a failed snapshot and an absent LFS object.
- R1.9 (open question): The credentials path behavior after the job token expires mid-phase, and whether the token is reissued, is not decided. ADR 100 decision 20 covers the expiry but not renewal of the token text during a long phase. Resolution: answered: The considered section says the token has the expiry of the claim and the path answers the token of the live claim.
- R1.10 (scope): The tasks put the e2e check on a Pulumi preview and tofu plan, but the change does not own the runner e2e pool run, which the proposal places in another change. The check cannot pass without that other change. Resolution: answered: The e2e step now uses the real launcher in this change and no longer depends on the pool e2e change.

## Open questions

none

Questions considered and answered:

- Which paths, caps and error codes apply? ADR 131 decisions 3, 12 and 14 give them.
- Can a sweep-purpose pool reach the job session paths? No. Owner decision, 13 Nov 2026 (queue P14), with ADR 130 decisions 3 and 5: the claim kinds come from the pool, and a namespace pool with the sweep purpose is refused.
- What happens when the job token expires in a long phase? ADR 100 decision 20 gives the token the expiry of the claim. The launcher renews the claim, and the credentials path answers the token of the live claim. A lost claim gives claim-lost.
- What happens when the snapshot failed or an LFS object is absent? ADR 135 decision 3: the phase fails with the reason of the source fetch. The path answers not found for an absent object.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
