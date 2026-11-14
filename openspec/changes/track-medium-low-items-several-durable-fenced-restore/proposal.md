---
version: 2
---

Issue: #670.

## Why

**Persona: operator.** An operator restores an installation while two server processes run. Today a kill of the holder leaves staging behind and the next start can fail or two workers can write at once. The change makes the restore survive a kill and run again on another worker with no operator action.

The user path this change must make work:

1. **API.** Operator starts restore through installation API.
2. **API.** Worker claims and runs copy, swap, drop-old, cleanupStage; operator polls restore state.
3. **Docs page.** Operator reads several-instances guide for restore behavior.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Operator starts a restore with no worker for 2 lease periods and gets 503 naming the worker. | 503 with Retry-After and the NEW error code `restore.worker-absent`, in the shape of the API. | `GET /api/dustpan/v1/worker` |
| Operator starts a second restore while one runs and gets 409 naming the running restore. | 409 with the NEW error code `restore.running` and the running restore ID. | `GET ANY /api/server/restores/{}` |
| Operator without the restore right starts a restore and gets 403 naming the right. | 403 with the NEW error code `restore.forbidden` naming the missing restore right. | `GET ANY /api/server/restores/{}` |

Evidence for the claims above:

- Main has fenced restore helper and worker restore cases. Proof: pkg/store/postgres/restore_operation.go:100 (func fencedRestore); pkg/store/storetest/restore_worker_cases.go:534 (func workerRig.failoverVersion)
- Main has restore error codes and restore tests. Proof: restore.blobs-missing, restore.live-blobs-damaged, restore.live-blobs-missing, restore.swapped, restore.ended; TestRestoreSwappedManifestIsRefusedAtEachStep; TestShutdownDuringWriteStepLeavesTheBackupForAnotherWorker
- Issue 670 tracks medium and low multi-instance items. Proof: #670, #717, #718 with scope M8 to M24 except M22
- Main splits api and worker and checks audit chain once with a durable store. Proof: pkg/dustpan/components_split_test.go:32 (TestSplitTopologyWorkerFinishesWhatAPIStarted)

## What Changes

- Durable kind restore with fenced copy, swap, drop-old, and cleanupStage steps
- Staging name stored in restore row; non-failing start with failover version bump
- Drop-old with 5 s lock timeout and bounded retry

Out of scope:

- Audit compare-and-swap and audit guards
- Shared database clock for expiry
- Sink lag and readiness gate
- Helm chart replicas and high availability e2e suite

## Usability

Where the path still costs the person time or surprises them:

- Operator sees one restore ID and one state from any instance; stale staging never blocks a new start of the same restore.
- Drop-old wait surfaces as running with lock-wait on the row, not as failure; one poll command shows progress.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has fenced restore helper in postgres store. (`pkg/store/postgres/restore_operation.go:100 (func fencedRestore)`)
- Main has worker restore cases with failover version helper. (`pkg/store/storetest/restore_worker_cases.go:534 (func workerRig.failoverVersion)`)
- Main has lock timeout helpers. (`pkg/store/postgres/maintenance_revocations.go:252 (func isLockTimeout)`)
- Main refuses a swapped manifest at each restore step. (`pkg/backup/restore_manifest_swap_test.go:17 (func TestRestoreSwappedManifestIsRefusedAtEachStep)`)
- Main leaves a backup for another worker after shutdown during a write step. (`pkg/backup/service_durable_test.go:480 (func TestShutdownDuringWriteStepLeavesTheBackupForAnotherWorker)`)
- Main splits api and worker in tests. (`pkg/dustpan/components_split_test.go:32 (func TestSplitTopologyWorkerFinishesWhatAPIStarted)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 5 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock. The worker that runs it extends the expiry. Each claim has a fencing token that increases on each takeover."
- Quote: "Control tables stay outside the restore swap. The installation, backups, restores, operations, steps and schedules tables are control tables. A restore does not replace them."
- Effect: Restore follows the lease with fencing token and keeps control tables outside the swap.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "A restore is a durable operation (owner). A restore becomes a durable operation of ADR 72. A `worker` process on any instance claims it and runs it, not the process that took the request."
- Effect: Restore runs as a durable operation on any worker with fenced steps that run again after takeover.
- Conflict: none.

**ADR 249: Components of the server process, and a start flag that selects them** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Two components for now: `api` and `worker`. - `api` runs each HTTP route: the Pulumi Cloud-compatible API, the TFE-compatible API, the tool-neutral APIs, the installation API and the console files. It starts a durable operation (it inserts the row), but it never claims one."
- Quote: "No worker. When no `worker` claimed work for 2 lease periods, an `api` process refuses each request that starts an operation, for example a backup or a restore, with 503 and the command that helps."
- Effect: The api process starts the restore row; only a worker process claims and runs its steps, and api refuses a start with 503 only after no worker claimed work for 2 lease periods.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner). Each dialect keeps one numbered migration file, the baseline `pkg/store/<dialect>/migrations/<NNNN>_baseline.sql`."
- Effect: Schema change edits the baseline of each dialect in place with no numbered migration.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- How does a second worker avoid running restore steps twice? ADR 72 decision 2 answers it: each step write checks the fencing token in its own transaction and another instance claims only after expiry.
- Where does the staging name live so it survives the swap? ADR 72 decision 5 answers it: restores table is a control table and a restore does not replace it, so the row with the staging name survives the swap.
- Which component runs the restore copy, swap, and cleanup? ADR 280 decision 3 as owner decision answers it: restore is a durable operation run by a worker, not the request process.
- How is the staging-name column added before the first tag? ADR 322 decision 1 answers it: edit each baseline in place, add no numbered migration.
- When does restore start answer worker-absent 503? ADR 249 decision 6 answers it: 503 only after no worker claimed work for 2 lease periods; otherwise the start is accepted and waits for a worker.
- What happens to the inventory projection at swap? ADR 166 decision 9 answers the rebuild part, and this part only orders it: the swap transaction starts one projection rebuild operation per namespace from the same transaction.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
