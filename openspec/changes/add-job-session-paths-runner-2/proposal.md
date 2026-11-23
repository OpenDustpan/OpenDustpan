---
version: 13
---

Issue: #982, #1026.

## Why

**Persona: runner operator.** A runner operator runs a pool whose runners claim jobs. The launcher of each claim needs the session paths that move one job phase forward: open the session, renew the claim, get the job token, report steps and logs, and upload and commit the plan. Each path needs a clear cap and a clear error.

The user path this change must make work:

1. **API.** The runner polls and claims a job phase with its runner key. The server takes the claim kinds from the pool.
2. **API.** The launcher opens a job session with the handoff token, then renews the claim through the session renew path.
3. **API.** The launcher begins and ends steps, streams log chunks, uploads plan parts and commits the plan. A repeat of the same commit answers 204.
4. **Docs page.** The runner operator reads docs/reference/runner-api.md for each session path, cap and error code.
5. **E2e.** The step drives the e2e launcher client (`e2e.FakeLauncher`, which speaks the session protocol; the `dustpan-launcher` binary has its own change) against POST /runner/v1/session with a handoff token, then the session paths with the session key proof (action runner:claim).

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The launcher calls a session path with a bad, expired or replayed session proof, or with a runner key. | 401 {"code": "bad-proof", "message": "session proof failed"} | `POST /runner/v1/session/renew` (new in this change) with a fresh proof of the session key |
| The launcher writes after a takeover, cancel or expiry ended the claim. | 409 {"code": "claim-lost", "message": "claim no longer live"} | `POST /runner/v1` poll path of the runner (route of main under /runner/v1) with the runner key to take the next claim |
| The launcher sends a log chunk above 1 MiB, above a step or phase cap, out of order or with a wrong hash. | 400 {"code": "invalid", "message": "log chunk refused"} | `POST /runner/v1/session/logs` (new in this change) with the same sequence number and the correct Dustpan-Content-SHA256 |
| The launcher commits other plan parts after a commit stored the plan. | 409 {"code": "conflict", "message": "..."} (ADR 131 decision 13; a repeat with the same parts and hashes answers 204) | `POST /runner/v1/session/renew` to keep the claim, then stop: the first commit stands |

Evidence for the claims above:

- The sweep claimer port exists and the runnerapi config holds it. Proof: Code fact for runner.SweepClaimer: internal/api/runnerapi/runnerapi.go:61 and pkg/runner/store.go:244 (main list).
- The reference page for the runner API exists and must list each session path. Proof: docs/reference/runner-api.md exists in the pack paths.
- Sweep types live in (read only) `internal/unmanaged`, so sweep code stays in other parts. Proof: Code fact for FindingStore: (read only) `internal/unmanaged/ports.go` line 249.

## What Changes

Most of the plan paths and session routes are already on main. This change adds only what main lacks. After the re-check against main 18f6d4d3d, the gaps are the phase caps of the log, the hostile tests of the plan caps and of a sweep-purpose pool at the session paths, and the docs rows for the caps.

