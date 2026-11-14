---
version: 1
---

Issue: #1965, #2199.

## Problem

An operator restores and imports portable backups. Each stage reruns every migration from zero, so restores pay the full migration chain each time, and each restore leaves staging files behind.

## Evidence

- SQLite portable staging is built by buildPortableStaging. Proof: pkg/store/sqlite/backup_stage_portable.go:137 (func Store.buildPortableStaging)
- The PostgreSQL portable staging path exists with StagePortable, portableStage, and loadPortableStaging. Proof: pkg/store/postgres/backup_stage_portable.go:21 (func Store.StagePortable)
- Each store records migration versions and hashes through recordedMigrations. Proof: pkg/store/sqlite/sqlite.go:1752 (func Store.recordedMigrations)
- The backup and restore guide exists. Proof: docs/guides/backup-restore-upgrade.md exists

## Fix boundary

- Build the migrated empty SQLite schema once per process as a read-only template and copy it for each portable stage after a version and hash check on the copied bytes.
- Refuse a changed template with backup.corrupt and verify the PostgreSQL StagePortable path against recordedMigrations.
- Remove per-restore staging lock files and empty restore-tmp trees of the ending restore only without touching non-empty or live trees.
- Report a copied backup directory as found by backup check from its manifest.

Out of scope:

- Unrelated backup, restore, and store work outside the two cited issues.

## Check

- Add failing tests for StagePortable template reuse on both dialects, buildPortableStaging changed-template refusal, and restore cleanup in pkg/store/storetest/portable.go. Check: New cases in pkg/store/storetest/portable.go fail on main and pass after the fix.
- Change SQLite portable staging in pkg/store/sqlite/backup_stage_portable.go to build the template once per process in a process-private directory and copy it per stage. Check: Template reuse case passes with a pre-copy version and hash check on the copied bytes in pkg/store/sqlite/backup_stage_portable.go.
- Apply verification against recordedMigrations in pkg/store/postgres/backup_stage_portable.go with no file copy on the PostgreSQL staging path. Check: PostgreSQL portable mismatch case returns backup.corrupt in pkg/store/postgres/backup_stage_portable.go.
- Remove the staging lock file and empty restore-tmp directories of the ending restore only in pkg/backup with blob cleanup in pkg/blob/fs, without touching non-empty or live trees. Check: Restore end leaves no lock file of that restore id and no empty restore-tmp tree in pkg/backup and pkg/blob/fs.
- Read backup check from the backup directory manifest in pkg/backup so a copied backup is found while a missing backup still answers 404. Check: Copied-backup check case passes for a directory copied without a registry row.
- Wire the change through ANY /api/server/restores and ANY /api/server/backups/{}/check with the operator gate and an access token, and run an e2e step against the real directory. Check: e2e step calls ANY /api/server/restores with the operator gate and an access token, then ANY /api/server/backups/{}/check, and leaves no staging lock file and no empty restore-tmp tree. Confirmed by: CI (make e2e).
- Update docs/guides/backup-restore-upgrade.md for template reuse, cleanup, and copied-backup check. Check: docs page updated in docs/guides/backup-restore-upgrade.md. A person's step.
- Measure the p95 SQLite portable stage build time and record the result. Check: Staging build p95 measured under 2 seconds per SQLite portable stage. Confirmed by: the driver's gates.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- SQLite builds each portable staging file with buildPortableStaging. (`pkg/store/sqlite/backup_stage_portable.go:137 (func Store.buildPortableStaging)`)
- PostgreSQL stages portable data with StagePortable, portableStage, and loadPortableStaging. (`pkg/store/postgres/backup_stage_portable.go:21 (func Store.StagePortable)`)
- Each store records migration versions and hashes through recordedMigrations. (`pkg/store/sqlite/sqlite.go:1752 (func Store.recordedMigrations)`)
- Portable stage behavior is covered by the shared portable conformance case. (`pkg/store/storetest/portable.go:233 (func testPortableStages)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the change cache a migrated empty-schema template instead of running migrations per stage? Yes. One migrated empty-schema template is built once per process and copied for each SQLite stage; the copied bytes pass the recorded-migration version and hash check before use.
- Does any ADR in the pack govern backup staging or restore cleanup? The pack ADR list is empty so there are no decision lines to quote and no conflict. The change keeps the existing version and hash gate and the existing backup.corrupt refusal.
- What integrity and cleanup work do the two issues require? Hash is checked on the copied bytes before use, the template stays read-only and process-private, a mismatch is refused with backup.corrupt, the PostgreSQL path checks recordedMigrations with no file copy, and only the ending restore id is cleaned.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
