---
version: 1
---

Issue: #2306.

## Problem

Operator restores a backup to recover an installation. Today the restore cannot see that the backup holds another version of the edited baseline, so it can load table files into the wrong schema without a clear refusal.

## Evidence

- Backup manifest records only schema version today. Proof: pkg/backup/service.go:67: SchemaVersion        int            `json:"schemaVersion"`
- Restore compares schema version and dialect only. Proof: pkg/backup/restore_service.go:1214: manifest.SchemaVersion != pinned.schema || manifest.Dialect != pinned.dialect ||
- Store open path already refuses on SHA-256 difference. Proof: Issue body: The store open path is not affected: it refuses a database whose recorded SHA-256 of the baseline differs from the binary's.

## Fix boundary

- Backup manifest records the SHA-256 of the applied baseline.
- Restore refuses a SHA-256 mismatch before it changes anything with a clear error.

Out of scope:

- Support for restores across untagged baselines
- Change to the store open path
- Per-migration hash list after the first tag
- Manifest content integrity beyond the existing backup.corrupt check

## Check

- Add failing manifest test for the recorded baseline SHA-256 in pkg/backup/restore_check_test.go. Check: NEW test TestManifestHoldsBaselineHash fails on main, passes after fix.
- Add failing restore test for mismatched and missing SHA-256 with no writes in pkg/backup/restore_check_test.go. Check: NEW test TestRestoreRefusesBaselineMismatch fails on main, passes after fix.
- Record the baseline SHA-256 in the backup manifest in pkg/backup/service.go. Check: Manifest holds 64 lowercase hex hash.
- Compare SHA-256 at restore enter and refuse with the NEW error code in pkg/backup/restore_service.go. Check: Mismatch, missing, and malformed hash refuse with NEW code restore.baseline-mismatch.
- Write an audit entry for each refused restore through the checked port in pkg/backup/restore_service.go after write-audit-entry-when-person lands. Check: Audit entry exists for each refusal through the checked append port from write-audit-entry-when-person.
- Wire route ANY /api/server/restores to the check with its installation restore grant, called with an access token. Check: An access-token call without the grant is denied. Confirmed by: CI (make e2e restore step).
- Add the docs page NEW docs/guides/backup-restore-baseline.md with the code restore.baseline-mismatch and the remedy to make a fresh backup. Check: A person's step.
- Run the usability check and make check with zero findings. Check: Confirmed by: CI (make check).

Depends on:

- write-audit-entry-when-person
- `write-audit-entry-when-person` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- SQLite store can report its schema version. (`pkg/store/sqlite/backup.go:1066 (func schemaVersion)`)
- Postgres store can report its schema version. (`pkg/store/postgres/backup.go:1621 (func Store.schemaVersion)`)
- Backup has a restore schema-before-baseline check. (`pkg/backup/restore_check_test.go:399 (func TestCheckRestoreSchemaBeforeBaseline)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 73: Until 1.0, a change can break everything except the tool-compatible APIs** (not owner-tagged).

- Decisions that apply: 6 (changed).
- Changed by: ADR 322 changes decision 6; ADR 322 extends decision 4. Chain to the end: ADR 322, ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 322.
- Quote: "A database or a backup that an untagged build wrote has no support."
- Effect: Change keeps the no-support rule for untagged backups and adds a clear refusal before any restore write.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner)."
- Effect: Change follows the one-baseline rule. Until the first tag one baseline hash covers the whole schema. Decision 3 covers the store open path only and this change does not reuse it.
- Conflict: none.

**ADR 362: Owner decisions of 8 November 2026: stable IDs, store order, MySQL, baseline hashes and portable backups** (owner-tagged: decision 4).

- Decisions that apply: 4 (current).
- Changed by: ADR 363 extends decision 2. Chain to the end: ADR 363.
- Current rule: this ADR, with ADR 363.
- Quote: "The `.sha256` files of the schema baselines (ADR 322) are absent until the first tag. The release generates them."
- Effect: Change does not read .sha256 files. It hashes the embedded baseline SQL at runtime because the files stay absent until the tag.
- Conflict: none.

**ADR 250: Defense in depth for security code** (not owner-tagged).

- Decisions that apply: 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed. A layer without its own test counts as missing."
- Quote: "Also configuration files and environment variables, backups that you restore, database rows written by another version, and file names inside archives."
- Effect: Restore treats the backup as untrusted input and tests each guard layer with hostile input sent direct to that layer.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must an untagged backup restore across baseline edits? No. ADR 73 decision 6 says a database or backup that an untagged build wrote has no support. This change adds a clear refusal, not a supported cross-baseline restore.
- Where does the expected SHA-256 come from and does it match the open path? The expected hash is the SHA-256 of the raw embedded baseline SQL bytes of each dialect, computed by the NEW function BaselineSha256. ADR 362 decision 4 keeps the .sha256 files absent until the tag, so no file is read.
- Why does this change record one hash instead of one hash per migration? Until the first tag there is one baseline per dialect, so one hash covers each applied migration. A per-migration list after the first tag belongs to the tag change and stays out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
