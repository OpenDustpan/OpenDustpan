---
version: 5
---

Issue: #1028

## Why

**Persona: runner operator.** Runner operator runs cloud sweeps on the runners of the team. The operator needs three things. A sweep poll claims only sweep work. A report upload stays bounded and encrypted. The pool API accepts a team pool with the sweep purpose.

The user path this change must make work:

1. **API.** Create a pool of the namespace with the sweep purpose, join a runner, and poll for sweep work under /runner/v1.
2. **API.** Open a job session for the sweep claim, then upload the sweep report with `PUT /runner/v1/session/sweep-report`. The upload ends the sweep.
3. **Docs page.** Read the sweep entries in docs/reference/runner-api.md.
4. **E2e.** Drive a real runner poll and report upload against the TLS server.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller changes the purpose of a pool. | 409 with the immutable error of the pool routes. The purpose stays. | Create a new pool with the other purpose through the runner pool routes of the namespace. |
| The report is above the bound, or a line is above 64 KiB. | 413 with a body of the runner protocol error shape; the sweep ends failed and the staged report is dropped. | Upload a smaller report to `PUT /runner/v1/session/sweep-report`. |
| A person cancels the sweep after the runner claimed it, and before the server moved the sweep to running. | The poll answers 204 and the claim is released. The sweep stays canceled. | Start the sweep again through the sweep start route. |
| The launcher calls finish for a sweep session. | 400 {"code": "invalid", "message": "a sweep has no finish"}. The sweep state stays. | Upload the report to `PUT /runner/v1/session/sweep-report`; the upload ends the sweep. |

Evidence for the claims above:

- The runner API reference page exists and is the docs target of this change. Proof: docs/reference/runner-api.md

## What Changes

- Add the sweep claimer on durable operations of kind runner.SweepKind(pool), chosen by the stored purpose of the pool.
- The report route `PUT /runner/v1/session/sweep-report` already has its path constant in pkg/runner/protocol/protocol.go. Add its handler in the NEW file internal/api/runnerapi/sweep_report.go. It streams a bounded report, stores it encrypted, and ends the claim and the sweep through `runner.SweepClaimer.Report`. The change adds no finish route: a sweep has no finish (ADR 131 and ADR 164 decision 12, and the code in internal/api/runnerapi/session.go refuses it). The launcher opens its session on the existing session route with the handoff of the sweep claim.
- Add two NEW small interfaces that the claimer consumes, `runner.SweepReportStore` (encrypted staged blob write with the bounds) and `runner.SweepScopeReader` (the scopes of the target of the sweep), and the NEW sentinel `runner.ErrReportTooLarge` for a size breach. All three live in the NEW file pkg/runner/sweep_claimer.go. The sentinel wraps `runner.ErrInvalid`, so the documented contract of `SweepClaimer.Report` in pkg/runner/store.go stays true. The handler maps the sentinel to 413 in internal/api/runnerapi/errors.go. Each interface has one implementation, in the NEW file pkg/dustpan/sweep_report.go, over the existing blob and data key ports and the sweep store port `unmanaged.ScanStore`.
- Add the NEW method `ScanStore.CompleteScan` and the NEW read `ScanStore.ScanReportKey` to the sweep store port in internal/unmanaged/ports.go. The three stores that the report touches (the blob store, the durable claim and the sweep row) cannot share a transaction, and `ScanStore` has no method that ends the claim. `CompleteScan` ends the durable claim, advances the sweep (uploaded to matching, or fail to failed), stores the key of the staged report and appends the audit entry, all in ONE transaction. The report store writes the encrypted blob first, under a key that the server builds for this attempt, and then calls `CompleteScan`. A failure before the call leaves only an unnamed blob, and a failure of the call rolls back all of it; the report store then deletes its own blob. Add the conformance cases of both methods in internal/unmanaged/unmanagedtest, and add the NEW column `report_key` to the table `unmanaged_scans` in the one schema file of each dialect. The sqlite and postgres adapters implement the method. No migration file is added, because no tag shipped the table.
- The existing poll route under /runner/v1 calls the sweep claimer for a pool with the sweep purpose.
- The sweep claimer moves the sweep with the claim. No code on main applies the events dispatch, claim or release, and the report upload needs a sweep in the status running. Add the NEW small interface `runner.SweepStateStore` and the NEW sentinel `runner.ErrSweepState` in pkg/runner/sweep_claimer.go. Right after the durable claim, the claimer calls the NEW method `SweepStateStore.MarkClaimed`: it applies the event dispatch when the sweep is queued, and then the event claim. The sweep ID is the key of the durable operation. When the call fails, the claimer releases the durable claim and gives no work. The claimer's `Release` calls the NEW method `SweepStateStore.MarkReleased` after the durable release. The one implementation is in the NEW file pkg/dustpan/sweep_state.go over `unmanaged.ScanStore.Scan` and `AdvanceScan`, with the `scanStore` setting of task 2.5. The change adds no method to `unmanaged.ScanStore` for this.
- The pool routes accept the sweep purpose on a namespace pool at create, with the same rules as for the jobs purpose. The change adds no refusal of the sweep purpose and no installation pool. The host identity of a sweep pool stays read-only.
- The purpose of a pool never changes on main, so the change adds no purpose-change guard.
- Wire the sweep claimer into the handler in pkg/dustpan/runners.go, where `Sweeps` is nil today. pkg/dustpan gets the NEW setting `scanStore` and the NEW option `WithScanStore`, which the built-in stores set from `UnmanagedScans()`.
- Add the sweep entries to docs/reference/runner-api.md.

