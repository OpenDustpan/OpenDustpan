## Context

`housekeeping-principals-install` seeds the eleven housekeeping principals and the seven grants on `@server` in the baseline of each dialect. A baseline row has no install time and no audit entry. The review of that change (rev2, finding 3) found that the seven grants have `created_at_ms = 0`, no creator that records the install, and no entry in the audit chain of `@server`. This change moves the seven grants (and only them) from the baseline to an install step that writes them with the install time and one audit entry each. The principal rows stay in the baseline: a principal has no creator and no time field, so the baseline row loses no fact.

## Decisions

1. **The install step lives in `Open` of each store adapter.** After `migrate`, `Open` of `internal/store/sqlite/sqlite.go` (`Open` at :338, `migrate` at :1672) and of `internal/store/postgres/postgres.go` (`Open` at :244, `migrate` at :1494) calls NEW `installHousekeepingGrants`. `cmd/opendustpan/main.go` (the caller of `OpenStore`, :1795) and `dustpan.OpenStore` need no change, so every way to open a store (the server, an SDK user, a test) gets the grants. A step in `cmd/opendustpan` would let another caller open a store with no grants.
2. **One marker, one transaction.** NEW column `installation.housekeeping_installed_at_ms` (integer, NULL in the baseline). The step runs one transaction (`inTx` at `internal/store/sqlite/sqlite.go:1431`, the equivalent in the PostgreSQL adapter). In it: (a) `UPDATE installation SET housekeeping_installed_at_ms = ? WHERE id = 1 AND housekeeping_installed_at_ms IS NULL`; (b) if the update changed no row, commit and stop; (c) otherwise write the seven grants and their audit entries, then commit. The conditional update is the guard: two servers that start together on PostgreSQL both reach it, the first one to commit wins, and the second one sees `NULL` no more (it waits on the row lock of the update, then changes no row). The marker is the record of the install: its value is the install time, and it is visible in the installation row.
3. **The grants are the same rows as before, with real values.** Each grant has an ID from the same generator as `access.Service.Grant` (`pkg/access/service.go:354`), holder `principal:prn-housekeeping-<purpose>`, the role of the purpose from `pkg/access/roles.go`, object `@server`, creator `@server` (`history.ServerSubject`, `pkg/history/history.go:156`) and `created_at_ms` equal to the marker. The list of the seven comes from `principal.HousekeepingPurposes()` filtered by the field `ActsOnInstallation` (NEW in `housekeeping-principals-install`, task 2.5), never from a second list. A purpose that is added later with `ActsOnInstallation` gets its grant on a new installation only, which is a baseline-time choice (ADR 322, no migration code).
4. **One audit entry for each grant, in the chain of `@server`.** The step appends the entry with the same store function that namespace creation uses (`appendAuditEntry`, `internal/store/sqlite/audit.go:189`, and the PostgreSQL equivalent), inside the same transaction, with scope `audit.ServerScope` (`pkg/audit/audit.go:76`), actor `@server`, the action name of a grant creation, the grant ID as the object, and the reason "housekeeping grants of the install". The entry has the same shape as the entry of `access.Service.Grant`, so the review and the audit routes show it with no new code. The entries are appended in the order of the purposes list, so the chain order is stable.
5. **A failure stops the start.** On any error the transaction rolls back, `Open` returns an error that wraps `ErrHousekeepingInstall` (NEW sentinel in `pkg/access`), and the server does not start. The start log says `housekeeping install failed` with the cause. No grant is half written. This follows the rule "Fail closed".
6. **The restore keeps the live marker and loads the grants of the backup.** The SQLite restore builds a new baseline database, loads the table files of the backup, and copies the control tables from the live database. The installation row is one of them, and it copies only three columns: an `INSERT` of `id, state, kind` from the live installation table (`internal/store/sqlite/restore.go:778`). The new column would stay NULL, and the next `Open` would write the seven grants again next to the ones of the backup. So the statement copies the marker too (`id, state, kind, housekeeping_installed_at_ms`). The PostgreSQL restore keeps the whole installation table of the live control schema (`internal/store/postgres/restore.go:687`, `controlSchemaTables`), so it needs no change. Because the baseline holds no install grant, the staged database holds none before the load, and the backup brings its own (creator, time, revoke). The existing delete of seeded `grants` and `principals` rows in the stage stays as it is (`housekeeping-principals-install`, task 2.4).
7. **A restore into an installation that holds the seven live grants.** The restore clears the live `grants` table as it does for every table, then loads the grants of the backup. The seven live grants are therefore replaced by the seven of the backup (same holder, role and object, with the creator and the time of the backup). No double grant exists, and a revoke that an operator made before the export stays revoked. A test checks this on both dialects (task 1.3).
8. **A backup made before the marker was set is not a case.** A backup comes from a store that ran `Open`, so it holds the seven grants. The backup format is the same: the marker is a column of the installation row and the portable export of that row is unchanged by this change (the restore ignores the marker of the backup and keeps the live one, as for the policy rows). No upgrade code (ADR 322).
9. **No hidden rule.** An operator that revokes one of the seven grants does not get it back on the next start: the marker is set, so the step writes nothing. A count of rows would write the grant again after a revoke, which is a silent rule (No magic).

