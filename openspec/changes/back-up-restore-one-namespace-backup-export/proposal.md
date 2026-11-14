---
version: 1
---

## Problem

A namespace administrator keeps one namespace safe and moves its copy. They need a namespace export they can download, audit, and hold within a clear limit, without a full installation backup.

## Evidence

- Main already exports portable format at the store layer. Proof: pkg/store/postgres/backup_portable.go:19 (func Store.ExportPortable)
- Main already has a portable export helper in the backup service. Proof: pkg/backup/service.go:1075 (func Service.exportPortable)
- Main already wires backup routes in the installation API handler. Proof: internal/api/server/routes.go:169 (func Handler.backupRoutes)
- Main already has namespace-scoped helpers and routes under /api/dustpan/v1/namespaces. Proof: pkg/gate/scope.go:97 (func NamespaceScope)

## Fix boundary

- Add NEW namespace backup export route that streams a portable-format archive for one namespace.
- Add NEW namespace backup export action namespace-backup:export on the stable namespace ID.
- Write one audit entry for each namespace export allow and deny.
- Enforce a 512 MiB pre-count export limit with the NEW error code namespace-backup.too-large.

Out of scope:

- Namespace restore.
- Writer fence for restore.
- Pre-restore safety backup.

## Check

- Write NEW `pkg/backup/namespace_export_test.go` with service and store tests for namespace-scoped portable export with the stable namespace ID. Check: `NEW pkg/backup/namespace_export_test.go` TestNamespaceBackupExportWritesPortableArchive fails on main, passes after the change.
- Write NEW `internal/api/server/namespace_backup_test.go` with route tests for the NEW export route, the NEW actions, and the audit entry. Check: `NEW internal/api/server/namespace_backup_test.go` TestNamespaceBackupExportRouteAuth fails on main, passes after the change.
- BLOCKED by NEW route GET /api/dustpan/v1/namespaces/{namespace}/backup/export: wire the NEW export route with its authorization actions and audit entries. Check: BLOCKED by NEW route GET /api/dustpan/v1/namespaces/{namespace}/backup/export; no existing route serves a namespace export.
- Write NEW `docs/reference/namespace-backup.md` for the namespace export path, grants, errors, and the 512 MiB limit. Check: `NEW docs/reference/namespace-backup.md` each command runs as written against a dev server.
- Run usability check from the docs page alone and `make check` with zero findings. Check: `make check` with zero findings after the docs-page usability check.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Store exports portable format. (`pkg/store/postgres/backup_portable.go:19 (func Store.ExportPortable)`)
- Backup service exports portable format. (`pkg/backup/service.go:1075 (func Service.exportPortable)`)
- Server handler wires backup routes. (`internal/api/server/routes.go:169 (func Handler.backupRoutes)`)
- Gate builds a namespace scope. (`pkg/gate/scope.go:97 (func NamespaceScope)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which role holds the NEW namespace-backup:export action by default?
   - Options: Grant namespace-backup:export to the namespace administrator role by default; Grant it to no role by default and require an explicit grant.
   - Provisional decision (owner review 14 Nov): Grant namespace-backup:export to the namespace administrator role by default.
   - Reason: The pack names no default holders, so the default grant needs a PM choice before build. Benchmark: AWS practice for scoped backup export: explicit allow action on the scope, deny by default, audit each allow and deny, and enforce a size cap before streaming.
   - Persona impact: Namespace administrators can export without extra steps under option one; option two adds one grant step but limits exposure. Escape hatch: A site administrator can grant the export action per namespace or refuse it; no bypass path exists.

Questions considered and answered:

- Does this change include namespace restore? No. The roadmap split assigns restore, writer fence, and pre-restore backup to part 2. This part covers only export.
- Does any ADR already decide the namespace export format? No. The pack lists no ADR text, so no ADR decision constrains the format. The glossary terms Namespace backup and Portable format apply: export writes the portable format at scope namespace.
- Does the export reveal secret values in plain text? The archive keeps stored bytes as stored, including sealed secret values. Export never decrypts or reveals plain text. No new key handling; restore and re-key stay in part 2.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