- Main already has the session routes: POST /runner/v1/session, /session/renew, /session/steps/{step}/begin and /end, /session/steps/{step}/logs/{seq} and /session/finish (internal/api/runnerapi/routes.go, PathSession* in pkg/runner/protocol/protocol.go:75-117), with tests in internal/api/runnerapi/session_test.go and internal/api/runnerapi/session_guard_test.go.
- Main already routes and tests the three plan paths: PathSessionPlanUpload, PathSessionPlanCommit and PathSessionPlanPart, through `planRoutes` (internal/api/runnerapi/plan.go:22-47, called from `buildRoutes` in internal/api/runnerapi/routes.go:63). Their tests are in internal/api/runnerapi/plan_test.go (TestPlanUploadAndCommit, TestPlanCommitIsChecked, TestPlanRoutesWithALostClaim, TestPlanRoutesNeedTheKeyOfTheSession). The docs rows are at docs/reference/runner-api.md:170-172 and the e2e step is e2e/path_runner_apply_test.go. This change does not route them again.
- The server refuses a log step with more than `protocol.MaxLogChunksPerStep` chunks (internal/api/runnerapi/session.go:557-575). `protocol.MaxLogBytesPerStep`, `protocol.MaxLogChunksPerPhase` and `protocol.MaxLogBytesPerPhase` (pkg/runner/protocol/protocol.go:156-162) are declared and the server does not enforce them: only the launcher checks the step cap (the log writer of the launcher run package). This change adds the server check of the three limits, in the transaction of the chunk write, with hostile tests.
- A runner key on a session path and a session key on a runner path both answer 401 bad-proof. Main has tests for the plan paths (TestPlanRoutesNeedTheKeyOfTheSession); this change adds the same hostile test for the log and step paths.
- The session paths serve job claims only. Main refuses a sweep claim at the exchange (TestSweepExchangeIsRefused, internal/api/runnerapi/session_guard_test.go:337). This change adds the hostile test that a pool with the sweep purpose gets no job session. It does not refuse the sweep purpose on a pool: main has no installation (operator) pool, and namespace pools with the sweep purpose are a working feature of the scans (see "Not decided here" under the owner decision).
- A plan commit follows ADR 131 decision 13. A repeat with the same parts and hashes under the same claim answers 204. Other parts after a commit answer `conflict`. Main already does this (the CommitPlan method of the job plan recorder port; TestPlanUploadAndCommit expects 204 for the second commit). This change adds no new error code.
- Entries in docs/reference/runner-api.md for the log and plan caps (per phase and per step).

Out of scope:

- The source paths PathSessionSource and PathSessionSourceObject (pkg/runner/protocol/protocol.go:83-86). The server has no source pipeline to serve them: runnerapi.Config holds no source dependency, the assignment has `Source: nil` (internal/api/runnerapi/session.go:362), and the snapshot store port of the source package has no adapter. Follow-up: the source paths come in a later change, after the source pipeline of ADR 135 lands.
- The sweep report, finish routes and all sweep code.
- The credentials path and the job token issuer (`add-job-session-paths-runner-token-issuer`).
- The launcher binary modes, runner pool routes and the runner pool e2e, in other changes.

Depends on:

