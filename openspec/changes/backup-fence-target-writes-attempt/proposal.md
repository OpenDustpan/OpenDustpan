---
version: 2
---

Issue: #1201.

## Why

**Persona: operator.** Operator runs two or more instances of one installation on one database and one S3-compatible bucket. When one instance dies during a backup, another instance finishes the backup while the dead instance resumes with a delete in flight. The operator needs the finished backup to stay complete and the finish step to refuse a truncated target.

The user path this change must make work:

1. **API.** Operator starts a backup through `ANY /api/server/backups` and it runs on a worker instance.
2. **CLI.** Operator checks it with `ANY /api/server/backups/{}/check` with an access token.
3. **Docs page.** Operator reads the several-instances docs page for target layout and recovery figures.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Finish check fails with `backup.corrupt` when a file is missing or truncated. | Finish failed because the target is missing or truncated. Run the check route to see which file failed, then run the backup again. | `GET /api/server/backups/{id}/check` |
| Finish refuses with fenced-token mismatch when the manifest attempt differs from the claim. | Finish refused because the manifest attempt differs from the live claim. Run the backup again to make a new attempt. | `GET /api/server/backups/{id}/check` |
| Cleanup stops without deletes when the claim fencing token moved. | Cleanup skipped because the claim moved. No action is needed; the live holder or retention expiry deletes the prefix. | `GET /api/server/backups/{id}/check` |

Evidence for the claims above:

- Backup write step clears the target prefix and finish lists files against the manifest before the fenced finish write. Proof: Issue #1201 body describes the clear of `backups/<id>/` and the list-then-commit window.
- A clear-partial guard already refuses keys of another backup. Proof: Test `TestClearPartialRefusesAKeyOfAnotherBackup` at `pkg/backup/service_durable_guard_test.go:73`.
- Manifest checksum check refuses a manifest with no checksum. Proof: Test `TestCheckRefusesAManifestWithNoChecksum` at `pkg/backup/check_checksum_test.go:78`.

## What Changes

- Target writes move to `backups/<id>/<attempt>/` with the manifest naming the attempt, so a stale attempt touches only its own prefix.
- Finish step reuses Check with sizes and blob copies before the fenced finish write.
- StepFinish comment states which deletes are covered.

Out of scope:

- Replication between installations, promotion and forwarding.
- Shared cache and lease period flag.
- Backup target types beyond S3-compatible store.
- Read support for the old unscoped target layout.

## Usability

Where the path still costs the person time or surprises them:

- Operator sees one extra path segment in the target; `opendustpan backup check` output stays the same.
- Stale prefixes cost storage until the live holder or retention expiry deletes them; the docs page names the cleanup step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Clear-partial guard refuses a key of another backup. (`pkg/backup/service_durable_guard_test.go:73 (func TestClearPartialRefusesAKeyOfAnotherBackup)`)
- Manifest checksum check refuses a manifest with no checksum. (`pkg/backup/check_checksum_test.go:78 (func TestCheckRefusesAManifestWithNoChecksum)`)
- Copy-checked key stops when the claim is stale. (`pkg/backup/restore_live_blob_internal_test.go:368 (func TestCopyCheckedKeyStopsWhenTheClaimIsStale)`)
- Backup check CLI wrapper exists. (`internal/cli/client/backup_check.go:67 (func BackupCheck)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "Each claim has a fencing token that increases on each takeover."
- Quote: "Every step write, renewal and completion checks the token in its own transaction."
- Effect: Finish write and cleanup delete check the fencing token of the claim in the same transaction.
- Conflict: none.

**ADR 280: Milestone 7: high availability on one database** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 7 (current).
- Changed by: ADR 281 extends decision 5; ADR 286 changes; ADR 298 extends decision 4; ADR 302 extends. Chain to the end: ADR 281, ADR 286, ADR 298, ADR 302, ADR 340, ADR 337, ADR 368, ADR 369, ADR 370.
- Current rule: this ADR, with ADR 281, 286, 298, 302.
- Quote: "A `worker` process on any instance claims it and runs it, not the process that took the request."
- Quote: "A deleter deletes a blob that a committed row named only when a read of the backups registry, which starts after the commit that deleted or replaced that row, finds no running backup."
- Effect: Backup target writes become fenced durable steps under attempt prefixes and keep the no-lock backup rule.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "Until the first tag, no pull request writes migration, upgrade, compatibility, alias or data-conversion code for a schema or an API shape that no tag shipped."
- Effect: No read path for the old unscoped layout; the attempt column is a baseline edit.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a fenced finish write alone stop a stale holder with a blob delete in flight? No. ADR 72 decision 2 checks the fencing token in its own transaction; attempt-scoped keys add isolation so a stale attempt touches only its own prefix.
- Should the backup take a lock on the target while it writes? No. ADR 280 decision 7 says the backup takes no lock and never waits; attempt-scoped writes keep that rule.
- Should the server keep reading backups written to the old unscoped layout? No. ADR 322 decision 2 forbids compat code for untagged shapes, so old unscoped targets get no read support.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
