## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Work

- [ ] 1.1 Add failing tests for StagePortable template reuse on both dialects, buildPortableStaging changed-template refusal, and restore cleanup in pkg/store/storetest/portable.go. Check: New cases in pkg/store/storetest/portable.go fail on main and pass after the fix.
- [ ] 1.2 Change SQLite portable staging in pkg/store/sqlite/backup_stage_portable.go to build the template once per process in a process-private directory and copy it per stage. Check: Template reuse case passes with a pre-copy version and hash check on the copied bytes in pkg/store/sqlite/backup_stage_portable.go.
- [ ] 1.3 Apply verification against recordedMigrations in pkg/store/postgres/backup_stage_portable.go with no file copy on the PostgreSQL staging path. Check: PostgreSQL portable mismatch case returns backup.corrupt in pkg/store/postgres/backup_stage_portable.go.
- [ ] 1.4 Remove the staging lock file and empty restore-tmp directories of the ending restore only in pkg/backup with blob cleanup in pkg/blob/fs, without touching non-empty or live trees. Check: Restore end leaves no lock file of that restore id and no empty restore-tmp tree in pkg/backup and pkg/blob/fs.
- [ ] 1.5 Read backup check from the backup directory manifest in pkg/backup so a copied backup is found while a missing backup still answers 404. Check: Copied-backup check case passes for a directory copied without a registry row.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the change through ANY /api/server/restores and ANY /api/server/backups/{}/check with the operator gate and an access token, and run an e2e step against the real directory. Check: e2e step calls ANY /api/server/restores with the operator gate and an access token, then ANY /api/server/backups/{}/check, and leaves no staging lock file and no empty restore-tmp tree. Confirmed by: CI (make e2e).
- [ ] 3.2 Update docs/guides/backup-restore-upgrade.md for template reuse, cleanup, and copied-backup check. Check: docs page updated in docs/guides/backup-restore-upgrade.md. A person's step.
- [ ] 3.3 Measure the p95 SQLite portable stage build time and record the result. Check: Staging build p95 measured under 2 seconds per SQLite portable stage. Confirmed by: the driver's gates.
- [ ] 3.4 Run the full check gate. Check: `make check` with zero findings. Confirmed by: CI (make check).
