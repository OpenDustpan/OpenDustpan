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
| Restore fails when a registry file named by the backup is missing in the backup. | Restore stops before writing any registry file and names the missing SHA-256 key. Run the backup again from the source installation. | `opendustpan backup create --namespace <namespace>` |
| Restore fails when a registry file does not match its SHA-256 content key. | Restore stops before writing any registry file and names the SHA-256 key with the bad hash. Run the backup again from the source installation. | `opendustpan backup create --namespace <namespace>` |
| Backup fails when a registry release file cannot be read from the blob store. | Backup stops with unreadable registry file and names the release and version. Fix blob store access and run the backup again. | `opendustpan backup create --namespace <namespace>` |

Evidence for the claims above:

- Backups include the registry_releases and registry_keys tables. Proof: pkg/backup/portable_registry.go: Table: registry_releases and Table: registry_keys; pkg/backup/backup.go: registry_releases and registry_keys
- The backup blob reference path uses MD5 references built for state versions. Proof: pkg/store/internal/blobrefs/blobrefs.go: refs []backup.BlobRef; pkg/store/storetest/portable.go: want := []backup.BlobRef and pkg/store/storetest/portable_restore.go: wantBlobs := []backup.BlobRef
- Registry rows are stored per dialect with dedicated queries. Proof: pkg/store/sqlite/internal/db/queries.sql.go: FROM registry_releases AS r and INSERT INTO registry_releases; pkg/store/postgres/internal/db/queries.sql.go: SELECT COUNT(*) FROM registry_keys and DELETE FROM registry_keys

## What Changes

- Namespace backup copies each registry release file under namespaces/<namespace ID>/registry/sha256/ by SHA-256 content key.
- Namespace restore writes each copied file and refuses when a named file is missing.
- Backup guide no longer says that registry files are skipped.

Out of scope:

- Curated upstreams rules.
- Background sweep of staged and unreferenced registry files.
- h1 hash and registry slug changes.
- Upgrade pull requests.

Depends on:

- artifact-registry

## Usability

Where the path still costs the person time or surprises them:

