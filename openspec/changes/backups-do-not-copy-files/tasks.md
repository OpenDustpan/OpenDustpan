## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

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

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the existing routes /api/server/backups and /api/server/backups/{} to the new copy and check, with backup:create and restore:execute. Check: API call POST /api/server/backups and the restore call under /api/server/backups/{} with an access token of a principal that holds backup:create and restore:execute on the installation; a call without the grant gets 403.
- [ ] 4.2 Add the e2e step and update the backup guide to remove the no-copy limit and cover legacy backups. Check: make e2e backup step through the existing routes POST /api/server/backups and /api/server/backups/{} with the access token of an administrator principal: publish a registry release, create a backup, restore on an empty blob store, then fetch the release from the registry. Docs page command runs as written and lists file copy, size report, legacy warning and limits.
- [ ] 4.3 Run the usability check and make check. Check: Usability check: the Operator completes the path from the docs page alone using /api/server/backups with an access token; each error names `opendustpan backup create --namespace <namespace>`; make check with zero findings.
