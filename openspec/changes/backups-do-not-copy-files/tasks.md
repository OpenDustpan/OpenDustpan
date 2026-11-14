## 1. Failing tests

- [ ] 1.1 Add failing backup suite test for registry file entries. Check: TestBackupRegistryFiles names each SHA-256 key, shared key stored once, and fails on main.
- [ ] 1.2 Add failing backup suite test for collector inputs and manifest shape. Check: TestBackupRegistryInputs rejects empty namespace ID, empty hash, and hostile hash, and fails on main.
- [ ] 1.3 Add failing backup suite tests for snapshot, hash check, unreadable file, published-only scope, and size report. Check: TestBackupRegistrySnapshot, TestBackupVerifyHash, TestBackupUnreadableFile, TestBackupPublishedOnly, and TestBackupReportSize fail on main.
- [ ] 1.4 Add failing restore suite test for registry file copy. Check: TestRestoreRegistryFiles restores bytes under the target namespace ID path and fails on main.
- [ ] 1.5 Add failing restore suite tests for missing file, check-before-write, and corrupt bytes. Check: TestRestoreMissingRegistryFile, TestRestorePartialManifestWritesNothing, and TestRestoreCorruptBytes fail on main.
- [ ] 1.6 Add failing restore suite test for old backups without file entries. Check: TestRestoreLegacyWithoutEntries warns, restores rows without files, and fails on main.
- [ ] 1.7 Add failing restore suite tests for retry cleanup and for authorization with audit. Check: TestRestoreIdempotentRetry and TestRestoreAuthorizedAudited fail on main.

## 2. Build

- [ ] 2.1 Extend blob collection with SHA-256 registry references and streamed copy path. Check: go test -race ./pkg/backup/... ./pkg/store/...
- [ ] 2.2 Add restore presence, hash, and check-before-write logic with partial-file cleanup. Check: go test -race ./pkg/store/...
- [ ] 2.3 Add release file hash reads where needed without a numbered migration. Check: make generate with clean sqlc diff.

## 3. Definition of done

- [ ] 3.1 Wire routes with authorization actions and backup commands to the new copy and check. Check: Backup and restore routes declare backup:create and restore:execute actions; make e2e backup step with registry publish, backup, restore on empty store, then fetch from registry.
- [ ] 3.2 Update backup guide to remove the no-copy limit and cover legacy backups. Check: Docs page backup command runs as written and lists registry file copy, size report, legacy warning, and limits for large registries.
- [ ] 3.3 Run usability check, e2e step, docs page, and full check. Check: make check with zero findings.
