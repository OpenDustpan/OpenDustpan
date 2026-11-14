---
version: 2
---

## Why

**Persona: runner operator.** Runner operator runs cloud sweeps on runners of the installation. The operator needs a sweep poll to claim only sweep work, a report upload to stay bounded and encrypted, and pool setup to refuse a namespace pool with the sweep purpose at once.

The user path this change must make work:

1. **API.** Create an operator pool with the sweep purpose, join a runner, and poll for sweep work under /runner/v1.
2. **API.** Open a job session for the sweep claim, upload the sweep report, then call finish under /runner/v1.
3. **Docs page.** Read the sweep entries in docs/reference/runner-api.md.
4. **E2e.** Drive a real runner poll, report upload and finish against the TLS server.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller creates or updates a namespace pool with the sweep purpose. | 422 {"code": "runner.invalid", "message": "only an installation pool can have the sweep purpose"}; no pool is stored. | Create the pool again with purpose `jobs` through the runner pool routes of the namespace. |
| The report is above the bound, or a line is above 64 KiB. | 413 with a body of the runner protocol error shape; the sweep ends failed and the staged report is dropped. | Upload a smaller report to the new route `POST /runner/v1/session/sweep/report`. |
| The launcher calls finish with no stored report. | 400 {"code": "invalid", "message": "no complete sweep report"}; the sweep ends failed. | Upload the report to the new route `POST /runner/v1/session/sweep/report`, then call the new route `POST /runner/v1/session/sweep/finish`. |

Evidence for the claims above:

- The runner API reference page exists and is the docs target of this change. Proof: docs/reference/runner-api.md

## What Changes

- Add the sweep claimer on durable operations of kind runner.SweepKind(pool), chosen by the stored purpose of the pool.
- Add two new routes under /runner/v1: `POST /runner/v1/session/sweep/report` and `POST /runner/v1/session/sweep/finish`. They stream a bounded report and close the sweep. The launcher opens its session on the existing session route under /runner/v1 with the handoff of the sweep claim.
- The existing poll route under /runner/v1 calls the sweep claimer for a pool with the sweep purpose.
- The pool service refuses a namespace pool with the sweep purpose at create and at update, and accepts it only for an installation pool.
- The pool service refuses a purpose change of a pool that has queued or claimed operations of the old purpose.
- Add the sweep entries to docs/reference/runner-api.md.

Out of scope:

- Sweep target CRUD, sweep start and match
- Launcher run modes and helper modes
- Job claim path for unit jobs
- Console sweep views
- Converter commands of a sweep target

Depends on:

- add-job-session-paths-runner-unmanaged-sweep-store
- `add-job-session-paths-runner-unmanaged-sweep-store` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The runner operator learns the purpose refusal at pool create, and the error names the allowed purpose for a namespace pool.
- A failed report drops its staged blob, so a retry starts clean with no manual cleanup.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- runner.SweepKind(pool) already names the durable operation kind of a sweep pool. (`pkg/runner/runner.go:152 (func SweepKind)`)
- A test shows a release goes through the claimer in the runner protocol adapter. (`internal/api/runnerapi/protocol_review_test.go:22 (func TestReleaseGoesThroughTheClaimer)`)
- The error code runner.invalid exists for a bad pool setting. (`internal/errcode/catalog_runner.go:10 (runner.invalid)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 4 (current), 12 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Each path calls one port. The HTTP adapter of the protocol is a thin layer over the ports of the job model and of this design: - The poll calls `job.Claimer.Claim` for a pool for jobs, and `runner.SweepClaimer.Claim` for a pool for sweeps."
- Quote: "Limits. A JSON body has at most 4 MiB. A log chunk has at most 1 MiB, a part of a plan at most 256 MiB, and a sweep report at most the limit of ADR 164."
- Effect: The poll calls the port runner.SweepClaimer for a sweep pool. The report upload keeps the bound of ADR 164 and the error shape of decision 12.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 3 (current), 5 (changed), 11 (current), 12 (current), 13 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "The durable operation. The start of a sweep starts one durable operation of the kind `unmanaged.sweep.<pool ID>`, with the start key `<sweep ID>`, in the transaction that stores the sweep."
- Quote: "A sweep pool runs only sweeps. A runner pool gets a **purpose**: `jobs` or `sweeps`. A sweep target must name a pool of the purpose `sweeps`, and a job must name a pool of the purpose `jobs`."
- Quote: "Bounds. A report is at most 1 GiB and 5,000,000 records. A line is at most 64 KiB. A record has at most 50 tags, with a key of at most 128 bytes and a value of at most 256 bytes. A header has at most 1,000 scopes."
- Effect: The claimer claims the durable sweep operation by purpose. The report keeps all bounds and the header-scope subset rule. The purpose rule is read as changed by ADR 343.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Sweeps stay on runners. Only an installation pool can have the sweep purpose (Owner decision, 13 Nov 2026 (queue P14)).
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The sweep session routes stay under /runner/v1.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: route `POST /runner/v1/join` (.tasks[2].items[0].text) is not in the API description or the route table; use an existing route (nearest: /runner/v1), or name it as new in "what_changes"; a code check fails: route `POST /runner/v1/poll` (.tasks[2].items[0].text) is not in the API description or the route table; use an existing route (nearest: /runner/v1), or name it as new in "what_changes"; a code check fails: task "Add an e2e step runner-sweep-claim. A runner joins a sweep pool with a" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Which pools may have the sweep purpose? Owner decision, 13 Nov 2026 (queue P14): only an operator (installation) pool. The pool service refuses a namespace pool with the sweep purpose at create and update.
- How do converters fit the purpose rule? ADR 343 extends ADR 164 decision 9: converters run on a pool with the sweep purpose. Claim is by operation kind, so no new claim path.
- Who drops a staged blob after a server crash? Staged report blobs use a staging key. The existing orphan cleanup (ADR 151 decision 22) deletes a blob with no record, so a crash leaves no kept partial blob.
- What bound applies to the report? ADR 131 decision 14 and ADR 164 decision 11: 1 GiB, 5,000,000 records, 64 KiB a line, 50 tags, 1,000 scopes, and the session caps.
- Which error codes does the report use? ADR 131 decision 12 gives the table: invalid is 400, and a body above a cap is refused with the protocol error shape. A pool refusal uses runner.invalid.
- Who moves a sweep to failed after a bad report or a lost claim? The service of the sweep store does it through its advance call under the claim that the session holds. A call under a lost claim changes nothing and answers 409.
- How is a race of two uploads or two finishes handled? A unique staged key for each sweep session and a state check in one store transaction let one request win. The other answers 409 for an upload, or the stored result for a finish.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
