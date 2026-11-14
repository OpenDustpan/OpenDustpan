---
version: 2
---

Issue: #982, #1026.

## Why

**Persona: runner operator.** A runner operator runs a pool whose runners claim jobs. The launcher of each claim needs the session paths that move one job phase forward: open the session, renew the claim, read source, get the job token, report steps and logs, and upload and commit the plan. Each path needs a clear cap and a clear error.

The user path this change must make work:

1. **API.** The runner polls and claims a job phase with its runner key. The server takes the claim kinds from the pool.
2. **API.** The launcher opens a job session with the handoff token, then renews the claim through the session renew path.
3. **API.** The launcher reads source, gets the job token, begins and ends steps, streams log chunks, uploads plan parts and commits the plan once.
4. **Docs page.** The runner operator reads docs/reference/runner-api.md for each session path, cap and error code.
5. **E2e.** The step drives the real launcher against POST /runner/v1/session with a handoff token, then the session paths with the session key proof (action runner:claim).

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The launcher calls a session path with a bad, expired or replayed session proof, or with a runner key. | 401 {"code": "bad-proof", "message": "session proof failed"} | `POST /runner/v1/session/renew` (new in this change) with a fresh proof of the session key |
| The launcher writes after a takeover, cancel or expiry ended the claim. | 409 {"code": "claim-lost", "message": "claim no longer live"} | `POST /runner/v1/poll` (route of main under /runner/v1) with the runner key to take the next claim |
| The launcher sends a log chunk above 1 MiB, above a session cap, out of order or with a wrong hash. | 400 {"code": "invalid", "message": "log chunk refused"} | `POST /runner/v1/session/logs` (new in this change) with the same sequence number and the correct Dustpan-Content-SHA256 |
| The launcher commits the plan a second time for one phase. | 409 {"code": "plan-committed", "message": "plan already committed"} | `POST /runner/v1/session/renew` (new in this change) to keep the claim, then stop: the first commit stands |

Evidence for the claims above:

- The sweep claimer port exists and the runnerapi config holds it. Proof: Code fact for runner.SweepClaimer: internal/api/runnerapi/runnerapi.go:61 and pkg/runner/store.go:244 (main list).
- The reference page for the runner API exists and must list each session path. Proof: docs/reference/runner-api.md exists in the pack paths.
- Sweep types live in internal/unmanaged, so sweep code stays in other parts. Proof: Code fact for FindingStore: internal/unmanaged/ports.go:249.

## What Changes

- New routes under /runner/v1/session for a job claim: POST /runner/v1/session, POST /runner/v1/session/renew, GET /runner/v1/session/source and the LFS object path, credentials, step begin and end, POST /runner/v1/session/logs, plan part upload and plan commit.
- A runner key on a session path and a session key on a runner path both answer 401 bad-proof.
- The session paths serve job claims only. A namespace pool with the sweep purpose is refused.
- Upload caps, a log sequence rule, the Dustpan-Content-SHA256 check, and one plan commit with a stored result.
- Entries in docs/reference/runner-api.md for each session path, cap and error code.

Out of scope:

- The sweep report, finish routes and all sweep code.
- The job token issuer, from the first part.
- The launcher binary modes, runner pool routes and the runner pool e2e, in other changes.

Depends on:

- add-job-session-paths-runner
- `add-job-session-paths-runner-token-issuer` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The reference page lists the session paths in call order.
- Each refusal names the cap or rule that failed and the one command that helps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- runner.SweepClaimer is a port with no implementation. (`pkg/runner/store.go:244`)
- The runnerapi config holds the sweep claimer. (`internal/api/runnerapi/runnerapi.go:61`)
- The protocol package mentions the session key. (`pkg/runner/protocol/protocol.go:95`)
- ErrSessionWaitPassed exists in the runner package. (`pkg/runner/runner.go:100`)
- RecordClaim is a store port method. (`pkg/runner/store.go:153`)
- The job token credential kind exists. (`pkg/identity/identity.go:113`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (current), 10 (current), 11 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Logs. The launcher sends the output of each step in chunks of at most 1 MiB, with numbers from 0 and the header `Dustpan-Content-SHA256`."
- Quote: "Plans and outputs. After the last step that names an output, the launcher uploads each part of the plan, then commits the plan in one call."
- Effect: The change serves the session paths, caps and errors as decided.
- Conflict: none.

**ADR 130: Runner pools, runners and coordinators** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 164 changes decision 5; ADR 300 changes; ADR 343 changes decision 3. Chain to the end: ADR 164, ADR 300, ADR 343, ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 164, 300, 343.
- Quote: "A pool has a purpose that never changes (owner)."
- Effect: The session paths take the claim kind from the pool and serve job claims only.
- Conflict: none.

**ADR 135: The source pipeline: snapshots of git packs** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: ADR 199 extends decision 1; ADR 213 extends decision 6; ADR 220 changes decision 9; ADR 222 changes decision 6; ADR 226 changes decision 6. Chain to the end: ADR 199, ADR 213, ADR 220, ADR 222, ADR 226, ADR 286, ADR 310, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 300, ADR 309, ADR 353, ADR 363, ADR 335, ADR 351, ADR 354, ADR 240, ADR 258, ADR 263, ADR 342, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 338, ADR 303, ADR 336, ADR 297, ADR 358, ADR 366, ADR 365, ADR 328, ADR 359, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 199, 213, 220, 222, 226.
- Quote: "The runner never connects to the forge for the code."
- Effect: The source path reads the snapshot through the session.
- Conflict: none.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 20 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "The job token and the job-ID claim."
- Effect: The credentials path serves the token that the first part issues.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The routes stay outside /api/dustpan/v1, so no API description change is needed.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: route `POST /runner/v1/poll` (.errors[1].command) is not in the API description or the route table; use an existing route (nearest: /runner/v1), or name it as new in "what_changes"; a code check fails: task "Add the e2e step for POST /runner/v1/session with a session key proof." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: the state diagram after this change: `ClaimLive` does not resolve on main; use the name main has, or mark it NEW; a code check fails: and 3 more.

Questions considered and answered:

- Can a sweep-purpose pool reach the job session paths? No. Owner decision, 13 Nov 2026 (queue P14), with ADR 130 decision 3: claim kinds come from the pool, and a namespace pool with the sweep purpose is refused.
- Does the Idempotency-Key rule apply? No. ADR 210 decision 2 keeps runner paths outside the tool-neutral API; the replay rule of ADR 131 decision 13 applies.
- What does the credentials path answer when no issuer is wired? It depends on the first part. This change answers 503 until the issuer exists and writes no issue logic.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
