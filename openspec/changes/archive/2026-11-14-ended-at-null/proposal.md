Issue: #2993.

## Problem

An operator or a store adapter author reads a row in `backups`, `restores` or
`store_checks` that is not ended. The row holds `NULL` in PostgreSQL and `0` in
SQLite for `ended_at_ms`, so a query that compares with `0` or a schema check
gives a different answer in each dialect. The API hides this today because the
store maps `0` and `NULL` to the zero time, but a new query can read the wrong
value. The issue says a backup in the portable format behaves differently; that
claim is false on this checkout (see Evidence).

## Evidence

**Baselines.**

- `pkg/store/sqlite/migrations/0168_baseline.sql`: `:132`, `:149` and `:1141`
  hold `ended_at_ms   INTEGER NOT NULL DEFAULT 0,` for `backups`, `restores`
  and `store_checks`.
- `pkg/store/postgres/migrations/0168_baseline.sql`: `:2311`, `:2360` and
  `:2373` hold `ended_at_ms bigint,` for the same three tables.

**Queries.** The line numbers are the query name lines in each `queries.sql`.
The last column says how the query treats a row that is not ended.

| Query | SQLite | PostgreSQL | Not ended |
| --- | --- | --- | --- |
| `CreateBackup` | 947 | 1082 | SQLite binds 0; PostgreSQL binds NULL. |
| `GetBackup`, `ListBackups` | 952, 971 | 1087, 1106 | Read. SQLite scans `int64`; PostgreSQL scans `pgtype.Int8`. |
| `FinishBackup`, `FailEndedBackups` | 976, 983 | 1111, 1116 | Writes the end time. |
| `InsertRestore` | 1068 | 1206 | SQLite writes literal `0`; PostgreSQL writes literal `NULL`. |
| `GetRestore`, `ListRestores` | 1075, 1080 | 1213, 1218 | Read, as for the backups. |
| `EndRestore` | 1092 | 1230 | Writes the end time. |
| `CreateStoreCheck` | 4960 | 5383 | SQLite writes literal `0`; PostgreSQL writes literal `NULL`. |
| `GetStoreCheck`, `ListStoreChecks` | 4965, 4970 | 5388, 5393 | Read, as for the backups. |
| `FinishStoreCheck`, `FailEndedStoreChecks` | 4975, 4989 | 5398, 5410 | Writes the end time. |
| `CountStoreChecksEndedSince` | 4982 | 5403 | Filters `ended_at_ms > ?`; 0 and NULL are both excluded. |

- SQLite binds 0 for a running row (`pkg/store/sqlite/backup.go:44`,
  `pkg/store/internal/codec/codec.go:163`,
  `pkg/store/internal/codec/updates.go:73-76`), writes an end time at
  `pkg/store/sqlite/backup.go:293`, `:322`,
  `pkg/store/sqlite/restore_operation.go:603`,
  `pkg/store/sqlite/storechecks.go:162`, `:196`, and maps a stored 0 to the zero
  time at `pkg/store/sqlite/backup.go:341`, `pkg/store/sqlite/restore.go:83`,
  `pkg/store/sqlite/storechecks.go:211`. PostgreSQL maps zero to NULL
  (`pkg/store/postgres/backup.go:1921-1926`) and checks `.Valid`
  (`pkg/store/postgres/backup.go:1905`).
- Hand-written control-table copies keep the value unchanged:
  `pkg/store/sqlite/restore.go:455`, `:459`, `:503-521`;
  `pkg/store/postgres/backup.go:430-440`, `:491-497`;
  `pkg/store/sqlite/backup_columns.go:508-531`, `:591-596`.
- The portable claim is false. `pkg/backup/coverage.go:46-51` lists the three
  tables as control tables, and `pkg/backup/backup.go:101-108` says a backup
  leaves them out. The portable format writes a missing time as null
  (`pkg/backup/portable.go:35-37`; `pkg/store/sqlite/backup_portable.go:109-111`).
- A comparison of every `*_at_ms` column in both baselines finds only these
  three with this difference. Every other `DEFAULT 0` column has
  `DEFAULT 0 NOT NULL` in PostgreSQL, for example
  `audit_chain_state.migrating_at_ms`, `jobs.state_changed_at_ms`,
  `passkeys.last_used_at_ms` and `workspaces.locked_at_ms`.

## Fix boundary

**IN**

- `pkg/store/sqlite/migrations/0168_baseline.sql`: make the three columns
  `ended_at_ms INTEGER` (nullable, no default). Edit the baseline in place.