- Operator learns from the guide that registry files are now in the backup, with no extra flag.
- Restore error names the missing SHA-256 key and the one backup command that helps.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (missing scenario): The proposal error table lists a backup failure when a registry release file cannot be read from the blob store, naming the release and version. The spec in specs/backup/spec.md has no scenario for this error, so the backup behavior is not defined. Resolution: answered: Added requirement backup-stop-unreadable-file with scenario Unreadable file stops backup for unreadable blob store file.
- R1.2 (missing scenario): The design risk says a restore of an old backup without file entries still leaves missing files. The spec only checks files that the backup names, so a restore of an old backup with registry rows and no file entries succeeds silently. No requirement says whether such a restore must fail, warn or proceed. Resolution: open question: Added open question on backups without file entries; restore warns and proceeds until the owner decides.
- R1.3 (open question): The restore path is namespaces/<namespace ID>/registry/sha256/, but the spec does not say whether the source or the target namespace ID is used when a backup is restored on another installation. The IDs may differ, so the file location is undefined. Resolution: answered: Copy and restore requirements now name the target namespace ID for the sha256 path.
- R1.4 (failure mode): The restore writes files and then checks presence, or the reverse order is not stated. If the check fails after some files are written, the target store keeps a partial copy and the restore is not atomic. The spec and the design do not say how a failed restore is rolled back or retried. Resolution: answered: Added requirement restore-check-before-write with scenario that no file is written when the manifest is incomplete.
- R1.5 (failure mode): No requirement verifies that a copied or restored file matches its SHA-256 content key. A hostile or corrupted backup, or a corrupted blob store, can supply bytes under a valid key name, and the restore accepts them. Integrity of the content-addressed files is not checked. Resolution: answered: Added requirement restore-verify-hash with scenario for corrupt bytes under a valid key name.
- R1.6 (open question): The spec does not say whether the backup copies only files named by published releases, or also staged uploads and unreferenced files. The out-of-scope list excludes only the background sweep, so the set of files to copy is not defined. Resolution: answered: Added requirement copy-published-only that excludes staged uploads and unreferenced files.
- R1.7 (failure mode): A release can be published or removed while the backup reads registry rows and then copies files. The snapshot consistency of rows and files is not defined, so the backup can name a file that is not copied or copy a file that a release no longer names. Resolution: answered: Design decision snapshot scope plus scenario for stable row and file set; backup reads rows and file list from one snapshot.
- R1.8 (open question): The design says the constructor rejects an empty namespace ID and an empty hash, but no spec scenario covers these errors. Two releases that share one content key are also not covered, so the number of entries and copies is unclear. Resolution: answered: Added scenarios for empty namespace ID, empty hash, and shared content key with one stored copy.
- R1.9 (wrong claim): The provisional decision cites ISO 27001 backup restoration testing as its benchmark. The input contains no source for this claim, so the benchmark cannot be checked. The ADR check also says No ADR is touched, but the decisions list is empty and gives no quoted decision lines as proof. Resolution: answered: Replaced ISO claim with AWS backup practice analogy and recorded empty ADR pack as no quoted decision.
- R1.10 (wrong claim): The error table names the command opendustpan backup inspect <file> as the one command that helps a missing file error, but the spec scenario for that error does not show this command, and the table advises running the backup again instead. The message and the command do not match. Resolution: answered: Changed missing file error help command to backup create and aligned scenario text.
- R2.1 (wrong claim): The 'Open questions' section reads 'none' but then lists two open questions with provisional decisions, and the adversarial pass R1.2 says 'open question' for old backups. The structure is confusing, and the spec has no requirement or scenario for the provisional 'warn and proceed' behavior on old backups. Resolution: answered: Removed provisional open questions; legacy backup behavior is now a decided requirement with a scenario.
- R2.2 (missing scenario): Provisional decision 2 (restore of an old backup without file entries warns and proceeds) has no requirement or scenario in the spec. The warning text, the one command that helps, and how the restore tells an old backup from a broken new one are not defined. Resolution: answered: Added requirement restore-legacy-without-entries with warning text, help command, and old-versus-new manifest rule.
- R2.3 (wrong claim): The adversarial pass answers R1.7 with 'plus scenario for stable row and file set', but the spec has no snapshot-consistency scenario. Design decision 3 states the snapshot only in design, with no requirement or test. Resolution: answered: Added requirement backup-snapshot with a scenario for a stable row and file set.
- R2.4 (wrong claim): The pass says it added scenarios for empty namespace ID and empty hash. The spec has only the empty hash scenario (and it is about a release row, not the constructor). The empty namespace ID scenario is missing. The pass also says the ISO claim was replaced by AWS guidance, and the AWS benchmark is an unsupported claim with no source. Resolution: answered: Added requirement backup-collector-inputs with empty namespace ID and empty hash scenarios; removed benchmark claims.
- R2.5 (missing scenario): No scenario or task covers the failing test on main for most requirements (unreadable file, corrupt bytes, empty hash, published-only, shared key, partial manifest, target-ID path). Tasks 1.1-1.3 map only three scenarios, which breaks the rule that each scenario maps to a named failing test. Resolution: answered: Expanded failing-test tasks so each scenario maps to a named failing test.
- R2.6 (failure mode): Restore check-before-write is not atomic against a write failure midway (disk full, store outage). The change defines no rollback, cleanup or retry for files already written, and no behavior for a restore that is run twice onto a target that already holds some of the keys. Resolution: answered: Added requirement restore-idempotent-retry for cleanup of partial writes and safe retry.
- R2.7 (failure mode): Backup does not say whether the SHA-256 is verified at backup time. A corrupt source blob would be copied into the backup and only fail at restore. Also a hostile manifest could name a key with path traversal or a malformed hash; only an empty hash is rejected. Resolution: answered: Added requirements backup-verify-hash and backup-manifest-shape for backup-time hash check and hostile manifest rejection.
- R2.8 (scope): The error table and spec do not include the other registry blobs the guide may name (for example registry_keys material) and do not say whether staged-upload rows are restored without files. Also the 'Restore writes under the target namespace ID' scenario is not in the restore-check requirement and the old-backup case is not in the guide task 3.2. Resolution: answered: Clarified staged rows, signing keys, and target namespace ID path in requirements and guide task.
- R2.9 (open question): Backup of a large registry: no limit, streaming, timeout or size reporting is defined, though the design names backup size and time as a risk. The docs page 'limits' step in the user path has no content. Resolution: answered: Added requirement backup-report-size for streamed copy with count and byte totals in the guide.
- R2.10 (missing scenario): Authorization actions for the backup and restore routes are not declared, and the audit record of a restore that writes registry files is not specified. Task 3.1 only says 'wire route' with no actions. Resolution: answered: Added requirement restore-authorized-audited with authorization actions and audit record; wiring task names the actions.

## Open questions

none

Questions considered and answered:

- Shall a namespace restore fail when a registry release file named by the backup is missing? Decided: the restore fails. A registry that names missing files breaks installs from the registry without a clear cause, so fail closed.
- How shall a restore handle a backup made before file copy? Decided: the restore warns and restores rows without files. Old backups hold registry rows with no file entries, so the restore tells them apart by the absence of the file manifest section and warns.
- Does any ADR govern registry backups? No. The pack lists no ADR, so no ADR governs registry backups and no quote applies.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