Out of scope:

- Sweep target CRUD, sweep start and match
- Launcher run modes and helper modes
- Job claim path for unit jobs
- Console sweep views
- Converter commands of a sweep target
- A reaper for unnamed sweep report blobs after a crash between the blob write and `CompleteScan`. The blob port rule makes such a blob garbage for a reaper; the reaper belongs to the change that reads and purges sweep reports (issue #4297, "Orphan staged sweep-report blobs have no reaper").

Depends on:

- add-job-session-paths-runner-unmanaged-sweep-store
- `add-job-session-paths-runner-unmanaged-sweep-store` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The runner operator creates a sweep pool through the same routes as a jobs pool, and the pool list shows its purpose.
- A failed report drops its staged blob, so a retry starts clean with no manual cleanup. A crash between the blob write and the completion leaves one unnamed blob of the sweep. No cleanup covers it yet (see Out of scope). The sweep itself stays correct: the claim lapses and the sweep fails.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- runner.SweepKind(pool) already names the durable operation kind of a sweep pool. (`pkg/runner/runner.go:152 (func SweepKind)`)
- A test shows a release goes through the claimer in the runner protocol adapter. (`internal/api/runnerapi/protocol_review_test.go:22 (func TestReleaseGoesThroughTheClaimer)`)
- The error code runner.invalid exists for a bad pool setting. (`internal/errcode/catalog_runner.go:10 (runner.invalid)`)
- The poll already calls a sweep claimer for a pool with the sweep purpose, and the handler config field `Sweeps` is nil in the server wiring. (`internal/api/runnerapi/protocol.go:536 (func claimSweep)`, `pkg/dustpan/runners.go:205`)
- The port runner.SweepClaimer exists, and no adapter implements it. (`pkg/runner/store.go:251 (type SweepClaimer)`)
- The sweep store port has `AdvanceScan` and no method that ends the durable claim or stores a report blob key. The ports of the blob store, the durable store and the sweep store are separate, so no code of one transaction spans them. (`internal/unmanaged/ports.go:336 (type ScanStore)`, `internal/unmanaged/ports.go:368 (AdvanceScan)`)
- The state table of a sweep goes from queued with dispatch to dispatched, and from dispatched or running with claim to running. Release goes from running to dispatched, and uploaded goes only from running to matching. No code outside tests applies the events dispatch, claim or release, so a sweep that is queued or dispatched at the upload gets ErrTransition. (`internal/unmanaged/scan.go:289 (func scanTransitions)`, `internal/unmanaged/scan.go:314 (func NextScan)`)
- The runner protocol adapter calls `SweepClaimer.Release` for a sweep release, and records the claim after the claimer returns it. The adapter needs no change for the state moves. (`internal/api/runnerapi/protocol.go:536 (func Handler.claimSweep)`, `internal/api/runnerapi/protocol.go:746 (func Handler.giveBack)`)
- `AdvanceScan` appends its audit entry only when the new status ends the sweep. (`internal/store/sqlite/unmanaged_scans.go:406 (func UnmanagedScans.AdvanceScan)`)
- Both built-in stores expose the sweep store only as the method `UnmanagedScans()`, and pkg/dustpan has no setting for it. (`internal/store/sqlite/unmanaged_scans.go:28 (func Store.UnmanagedScans)`, `internal/store/postgres/unmanaged_scans.go:29 (func Store.UnmanagedScans)`)
- `AdvanceScan` moves the sweep by `NextScan` (running and uploaded to matching, running and fail to failed) in one transaction with its audit entry, and it does not end the claim. (`internal/store/sqlite/unmanaged_scans.go:406 (func UnmanagedScans.AdvanceScan)`, `internal/unmanaged/scan.go:414 (func CheckAdvance)`)
- The claim end of a durable operation runs inside the transaction of the domain write in the adapter, as `startScan` starts the operation in the transaction of the sweep. (`internal/store/sqlite/durable.go:693 (func endClaim)`)
- Pool.Validate accepts the purposes jobs and sweeps, and CheckUpdate refuses a purpose change with ErrImmutable. The pool routes refuse no purpose. (`pkg/runner/runner.go:358 (func Pool.Validate)`, `pkg/runner/runner.go:385 (func CheckUpdate)`)

## Owner decisions that apply

The project owner decided this on 23 Nov 2026. It binds this change. This section quotes it.

- **Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14)**: "Sweeps must run on the user's own runners as we most recently re-designed it." A sweep runs on a runner pool of the team (a namespace pool) that has the sweep purpose. The host identity of the pool has no write access (ADR 164 decisions 1 and 5, ADR 343 decision 3). The server has no installation sweep pool. The pool API accepts the sweep purpose on a namespace pool with the same rules as for any other purpose. The 13 Nov 2026 answer and its line "a sweep never runs on user runners" no longer hold.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 131: The runner protocol** (not owner-tagged).