## Contract

Tables:

- `installation` gets one column: `housekeeping_installed_at_ms INTEGER NULL` (PostgreSQL: `BIGINT NULL`), in the baseline of both dialects and in `internal/store/schema/migrations/0168_baseline.sql`, with `make generate` for the generated code of `internal/store/sqlite/internal/db` and `internal/store/postgres/internal/db`. The seven `INSERT INTO grants` rows leave the baseline files (SQLite lines 2079-2085, PostgreSQL lines 1814-1820 in the branch of `housekeeping-principals-install`). The `INSERT INTO principals` rows stay.
- No other schema change. The new query `MarkHousekeepingInstalled` is in `internal/store/sqlite/queries.sql` and `internal/store/postgres/queries.sql`.

Files (install and restore):

- `internal/store/sqlite/sqlite.go` and `internal/store/postgres/postgres.go`: the call in `Open`.
- NEW `internal/store/sqlite/housekeeping_install.go` and `internal/store/postgres/housekeeping_install.go`: `installHousekeepingGrants`.
- `internal/store/sqlite/restore.go:778`: the installation copy adds the marker column (decision 6). `internal/store/sqlite/backup_stage_portable.go` and `internal/store/postgres/backup_stage_portable.go`: no behavior change; tests only (task 1.3).
- `pkg/access/access.go`: NEW `ErrHousekeepingInstall`.
- NEW test files `internal/store/sqlite/housekeeping_install_internal_test.go` and `internal/store/postgres/housekeeping_install_internal_test.go`.
- Conformance: NEW cases in `pkg/access/accesstest` (an installation has seven grants with the install time and one audit entry each) and in `pkg/audit/audittest` for the chain.

No route, no action and no role is new. The existing routes show the result: `GET /api/server/access-review` and `GET /api/audit/entries`.

## Security notes (defense in depth)

- Layer: the adapter. The conditional update is the only guard against a second install; a test sets the marker to a value and calls the step again (no row written), and a test removes the guard and expects the test to fail.
- Layer: the transaction. Grants, entries and marker commit together; a forced failure of the audit append rolls all of them back (test with the `storefault` helper).
- Layer: the holder and role come from the closed purposes list and the built-in role table; no input from outside the process reaches the step. A hostile database row (a marker set with no grants) leaves the server running with no grants and a review that shows none: the operator sees the gap, no rule restores it silently.
- Fail closed: an error in the step stops the start (decision 5).

## Questions considered and answered

- Why not write the grants in a trigger or a default in the baseline? SQL has no install time and no audit chain append; a trigger would be a hidden rule.
- Why a column and not a row in a new table? One fact (when the install ran) for one row (the installation). A table would add a join and a conformance suite for one value.
- Why not clear the grants in the restore? The baseline holds none, and the live ones are replaced by the generic clear of the table that the restore already does.
