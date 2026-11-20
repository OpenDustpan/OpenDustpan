# 389. A restore that keeps the access tables keeps the temporary administrators

Status: accepted · 2026-11-20. Decided by the project owner (owner, 2026-11-20). The owner changed where `temp_admins` comes from on the restore path with `KeepAccessTables`

Changes:

- Changes ADR 253 decision 6: on the path with `KeepAccessTables`, the swap keeps the live `temp_admins` rows. The test row 2g changes with it.

## Context

ADR 70 decision 7 says that a restore by a caller without `server:restore-access` "replaces the other data and keeps the current access tables". The list of access tables is `backup.AccessTables`. It holds the principals, the grants, the sessions, the access tokens, the local users and the other tables that decide who can act.

`temp_admins` is not in `backup.AccessTables`. ADR 253 decision 6 says so on purpose: "`temp_admins` on the path with `KeepAccessTables`. The swap keeps the live access tables. It takes `temp_admins` from the backup." The swap, `ApplyStagingKeepingAccess`, drops each staged row that names a `principal_id` that the live `principals` table does not hold. The test row 2g says that a staged row of a live principal stays.

Issue #3809 shows the effect. A caller with `server:restore` only can restore a backup that holds a staged `temp_admins` row for a live principal. A temporary administrator has an administrator grant for the time of the row. So the restore gives the live principal an administrator grant. A row with a past end time makes the next sweep disable the live principal, and revoke its grants and tokens. The caller does not hold `server:restore-access`. The restore keeps the live principals, so the row passes the check that drops unknown principals. This is the case that decision 6 asks to keep.

A `temp_admins` row is a grant of authority: it names a principal, a time and a reason. ADR 70 decision 7 exists so that a restore without `server:restore-access` cannot change who holds authority. ADR 253 treats the staged `temp_admins` rows as data to strip, not as access data. That is the gap.

The rule of ADR 253 decision 6 that the strip "stays free of the path" is not the problem. The strip and the swap-time check of decision 12 read the whole staging area on both paths. They stay as they are.

On 20 November 2026 the project owner approved option (a) of the question on #3809. The owner said:

```text
"Decisions are approved as written, all seem logical/uncontroversial as is."
```

Options that the owner did not choose:

- Take the rows from the backup and check each row of a live principal (kind, expiry, end time). This keeps a second copy of the rules of the temporary administrator in the restore.
- Drop every staged row of a live principal and keep the live rows. The result is the same as the decision here for a live principal. It still takes the staged rows of a principal that the restore does not keep, and it needs the drop step.

## Decision

1. **`temp_admins` is an access table (owner, 2026-11-20).** The table `temp_admins` goes into `backup.AccessTables`. A restore without `server:restore-access`, and a restore with the setting `restore.replace-access-tables` off, keep the live rows of `temp_admins`. They do not take a row from the backup.
2. **This changes ADR 253 decision 6 (owner, 2026-11-20).** The paragraph "`temp_admins` on the path with `KeepAccessTables`" no longer applies. In its place:
   - The swap keeps the live `temp_admins` rows, like the other access tables. After the swap, the table holds the live rows and no staged row.
   - The step of `ApplyStagingKeepingAccess` that drops staged rows by `principal_id` ends. The staged rows are not in the swap, so no row can name a principal that the live table does not hold. `KeepAccessResult.DroppedTempAdmins` ends too.
   - The report of a restore has no `temp-admins` count from the swap. The class `temp-admins` of the strip report stays, because the strip still removes staged rows by `subject` on both paths.
3. **The path with the access tables does not change.** A restore by a caller with `server:restore-access` still takes `temp_admins` from the backup. It takes the principals that the rows name from the same backup. So the foreign key holds. The strip and the swap-time check of ADR 253 apply as before.
4. **The strip and the swap-time check stay.** The strip of ADR 253 decision 6 still reads and cleans the staged `temp_admins` rows by `subject`. The swap-time check of decision 12 still refuses a staged dev row. Both are layers of defense in depth (ADR 250). The decision here does not rely on them, and they do not rely on it.
5. **No other part of ADR 253 changes.** Decisions 1 to 5, 7 to 17 and the other test rows stay.

## Consequences

- Code that changes:
  - `pkg/backup/access_tables.go`: the entry in `AccessTables` and its comment. The entry goes after the principals table that its foreign key names.
  - `pkg/backup/restore_record.go` and `pkg/backup/devdata.go`: the comments that describe the drop. The restore report loses the swap count of the class `temp-admins`.
  - `pkg/store/sqlite/restore_access.go`, `pkg/store/sqlite/restore.go`, `pkg/store/postgres/restore_access.go` and `pkg/store/postgres/restore.go`: the steps that drop staged rows and return `DroppedTempAdmins`. The keep list and the lock list read `backup.AccessTables`, so they follow the new entry.
  - `pkg/backup/devstrip_coverage_test.go`: a new reason for `temp_admins`, if the coverage entry needs one.
- The test row 2g of ADR 253 changes. The staged `temp_admins` has three rows: one with the subject `Dev:x`, one with a `principal_id` that the live `principals` does not hold, and one with a live principal. The live table has its own rows. A restore with `KeepAccessTables` leaves the live rows as they were, with the same values. None of the three staged rows is in the table. The test fails when `temp_admins` is not in `AccessTables`. A second case runs the repro of #3809: a staged row of a live administrator with a past end time. After the restore and a sweep, the live administrator is not disabled, and its grants and tokens stay. A third case runs a restore with `server:restore-access`: the rows of the backup arrive with their principals. Both adapters run the three cases (`storetest`).
- The test row 2c, the test row 2h and the test row 3a keep their cases. The strip and the swap-time check still see the staged `temp_admins` table.
- Docs change in the same pull request as the code.
  - `docs/guides/backup-restore-upgrade.md` (the access tables paragraph) and `docs/guides/restore-access-setting.md` list the tables that a restore keeps. Each gets the words "temporary administrators" in that list.
  - The e2e restore tests (`e2e/restore_access_test.go`) get a step. A backup has a temporary administrator for a live principal. A restore runs without `server:restore-access`. The check shows that the principal has the grants that it had before.
- No migration and no new column. The schema does not change. A backup that holds `temp_admins` rows stays valid. The manifest format does not change.
- The restore of a backup into an installation that has temporary administrators now keeps them, also when the backup has none. An operator who wants the rows of a backup uses `server:restore-access`, or the setting `restore.replace-access-tables`.
- This ADR changes no code. A change from the spec factory or a lane fix that has the failing test of #3809 first carries the code and the test changes. The pull request of that work links this ADR.
- The words of ADR 253 decision 6 and of the test row 2g stay as the owner decided them on 19 October 2026. A reader finds this change in the "Changed by" list of ADR 253 and in `docs/adr/INDEX.md`.
