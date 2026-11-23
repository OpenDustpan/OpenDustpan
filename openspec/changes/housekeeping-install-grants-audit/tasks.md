## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" in the proposal at its file and line on the current main, and in the branch of `housekeeping-principals-install` if it has merged. Record each difference in the pull request body. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Conformance cases for the install, on both dialects: NEW cases in `pkg/access/accesstest/review.go` (`TestAccessConformance` runs them): a new store, read through `ReviewGrants`, holds exactly the seven grants of the purposes with `ActsOnInstallation`, each with creator `@server`, `created_at_ms` greater than 0 and equal for the seven, and the role of its purpose from `pkg/access`. Check: the case fails before 2.2 (time 0, creator not the install).
- [ ] 1.2 Audit cases: NEW case in `pkg/audit/audittest` (the suite that `internal/store/*/sqlite_test.go` and `postgres_test.go` run): the chain of `@server` of a new store has seven entries, one for each grant ID, actor `@server`, reason "housekeeping grants of the install", and the chain verifies. Check: fails before 2.2 (no entry).
- [ ] 1.3 Store tests in the NEW files `internal/store/sqlite/housekeeping_install_internal_test.go` and `internal/store/postgres/housekeeping_install_internal_test.go`, table-driven, `t.Parallel()`:
  - a second `Open` of the same database writes no grant and no entry (the count stays seven and seven);
  - an operator revokes one grant, a second `Open` does not write it again (decision 9);
  - the marker is set and the seven grants are missing: `Open` writes none and returns no error (no hidden rule);
  - a forced failure of the audit append (the `storefault` helper) rolls back the grants and the marker, and `Open` returns an error that wraps `access.ErrHousekeepingInstall`;
  - two `Open` calls at the same time on one PostgreSQL database write seven grants in total (PostgreSQL file only);
  - restore case: export a store, stage the export with `StagePortable` (the helper `stageExport` of `internal/store/sqlite/restore_minimum_internal_test.go` does this on SQLite), restore it into a live store that holds the seven live grants: the restored store has seven grants, none twice, each with the creator and time of the backup, a grant that was revoked before the export is revoked after the restore, the seven audit entries of the backup are in the chain, and an `Open` after the restore writes nothing.
  Check: each case fails before 2.2, and the restore case fails if the restore keeps the live grants next to the ones of the backup.
- [ ] 1.4 Each guard has its own test that fails when the guard is removed: the conditional `WHERE housekeeping_installed_at_ms IS NULL`, the single transaction, and the closed purposes list as the only source of the holders. Check: removing each guard in a scratch copy fails its test (record the three results in the pull request body).

## 2. Implementation

- [ ] 2.1 Baseline edit (ADR 322, no migration code). In `internal/store/sqlite/migrations/0168_baseline.sql`, `internal/store/postgres/migrations/0168_baseline.sql` and `internal/store/schema/migrations/0168_baseline.sql` add `housekeeping_installed_at_ms` to the `installation` table and delete the seven `INSERT INTO grants` rows of the housekeeping roles. Add the query `MarkHousekeepingInstalled` to `internal/store/sqlite/queries.sql` and `internal/store/postgres/queries.sql` and run `make generate` (never edit `internal/store/sqlite/internal/db` or `internal/store/postgres/internal/db`). Check: `sqlc diff` is clean.
- [ ] 2.2 Write `installHousekeepingGrants` in the NEW files `internal/store/sqlite/housekeeping_install.go` and `internal/store/postgres/housekeeping_install.go` and call it in `Open` after `migrate` (`internal/store/sqlite/sqlite.go:338`, `internal/store/postgres/postgres.go:244`). Add `ErrHousekeepingInstall` to `pkg/access/access.go`. Follow design decisions 2 to 5. Check: 1.1 to 1.4 pass on both dialects.
- [ ] 2.3 Restore: no code change expected. If 1.3 shows that the stage keeps a seeded grant or loses the marker, fix it in `internal/store/sqlite/backup_stage_portable.go`, `internal/store/postgres/backup_stage_portable.go` or `internal/store/sqlite/restore.go`, and say so in the pull request body. Check: the restore case of 1.3 passes.
- [ ] 2.4 Update the expected values of the shared suites that read the `created_at_ms` or the creator of the seven grants, or the length of the `@server` audit chain (test side only: no filter, no hidden row). Edit `pkg/access/accesstest/review.go`, `review_more.go`, `holders.go` and the audit conformance cases that count entries of `@server`. Check: `go test ./internal/store/sqlite -run 'TestAccessConformance|TestAuditConformance' -count=1 -timeout 60m` and the same for `./internal/store/postgres` pass.
- [ ] 2.5 E2E step. In `e2e/access_review_test.go`, add a step to `TestAccessReview` (the scenario that `housekeeping-principals-install` extends): after the server start with the real binary, `GET /api/server/access-review` shows the seven grants with creator `@server` and a time greater than 0, `GET /api/audit/entries` shows seven entries, and a second start (the same data directory) shows the same seven. Check: `make e2e` runs the step.

## 3. Usability and done

- [ ] 3.1 Usability run-through. A person starts a new installation with the real binary, reads the access review and the audit log with the two routes of the proposal, and says who gave each housekeeping grant and when. A person's step.
- [ ] 3.2 Documentation, in the same pull request: in `docs/guides/principals-and-groups.md` (the page that `housekeeping-principals-install` extends), one short paragraph: the install writes the seven grants of the housekeeping principals on `@server` with the install time, and the audit log shows one entry for each; a revoke stays revoked on restart. No internal reference.
- [ ] 3.3 Run the fast checks (`make test-changed`) and push. Confirmed by: CI (the agent runs the fast checks only). Check: `make check` is green in CI with zero findings.