- `add-job-session-paths-runner-token-issuer` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The reference page lists the session paths in call order.
- Each refusal names the cap or rule that failed and the one command that helps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- runner.SweepClaimer is a port with no implementation. (`pkg/runner/store.go:244`)
- The runnerapi config holds the sweep claimer. (`internal/api/runnerapi/runnerapi.go:61`)
- The protocol package mentions the session key. (`pkg/runner/protocol/protocol.go:115`)
- ErrSessionWaitPassed exists in the runner package. (`pkg/runner/runner.go:100`)
- RecordClaim is a store port method. (`pkg/runner/store.go:153`)
- The job token credential kind exists. ((read only) `pkg/identity/identity.go` line 113)
- The three plan paths are routed by `planRoutes` and tested. (`internal/api/runnerapi/plan.go:22`, `internal/api/runnerapi/routes.go:63`, `internal/api/runnerapi/plan_test.go:301`)
- A second plan commit with the same parts answers 204. (the CommitPlan method of the job plan recorder port, `internal/api/runnerapi/plan_test.go:314`)
- The step log cap is checked in the handler. (`internal/api/runnerapi/session.go:557`)
- The per-phase and per-step byte limits are constants with no server check. (`pkg/runner/protocol/protocol.go:156-162`)
- The assignment carries no source. (`internal/api/runnerapi/session.go:362`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

Not decided here: main has no installation (operator) pool, and every pool belongs to a namespace (`pkg/runner/runner.go:232-236`). Main creates and uses namespace pools with the sweep purpose in the unmanaged scans and in the conformance suite. A refusal in the pool admin API or in `Pool.Validate` would stop that feature. So this change adds no refusal of the sweep purpose on a pool. The session paths already refuse a claim of a sweep-purpose pool. The refusal of the owner decision waits for an installation pool, and the owner decides the order. The question is in the decision queue.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (current), 10 (current), 11 (current), 13 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Logs. The launcher sends the output of each step in chunks of at most 1 MiB, with numbers from 0 and the header `Dustpan-Content-SHA256`."
- Quote: "Plans and outputs. After the last step that names an output, the launcher uploads each part of the plan, then commits the plan in one call."
- Quote: "A plan commit with the same parts and hashes under the same claim changes nothing and answers 204. A plan commit with other parts after a commit fails with `conflict`." (docs/adr/0131-the-runner-protocol.md:214-216, decision 13). Changed-by check: ADR 139 extends decision 6; no later ADR in the chain changes decision 13. The change keeps this rule and adds no new error code for a repeat commit.
- Effect: The change adds the phase caps and tests for the session paths as decided.
- Conflict: none.

**ADR 130: Runner pools, runners and coordinators** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 164 changes decision 5; ADR 300 changes; ADR 343 changes decision 3. Chain to the end: ADR 164, ADR 300, ADR 343, ADR 166, ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 164, 300, 343.
- Quote: "A pool has a purpose that never changes (owner)."
- Effect: The session paths take the claim kind from the pool and serve job claims only.
- Conflict: none.

**ADR 100: A shared job model on durable operations** (not owner-tagged).

- Decisions that apply: 20 (changed).
- Changed by: ADR 110 extends decision 17; ADR 125 changes; ADR 128 changes decision 17; ADR 130 extends; ADR 135 extends decision 5; ADR 139 extends decision 5; ADR 144 changes decision 20; ADR 145 extends; ADR 148 extends decision 4; ADR 190 changes decision 4; ADR 213 changes decision 20; ADR 258 extends decision 4; ADR 263 extends decision 4; ADR 286 changes; ADR 299 changes decisions 12 and 15; ADR 304 changes decision 2; ADR 304 changes decision 20; ADR 314 extends; ADR 353 extends decision 4. Chain to the end: ADR 110, ADR 125, ADR 128, ADR 130, ADR 135, ADR 139, ADR 144, ADR 145, ADR 148, ADR 190, ADR 213, ADR 258, ADR 263, ADR 286, ADR 299, ADR 304, ADR 314, ADR 353, ADR 210, ADR 218, ADR 222, ADR 225, ADR 305, ADR 308, ADR 354, ADR 227, ADR 164, ADR 300, ADR 343, ADR 199, ADR 220, ADR 226, ADR 282, ADR 149, ADR 224, ADR 259, ADR 146, ADR 197, ADR 203, ADR 204, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334.
- Current rule: this ADR, with ADR 110, 125, 128, 130, 135, 139, 144, 145, 148, 190, 213, 258, 263, 286, 299, 304, 314, 353.
- Quote: "The job token and the job-ID claim."
- Effect: None for this change. The credentials path is in `add-job-session-paths-runner-token-issuer`.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The routes stay outside /api/dustpan/v1, so no API description change is needed.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Can a sweep-purpose pool reach the job session paths? No. Owner decision, 13 Nov 2026 (queue P14), with ADR 130 decision 3: claim kinds come from the pool, and a pool with the sweep purpose gets no job session. The refusal of the sweep purpose on a namespace pool is not part of this change.
- Where do the source paths go? Out of this change: the source pipeline of ADR 135 has no adapter on main. A later change adds them after the pipeline lands.
- Does the Idempotency-Key rule apply? No. ADR 210 decision 2 keeps runner paths outside the tool-neutral API; the replay rule of ADR 131 decision 13 applies.
- Who routes the credentials path? `add-job-session-paths-runner-token-issuer`. This change adds no credentials route and no issue logic.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
