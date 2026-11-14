---
version: 1
---

Issue: #1134.

## Problem

As a site administrator who must cut access at any time, a leaked token revoked at 10:00 works again after an 11:00 restore of a 09:00 backup because the maintenance journal resets on each enter.

## Evidence

- The maintenance journal resets on each enter, so a revocation after the backup returns on restore. Proof: Issue 1134: The journal resets on each enter, so a revocation made in normal operation after the backup, or in an earlier stay, comes back on restore.
- Main keeps control tables outside the restore swap in both dialects. Proof: pkg/store/postgres/restore.go:391 (func keepControlTables) and pkg/store/sqlite/restore.go:593 (func copyControlTables)

## Fix boundary

- NEW revocation tombstone control table in the baseline of each dialect, never in a backup, never reset on enter.
- After the swap ReplayRevocations applies each tombstone with at_ms later than the backup taken-at time, with one audit entry per target.
- Cover sessions, access and service tokens, grants, runner join tokens, disabled principals and disabled identity providers.
- Keep tombstones at least as long as the oldest restorable backup through a schedule prune.
- Skip replay on a cross-installation restore with one audit entry.

Out of scope:

- Backup format change
- Console view of tombstones

## Check

- Write the failing restore replay test in NEW file pkg/backup/restore_revocation_test.go. Check: NEW test TestReplayRevocationAfterRestore in NEW file pkg/backup/restore_revocation_test.go fails on main.
- Write the failing control-table coverage test in NEW file pkg/store/storetest/revocation_tombstone_test.go, covering pkg/store/postgres/restore.go:391 (func keepControlTables) and pkg/store/sqlite/restore.go:593 (func copyControlTables). Check: NEW test TestRevocationTombstoneIsControlTable in NEW file pkg/store/storetest/revocation_tombstone_test.go fails on main.
- Add the NEW tombstone table by an in-place edit of the one baseline migration file of each dialect under pkg/store/sqlite/migrations/ and pkg/store/postgres/migrations/. Check: TestRevocationTombstoneIsControlTable in pkg/store/storetest/revocation_tombstone_test.go passes.
- Write a tombstone in each live revocation path in the same transaction, starting from pkg/maintenance/revocation_test.go:23 (TestRevocationValidateKinds). Check: Extended pkg/maintenance/revocation_test.go:23 TestRevocationValidateKinds passes.
- Add the fenced ReplayRevocations step after the swap in pkg/backup/restore_operation.go:676 (func EncodeRestoreSwap). Check: TestReplayRevocationAfterRestore in pkg/backup/restore_revocation_test.go passes.
- Wire replay into the restore route ANY /api/server/restores with its authorization actions. Check: Route ANY /api/server/restores runs the fenced replay step.
- Run an e2e restore with a revoked access token through ANY /api/server/restores with an access token. Check: e2e step calls ANY /api/server/restores with an access token: the revoked token stays refused after restore.
- Write the docs page for restore revocations. Check: The docs page commands run as written.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Restore swap encode exists. (`pkg/backup/restore_operation.go:676 (func EncodeRestoreSwap)`)
- Postgres keeps control tables across the swap. (`pkg/store/postgres/restore.go:391 (func keepControlTables)`)
- SQLite copies control tables into the staged file. (`pkg/store/sqlite/restore.go:593 (func copyControlTables)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 5 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Control tables stay outside the restore swap."
- Quote: "A restore does not replace them."
- Effect: The revocation tombstone table is a NEW control table that the swap keeps on both dialects.
- Conflict: none.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 20 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "Redaction records and tombstones survive the restore swap."
- Effect: Revocation tombstones follow the same survival precedent as redaction records and tombstones.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner)."
- Effect: The table is added by edit of each baseline in place with no numbered migration.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "Each step of the restore checks the fencing token of the claim and can run again after a takeover."
- Effect: Replay is a fenced step of the durable restore with maintenance held until the step ends.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does a backup from another installation replay its revocation tombstones?
   - Options: Replay on cross-installation restore; Skip replay on cross-installation restore.
   - Provisional decision (owner review 14 Nov): Skip replay on cross-installation restore.
   - Reason: A foreign backup shares no tombstone clock or installation ID, so replay risks false denies. Skip until the PM decides. Benchmark: AWS IAM revocation stays in force when a resource snapshot is restored.
   - Persona impact: No persona loses access without a record. Escape hatch: The administrator re-issues a credential through the normal grant path.

Questions considered and answered:

- Where do tombstones live so a restore keeps them? ADR 72 decision 5: control tables stay outside the swap.
- Which precedent covers durable records across a restore? ADR 151 decision 20: redaction records and tombstones survive the swap.
- How is the table added before the first tag? ADR 322 decision 1: edit each baseline in place.
- How does replay survive a crash? ADR 280 decision 3: fenced steps run again after takeover.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