- Decisions that apply: 4 (current), 12 (current), 14 (current).
- Changed by: ADR 139 extends decision 6. Chain to the end: ADR 139, ADR 282, ADR 304, ADR 343, ADR 310.
- Current rule: this ADR, with ADR 139.
- Quote: "Each path calls one port. The HTTP adapter of the protocol is a thin layer over the ports of the job model and of this design: - The poll calls `job.Claimer.Claim` for a pool for jobs, and `runner.SweepClaimer.Claim` for a pool for sweeps."
- Quote: "Limits. A JSON body has at most 4 MiB. A log chunk has at most 1 MiB, a part of a plan at most 256 MiB, and a sweep report at most the limit of ADR 164."
- Quote: "For a sweep, they call `SweepClaimer.Renew` and `SweepClaimer.Release`."
- Effect: The poll calls the port runner.SweepClaimer for a sweep pool. The release calls `SweepClaimer.Release`, which applies the release event to the sweep. The report upload keeps the bound of ADR 164 and the error shape of decision 12.
- Conflict: none.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 3 (current), 5 (changed), 11 (current), 12 (current), 13 (current), 15 (changed).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9; ADR 396 changes decision 5. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247, ADR 396.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "The durable operation. The start of a sweep starts one durable operation of the kind `unmanaged.sweep.<pool ID>`, with the start key `<sweep ID>`, in the transaction that stores the sweep."
- Quote: "A sweep pool runs only sweeps. A runner pool gets a **purpose**: `jobs` or `sweeps`. A sweep target must name a pool of the purpose `sweeps`, and a job must name a pool of the purpose `jobs`."
- Quote: "The upload ends the claim (`runner.SweepClaimer.Report`)."
- Quote: "Bounds. A report is at most 1 GiB and 5,000,000 records. A line is at most 64 KiB. A record has at most 50 tags, with a key of at most 128 bytes and a value of at most 256 bytes. A header has at most 1,000 scopes."
- Quote: "A sweep has the statuses `queued`, `dispatched`, `running`, `matching`, `succeeded`, `failed`, `canceled` and `expired`. `unmanaged.NextScan` holds the table, and a test checks each pair of status and event against a table written out apart from the code."
- Effect: The claimer claims the durable sweep operation by purpose. The claim moves the sweep through the table of `unmanaged.NextScan` (queued, dispatched, running), and the change adds no state and no event. The upload ends the claim, and `ScanStore.CompleteScan` does it in the same transaction as the sweep advance. The report keeps all bounds and the header-scope subset rule. The purpose rule is read as changed by ADR 343.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: ADR 396 extends decision 3. Chain to the end: ADR 396.
- Current rule: this ADR, with ADR 396.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Sweeps stay on runners. A pool of the team can have the sweep purpose (Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14)).
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "Paths that stay outside `/api/dustpan/v1`. A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The sweep session routes stay under /runner/v1.
- Conflict: none.

