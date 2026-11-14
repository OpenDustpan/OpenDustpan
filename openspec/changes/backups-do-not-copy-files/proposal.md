---
version: 2
---

Issue: #1287.

## Why

**Persona: operator.** Operator backs up one namespace and restores it on another store. The restore gives releases that name missing files, so installs from the registry fail. The change copies the release files with the backup and checks them on restore.

The user path this change must make work:

1. **API.** Publish a provider or module release in a namespace, then create a namespace backup through the API or CLI.
2. **CLI.** Move the backup to a second installation and restore it, then fetch the release through the registry.
3. **Docs page.** Read the backup guide for limits and the restore error for the one command that helps.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Restore fails when a registry file named by the backup is missing in the backup. | Restore stops before any registry file becomes visible and names the missing SHA-256 key. Run the backup again from the source installation. | `opendustpan backup create --namespace <namespace>` |
| Restore fails when a registry file does not match its SHA-256 content key. | Restore stops before any registry file becomes visible and names the SHA-256 key with the bad hash. Run the backup again from the source installation. | `opendustpan backup create --namespace <namespace>` |
| Backup fails when a registry release file cannot be read from the blob store. | Backup stops with unreadable registry file and names the release and version. Fix blob store access and run the backup again. | `opendustpan backup create --namespace <namespace>` |

Evidence for the claims above:

- Backups include the registry_releases and registry_keys tables. Proof: pkg/backup/portable_registry.go:11:			Table:   "registry_releases",
- The backup blob reference path uses MD5 references built for state versions. Proof: pkg/store/internal/blobrefs/blobrefs.go:84:	refs       []backup.BlobRef
- Registry rows are stored per dialect with dedicated queries. Proof: pkg/store/sqlite/internal/db/queries.sql.go:11477:INSERT INTO registry_releases (

## What Changes

- Namespace backup copies each registry release file under namespaces/<namespace ID>/registry/sha256/ by SHA-256 content key.
- Namespace restore writes each copied file and refuses when a named file is missing.
- Backup guide no longer says that registry files are skipped.

Out of scope:

- Curated upstreams rules.
- Background sweep of staged and unreferenced registry files.
- h1 hash and registry slug changes.
- Upgrade pull requests.

## Usability

Where the path still costs the person time or surprises them:

- Operator learns from the guide that registry files are now in the backup, with no extra flag.
- Restore error names the missing SHA-256 key and the one backup command that helps.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Namespace backup code declares registry_releases and registry_keys tables for portable backup. (`pkg/backup/portable_registry.go:11`)
- Backup table list includes registry_releases and registry_keys. (`pkg/backup/backup.go:193`)
- Blob reference collector holds backup.BlobRef values for backup blobs. (`pkg/store/internal/blobrefs/blobrefs.go:84`)
- Backup error catalog defines corrupt backup errors. (`internal/errcode/catalog_backup.go:5`)
- Registry error catalog defines registry refusal errors. (`internal/errcode/catalog_registry.go:7`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Shall a namespace restore fail when a registry release file named by the backup is missing? Decided: the restore fails. A restored registry that names missing files breaks installs without a clear cause, so fail closed.
- How shall a restore handle a backup made before file copy? Decided: the restore warns and restores rows without files, and marks each restored release unavailable so a fetch fails with a clear error. Old backups hold registry rows with no file manifest section. A new backup with zero releases holds an empty file manifest section, which tells the two cases apart.
- Does any ADR govern registry backups? No. The pack names no ADR and the keyword search shows no ADR text on registry backups.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
