---
version: 1
---

Issue: #1984, #2342.

## Problem

Operator restores an installation from a backup and reads the restore document. A failed restore returns text with no error code. An unknown backup ID fails late as corrupt, which misleads the operator.

## Evidence

- Restore service sanitizes restore errors. Proof: pkg/backup/restore_service.go:840 (func sanitizeRestoreError)
- Server restore handler maps errors to codes. Proof: internal/api/server/restores.go:447 (func errorCodeOf)
- Catalog already holds restore.blobs-missing. Proof: internal/errcode/catalog_restore.go:7

## Fix boundary

- Return optional errorCode on the restore document for coded failures.
- Store the catalog code beside the failure text in the restore row.
- Refuse an unknown backup ID with the NEW error code `backup.not-found` instead of corrupt text.
- Update the API description, the gate list and the clients for the new member.

Out of scope:

- New restore failure kinds beyond unknown backup and coded failures.
- Backup listing and backup check changes.

## Check

- Add a failing test for the coded blobs-missing failure through sanitizeRestoreError in pkg/backup/restore_service.go. Check: TestRestoreFailedReturnsCatalogCode in pkg/backup fails on main; calls `ANY /api/server/restores/{}` after the restore grant with access token.
- Add a failing test for an unknown backup ID refused at start with the NEW error code `backup.not-found`. Check: TestRestoreUnknownBackupRefused in internal/api/server fails on main; calls `ANY /api/server/restores` after the restore grant with access token.
- Add the NEW catalog entry `backup.not-found` in internal/errcode/catalog_backup.go. Check: Catalog unit test for `backup.not-found` passes in internal/errcode.
- Store the catalog code beside the failure text in the restore row. Edit the baseline migration in pkg/store/sqlite/migrations/ and pkg/store/postgres/migrations/ in place, with no numbered migration. Check: Restore row round-trip test passes in each dialect.
- Add the start-time backup existence check after the grant check in internal/api/server/restores.go. Check: Start handler test passes for unknown and malformed backup IDs through `ANY /api/server/restores` after the restore grant with access token.
- Return the errorCode member on GET by editing internal/api/server/restores.go:447 (func errorCodeOf) and internal/api/apidesc/openapi.json:53, update the gate list, and regenerate the clients. Confirmed by: CI (make clients-check). Check: Gate list test passes for the new member on `ANY /api/server/restores/{}` after the restore grant with access token; make clients-check is clean.
- Wire `ANY /api/server/restores` with its authorization actions, an e2e step against the real CLI, the docs page for errorCode and the unknown backup behavior, and the usability check of the Operator path. Confirmed by: CI (make check). Check: E2E step calls `ANY /api/server/restores` and `ANY /api/server/restores/{}` after the restore grant with access token; docs page updated; `make check` shows zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Restore service sanitizes restore errors. (`pkg/backup/restore_service.go:840 (func sanitizeRestoreError)`)
- Server restore handler maps errors to codes. (`internal/api/server/restores.go:447 (func errorCodeOf)`)
- Catalog holds restore.blobs-missing. (`internal/errcode/catalog_restore.go:7`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR govern restore documents or error codes? No ADR in the pack governs restore documents or error codes.
- Does an unknown backup ID fail at start or as a failed restore? The start refuses it with 404 and the NEW error code `backup.not-found`; the issue allows 404 at the start.
- In what order do the grant check and the backup existence check run? The grant check runs first, so a caller without the grant cannot probe backup IDs.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