- `pkg/store/sqlite/migrations/0168_baseline.sql.sha256`: the new digest.
- `pkg/store/sqlite/queries.sql`: `InsertRestore` and `CreateStoreCheck` write
  `NULL`; the `CountStoreChecksEndedSince` comment says "has no end time".
- `pkg/store/sqlite/backup.go`, `restore.go`, `restore_operation.go`,
  `storechecks.go`: encode the zero time as `sql.NullInt64{Valid: false}` and
  read `.Valid`. Add `optionalMillis(time.Time) sql.NullInt64`.
- `pkg/store/sqlite/internal/db/queries.sql.go`: regenerate with
  `make generate`.
- `pkg/store/storetest/backup.go`, `restore.go`, `store_checks.go`: the case "a
  row that is not ended stores NULL, a row that is ended stores its time" and
  the optional `NullEndedWitness` capability with one raw read.
- `pkg/store/sqlite/sqlite_test.go`, `pkg/store/postgres/postgres_test.go`:
  implement the witness in each adapter test.
- `pkg/store/storetest/restore.go`: extend `testRestoreKeepsControlRows` with a
  not-ended row and the witness.

**OUT**

- No change to the PostgreSQL baseline, which is already nullable, or to the
  other `*_at_ms` columns, which have the same shape in both baselines.
- No change to `backup.Tables`, `backup.ControlTables`, the portable format or a
  backup file. These tables are control tables.
- No API, status code, audit entry or refusal change. No spec delta.

## Check

- Guard 1: a row that is not ended stores NULL in both stores. Test:
  `TestBackupConformance/a_not_ended_row_stores_NULL`,
  `TestRestoreConformance/a_not_ended_row_stores_NULL` and
  `TestStoreCheckConformance/a_not_ended_row_stores_NULL`. Mutation: change one
  of the three SQLite columns back to `INTEGER NOT NULL DEFAULT 0`; the case
  reads false, want true.
- Guard 2: a row that is ended stores its time. The same tests with a finished
  row. Mutation: make `FinishBackup`, `EndRestore` or `FinishStoreCheck` bind
  NULL; the case fails.
- Guard 3: the SQLite adapter does not bind 0. The same not-ended cases.
  Mutation: pass `codec.Millis` at `pkg/store/sqlite/backup.go:44`, or write
  `0` again at `pkg/store/sqlite/queries.sql:1072` or `:4962`; the case fails.
- Guard 4: a portable restore keeps the shape. Test:
  `TestRestoreConformance/a_restore_keeps_a_not_ended_row_as_NULL`. Mutation:
  copy `COALESCE(ended_at_ms, 0)` in `pkg/store/sqlite/restore.go:533-551`; the
  case fails.

## ADR check

- **ADR 42 decisions 2 and 3 (not owner-tagged).** Decision 3: "a time is an
  integer of Unix milliseconds, and a missing time is `null`, also where the
  SQLite schema stores `0`,". Decision 2: "The SQL part is one file
  `backups/<id>/sql/<table>.jsonl.gz` for each table in `backup.Tables`."
  Changed by: ADR 248 (the deferred restore into another region is moot),
  ADR 253 (changes decision 4). Current rule: this ADR, with ADR 248, 253. The
  fix makes SQLite store NULL and keeps the control tables out. No conflict.
- **ADR 322 decision 1 (owner).** "One baseline for each dialect until the first
  tag (owner). ... a pull request changes the schema by an edit of the baseline
  of each dialect, in place. It adds no numbered migration." Changed by: ADR 325
  (decisions 1, 5, 6), ADR 326 (extends decision 1), ADR 362 (changes decision
  5). Current rule: this ADR, with ADR 325, 326, 362. The fix edits the SQLite
  baseline in place. No conflict.
- **ADR 362 decision 4 (owner).** "The `.sha256` files of the schema baselines
  (ADR 322) are absent until the first tag. The release generates them."
  Changed by: none; ADR 363 extends decision 2 only. The files are in the tree
  today, and the fix updates the SQLite one. A separate change deletes them.
  No conflict.
- **ADR 274 decision 1.** "A generated shared `Querier`. ... A dialect still
  holds its own queries, schema and migrations." The text has no owner tag; the
  status line says decisions 1 to 7 are owner decisions. Changed by: ADR 362
  (extends decisions 2 and 6). Current rule: this ADR, with ADR 362. The fix
  stays in the SQLite dialect. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none
