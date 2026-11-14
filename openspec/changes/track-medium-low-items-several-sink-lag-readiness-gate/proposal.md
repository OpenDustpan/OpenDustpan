---
version: 1
---

Issue: #718.

## Why

**Persona: operator.** Operator runs several instances on one database and needs restores and readiness to stay safe during locks and sink lag without manual fix.

The user path this change must make work:

1. **Server start.** Start two instances on one PostgreSQL database with DUSTPAN_TEST_POSTGRES_URL
2. **API.** Run restore to swap through ANY /api/server/restores/{} with access token, then watch drop-old-live step retry on held lock
3. **API.** Query readiness while sink lags; read reason for not ready
4. **Docs page.** Follow guide for restore swap waits and readiness gate

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Held lock on old live schema during drop step | Worker keeps restore in `dropping` and defers the step with backoff; read readiness with the probe below to see the reason. | `curl -s http://127.0.0.1:8080/health/ready` |
| Audit sink lag past 300 s bound or chain gate invalid | Server answers 503 with `Retry-After: 5` in API shape and readiness reports not ready with reason `sink-lag` or `chain-gate`; read readiness with the probe below. | `curl -s http://127.0.0.1:8080/health/ready` |

Evidence for the claims above:

- Old live DROP must leave swap into its own step with lock_timeout Proof: Issue #718 body: The `DROP SCHEMA` of the old live data runs inside the swap. It must be its own step with a `lock_timeout`.
- Main sets lock_timeout in restore and revocation paths Proof: pkg/store/postgres/restore_operation.go:971: if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", wait); err != nil {
- Main has readiness and audit chain gate plumbing Proof: pkg/dustpan/health.go:36 (readinessCheck) and pkg/dustpan/maintenance.go:181 (func withAuditChainGate)

## What Changes

- DROP SCHEMA of prior live data runs in its own fenced step with lock_timeout; held lock retries and rerun is a no-op
- Sink lag reads from shared cursor; readiness reflects lag and audit chain gate
- OpenTofu retry stays in finalize step with no other retry path

Out of scope:

- Durable fenced restore core
- Compare-and-swap and shared clock guards
- Orphaned restore_stage_* cleanup

Depends on:

- `track-medium-low-items-several-compare-swap-clock-guards` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operator sees one retry state for held DROP lock, not a failed restore
- Readiness reason names sink lag or chain gate, not a bare 503

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Readiness check type exists (`pkg/dustpan/health.go:36 (func readinessCheck)`)
- Audit stores check exists (`pkg/dustpan/dustpan.go:1717 (func checkAuditStores)`)
- Audit chain interval check exists (`pkg/dustpan/auditchain.go:76 (func checkAuditChainInterval)`)
- Audit chain gate wrapper exists (`pkg/dustpan/maintenance.go:181 (func withAuditChainGate)`)
- Restore steps list exists (`pkg/backup/restore_operation.go:272 (func RestoreSteps)`)
- Lock timeout helpers exist (`pkg/store/postgres/maintenance.go:138 (func lockTimeoutError)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 5 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock. The worker that runs it extends the expiry. Each claim has a fencing token that increases on each takeover."
- Quote: "Control tables stay outside the restore swap. The installation, backups, restores, operations, steps and schedules tables are control tables. A restore does not replace them."
- Effect: Drop of old live schema runs as one fenced retryable step after swap, and control tables stay outside swap.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Scope: one installation, active-active (owner). Milestone 7 makes one installation run active-active: two or more `opendustpan serve` instances behind one load balancer, on one PostgreSQL database and one S3-compatible object store."
- Quote: "A restore is a durable operation (owner). A restore becomes a durable operation of ADR 72. A `worker` process on any instance claims it and runs it, not the process that took the request."
- Effect: Restore stays a durable operation on worker instances, safe with several instances behind one balancer.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current), 8 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Requests fail fast with 503. During the gap, each request that needs the database gets 503 with `Retry-After: 5`, in the shape of its API (W3, ADR 280 decision 4)."
- Quote: "No half writes, and no resend. - A database transaction commits all its rows or none. A connection that breaks before `COMMIT` rolls the transaction back on the server."
- Effect: Readiness gate and audit chain check fail closed during lag and store loss, with fast 503 shape.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1, 2).

- Decisions that apply: 1 (changed), 2 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner). Each dialect keeps one numbered migration file, the baseline `pkg/store/<dialect>/migrations/<NNNN>_baseline.sql`."
- Quote: "No migration or compatibility code for untagged shapes (owner). Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: No numbered migration and no compatibility code; baseline files change in place only.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the DROP SCHEMA of old live data run? ADR 72 decision 2: each step checks fencing token and runs again after takeover; drop step is idempotent and retries on lock timeout. ADR 280 decision 3: worker claims restore.
- How do sink lag and readiness stay correct on several instances? ADR 72 decision 9 and main `checkAuditStores` and `checkAuditChainInterval`: shipper uses shared cursor and chain gate feeds readiness; this change keeps OpenTofu retry in finalize step with no new retry path.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