**ADR 396: Sweeps run on the team's own runners, and the CLI does not audit its own usage errors** (owner-tagged (records decisions of the project owner)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The pool management API accepts the sweep purpose on a pool of a namespace. It applies the same rules as for any other purpose."
- Effect: The pool routes accept the sweep purpose on a namespace pool. The change adds no refusal. The server has no installation sweep pool.
- Conflict: none. This ADR replaces the 13 Nov 2026 answer P14.

## Open questions

none

Questions considered and answered:

- Which pools may have the sweep purpose? Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14): a pool of the team. The pool routes accept the sweep purpose on a namespace pool with the same rules as any other purpose. No installation pool exists.
- Does the change need a purpose-change guard? No. ADR 130 decision 3 (owner) says the purpose of a pool never changes, and `runner.CheckUpdate` refuses a change on main.
- How do converters fit the purpose rule? ADR 343 extends ADR 164 decision 9: converters run on a pool with the sweep purpose. Claim is by operation kind, so no new claim path.
- Who moves the sweep to dispatched and to running? The sweep claimer, right after the durable claim, through the NEW port `runner.SweepStateStore`. It is lease then idempotent state update: the lease is the durable claim, and each state event is safe to apply again. AWS (SQS visibility timeout) and Google (Pub/Sub ack deadline) consumers do the same, and neither holds a lease and a status write in one transaction across two stores. The sweep ID is the key of the durable operation. Any failure to move the state gives no work and releases the claim, so the claimer never hands out work for a sweep whose state it could not move.
- What if a person cancels the sweep between the claim and the state move? `AdvanceScan` returns ErrTransition, because a canceled sweep takes no claim event. The claimer answers no work and releases the claim. The cancel already stopped the durable operation in its own transaction (160-165-sweep-targets-lifecycle, Decision 3), so no new behavior exists. A cancel after the state move, while the sweep is running, stays refused as that change says.
- What if the server stops between the durable claim and the state move? The claim lapses and the next claim applies the events again. Each event is applied only from the status that the table allows, and claim from running is a takeover.
- Who drops a staged blob after a server crash? The report store deletes its own blob on every failure that it sees. A crash between the blob write and `CompleteScan` leaves one blob that no sweep row names, under the prefix of the sweep. No existing cleanup covers that prefix: ADR 151 decision 22 covers only the blobs of history items. The blob port rule says that a blob that no committed row names is garbage for a reaper. A reaper for the sweep report prefix is not part of this change, and the change does not claim that one exists. The loss is bounded: at most one blob of at most 1 GiB for each crash in that window, encrypted with the data key of the namespace, and the sweep itself ends failed when its claim lapses.
- How can the report, the claim end and the sweep advance be one transaction? They cannot be one transaction across the blob store. The change does what the blob port rule says: the blob is written first, under a key that is unique for this attempt, and the one transaction of `CompleteScan` names that key in the sweep row, ends the claim and advances the sweep. A reader never sees a sweep in matching without its key. A second attempt cannot overwrite the blob of the first, because each attempt has its own key. This is the transactional outbox practice of AWS and Google: commit the result and its state change together, and never depend on the order across stores.
- Why a column, and not a fixed key? A fixed key lets a second attempt overwrite the blob of the winner before its own transaction fails. A per-attempt key that the committed row names makes the winner the only named blob.
- Does the fail path go through the same method? Yes. `CompleteScan` takes the event fail too, so a bad report ends the claim as failed and moves the sweep to failed in one transaction, with no key.
- What bound applies to the report? ADR 131 decision 14 and ADR 164 decision 11: 1 GiB, 5,000,000 records, 64 KiB a line, 50 tags, 1,000 scopes, and the session caps.
- Which error codes does the report use? ADR 131 decision 12 gives the table: invalid is 400, and a body above a cap is refused with the protocol error shape. A pool refusal uses runner.invalid.
- Who moves a sweep to failed after a bad report or a lost claim? The service of the sweep store does it through its advance call under the claim that the session holds. A call under a lost claim changes nothing and answers 409.
- How is a race of two uploads handled? A unique staged key for each sweep session and a state check in one store transaction let one request win. The other answers 409 for the second upload.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
