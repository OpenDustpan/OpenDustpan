---
rfc: 104
title: Dialect-neutral store logic, so that a third SQL dialect can come later
status: discussing
created: 2026-10-22
---

# RFC 104. Dialect-neutral store logic, so that a third SQL dialect can come later

Status: discussing. This RFC answers finding 3 of the architecture review. The
owner gave the direction of that finding. This RFC does not reopen it:

- SQLite is a production adapter. More SQL dialects come later, so a third
  dialect is a design driver.
- Shared, dialect-neutral store logic becomes the rule for every area: one
  package for each area, and a transaction abstraction. A dialect holds its
  queries only.
- One SQLite runner holds the write lock.
- A declarative table list drives backup.

The RFC measures the problem, gives the target layout under `internal/`, says
what a new dialect must supply, checks the plan against MySQL, gives a
migration plan, and asks the owner five questions. It has no code.

ADR 272 moves the built-in adapters to `internal/`, with constructors in
`pkg/dustpan`. This RFC uses that layout. It names a package by its path after
step 8 of RFC 103 (`internal/store/...`). Today the same code is under
`pkg/store/...`.

The brief for this work calls the table list a "table registry". This RFC says
"table list", because the glossary uses "registry" for the private registry and
lists it under _Avoid_ for the inventory.

## Problem

The store has two SQL adapters: `pkg/store/sqlite` and `pkg/store/postgres`.
Each adapter implements the same 269 exported methods with its own copy of the
logic. The logic around the queries is the same in the two dialects: the
installation state check, the fence, the inventory publish, the audit append,
the paging, and the translation of driver errors into domain sentinels. Only
the queries, the locks and the backup engine must differ.

A change to the store is two changes. A third dialect would be a third copy of
about 12,000 lines of Go. Each copy can drift in behavior, and some already
drift (see "Where the dialects already differ").

Eleven packages under `pkg/store/internal/` already hold shared logic for some
areas: `jobstore`, `gatestore`, `driftstore`, `viewstore`, `registrystore`,
`githoststore`, `sinkstore`, `codec`, `devstrip`, `migrate` and `baseline`.
`jobstore` and `gatestore` define a `Runner` and a `Tx` interface, which each
dialect implements. This is the right direction. But the glue that implements
each `Tx` is still a copy for each dialect: `sqlite/gate.go` and
`postgres/gate.go` are 97 % the same, and `jobTx.InsertJob` is the same
text in both. The glue is a copy because sqlc makes a separate Go type for
each query parameter in each dialect, although the two types are often equal.

## Measured state

Measured on `main` at 48f4e31 (2026-10-22), on the files that are not tests.
A "line" is a line that is not blank and not a comment.

### How it was measured

- **Same lines.** For each file name that both adapters have, a line diff
  (Python `difflib`, no junk heuristic) after the dialect names are replaced
  with one token. "Same" is the size of the longest common subsequence.
  "Non-trivial" leaves out lines of 12 characters or fewer (`}`, `return nil`,
  `if err != nil {`), so that the count does not grow from syntax only.
- **Patterns.** `rg`/`grep -c` on the Go files of each adapter.
- **Generated code.** The method signatures and the struct fields that sqlc
  makes in `internal/db` of each adapter.
- **Health and risk.** Repowise `get_health` and `get_risk` for the largest
  pairs of files.

### Size and duplication

| | SQLite | PostgreSQL |
| --- | --- | --- |
| Go files (not tests) | 79 | 76 |
| Go lines, all (not tests) | 28,384 | 26,328 |
| Exported `Store` methods | 272 | 278 |
| `queries.sql` lines | 3,883 | 4,128 |
| sqlc query methods | 532 | 558 |
| Migration files | 68 | 68 |

- 74 file names are in both adapters. In those files, **14,388 of 18,463
  lines (78 %)** are the same in both. With trivial lines left out, **8,954 of
  12,069 lines (74 %)** are the same.
- The tests of the 48 shared test file names: 1,891 of 3,415 non-trivial lines
  (55 %) are the same. The conformance suite `storetest` (50 files, 34,544
  lines) is shared already.
- `queries.sql`: 1,485 of 2,281 non-trivial lines (65 %) are the same.
- Migrations: 196 of 1,218 lines (16 %) are the same. The type names and the
  table options differ in almost every `CREATE TABLE`.
- The exported methods: 269 names are in both adapters. Only `Close` and
  test helpers differ (`Close() error` on SQLite, `Close()` and
  `CloseContext` on PostgreSQL).
- sqlc: 524 query names are in both dialects. **504 method signatures are the
  same text.** Of 562 parameter and row structs with the same name, 432 have
  the same fields and types. The 130 that differ differ in these types:

  | SQLite | PostgreSQL | Fields |
  | --- | --- | --- |
  | `sql.NullInt64` | `pgtype.Int8` | 97 |
  | `sql.NullString` | `pgtype.Text` | 56 |
  | `int64` | `int32` | 24 |
  | `int64` | `bool` | 23 |
  | `string` (a JSON array) | `[]string` | 8 |
  | `int64` | `pgtype.Int8` | 7 |
  | `string` | `[]byte` | 6 |
  | other | other | 76 |

### Duplication for each area

Same non-trivial share of the largest pairs, with the Repowise duplication
score of the file where it reports one.

| Area | Files | SQLite lines | Same | Repowise duplication (SQLite / PostgreSQL) |
| --- | --- | --- | --- | --- |
| Runners | `runner.go` | 1,051 | 93 % | |
| Workspaces | `workspaces.go` | 947 | 87 % | 97 % / 100 % |
| Access | `access.go` | 828 | 93 % | 87 % / 100 % |
| Principals | `principals.go` | 632 | 91 % | |
| Gate glue | `gate.go` | 583 | 97 % | |
| Context | `objectcontext.go` | 516 | 96 % | |
| Lifecycle | `lifecycle.go` | 521 | 94 % | |
| Git hosts | `githost.go` | 493 | 94 % | |
| Stack updates | `updates.go` | 629 | 72 % | 95 % / 100 % |
| Journal | `journal.go` | 485 | 88 % | |
| Unit imports | `unitimport.go` | 441 | 96 % | |
| Job glue | `jobs.go` | 527 | 78 % | |
| Audit | `audit.go` | 403 | 82 % | |
| Inventory | `inventory.go` | 421 | 73 % | |
| Backup | `backup.go` | 828 | 19 % | 59 % / 34 % |
| Restore | `restore.go`, `restore_access.go` | 946 | 26 % | |
| Durable operations | `durable.go` | 610 | 21 % | |

The low shares are where the mechanism differs: backup and restore (a file on
SQLite, `COPY` and a staging schema on PostgreSQL), and durable operations
(`FOR UPDATE SKIP LOCKED` on PostgreSQL, a serialized read and write on
SQLite).

Repowise ranks `sqlite.go`, `postgres.go` and `postgres/backup.go` as the
three store files with the lowest health score (4.8 of 10, band "needs work").
All 17 of these files that Repowise reported on are hotspots with an increasing trend. The
history shows that a change to one adapter is a change to the other: when
`postgres.go` changes, `sqlite.go` changes in 93 % of the commits. For
`access.go` it is 100 %, for `updates.go` and `workspaces.go` 96 %.

### The cross-cutting logic, counted

Call sites in the Go files of each adapter (not tests).

| Concern | SQLite | PostgreSQL | Where it is today |
| --- | --- | --- | --- |
| Write transaction with the state check (`inTx`) | 47 | 42 | one helper for each dialect |
| Fenced write (`inFencedTx`) | 40 | 31 | one helper for each dialect |
| Other fenced helpers (control, locked and audited) | 6 | 3 | |
| Control write (`inControlTx`) | 22 | 28 | |
| Read snapshot (`inReadTx` / `inSnapshotTx`) | 18 | 16 | different names |
| Fence comparison that returns `namespace.ErrFenced` | 13 | 15 | copied, not one function |
| Inventory publish helpers | 28 | 28 | copied |
| Audit append | 37 | 38 | copied |
| "No row" checks (`sql.ErrNoRows` / `pgx.ErrNoRows`) | 201 | 202 | at each call site |
| Constraint checks (unique, foreign key, job index) | 20 | 21 | one classifier for each dialect, copied callers |
| Page token helpers | 11 | 11 | copied |
| Hand-written dynamic SQL statements | 70 | 92 | `?` against `$n` placeholders |

### Where the dialects already differ

These differences are in the code today. The conformance suite does not see
them, because each one needs a race or a specific concurrent write.

1. **Profile revision reads.** `Profiles.Revision` and `Profiles.Revisions`
   read the namespace, the profile and the revision in one read snapshot on
   SQLite (`inReadTx`). On PostgreSQL they run without a transaction
   (`p.read` calls the pool directly), so each statement has its own snapshot.
   A rename or delete between two statements gives a mixed answer only on
   PostgreSQL.
2. **Runner claim lookup.** `Runners.LookupClaim` reads in a read snapshot on
   SQLite and in a control write transaction on PostgreSQL.
3. **The lock that makes a count exact.** On PostgreSQL, the caps of saved
   views, registry keys and git hosts hold the namespace row `FOR UPDATE`, so
   a count and an insert are atomic. On SQLite, the same code relies on the
   one database-wide write lock (`_txlock=immediate`). Both are correct today.
   A dialect with row locks and no global write lock (MySQL) must take the
   PostgreSQL path, but the shared code does not say which path is the rule.
4. **Where the clock is read.** The sign-in failure window reads the clock of
   the store in SQL on PostgreSQL (one statement with `clock_timestamp()`) and
   in Go from `DatabaseNowMs` in the same transaction on SQLite. The result is
   the same. The logic is in two places in two forms.
5. **Durable claims.** PostgreSQL claims an operation with
   `FOR UPDATE OF o SKIP LOCKED` in one query (`ClaimDurableOperation`,
   `ClaimSinkOperation`). SQLite reads and updates in an IMMEDIATE
   transaction. 34 sqlc queries exist only in PostgreSQL, and 23 of them are
   lock or claim queries (`Lock...`, `...ForShare`, `...ForUpdate`).
6. **Backup column lists.** Each dialect has its own hand-written column list
   for each table (`sqliteBackupTables` in `backup_columns.go`, 706 lines;
   `pgCopyTables` in `backup.go`), next to the portable list in `pkg/backup`.
   A table is declared in four lists and in two migrations (see "The table
   list").
7. **Close.** `Close() error` on SQLite, `Close()` and `CloseContext` on
   PostgreSQL.

Each item is a candidate for a conformance case before its area moves (see
the migration plan). Item 1 may be a bug. This RFC does not fix it. The step
for the profile area adds a case and decides the rule.

### The table lists, counted

`pkg/backup` holds 74 data tables (`Tables`), 14 control tables, 3 emptied
tables and 16 access tables. The portable description holds 639 columns. Each
data table is declared in:

1. `backup.Tables` (or `ControlTables` / `EmptiedTables`, and
   `AccessTables`),
2. `portableSpec` in `pkg/backup` (columns, kinds, order),
3. `sqliteBackupTables` (columns again),
4. `pgCopyTables` (columns and order again),
5. `devstrip` classes, for a table that a principal owns,
6. the migrations of each dialect.

So about 1,900 column names are written by hand three times. The coverage
check (`CheckCoverage`) fails a build when the lists and the schema differ.
This makes the drift loud, but each new table still costs four Go edits.

## The target layout

### Packages

```
internal/store/
  sqlstore/            the dialect-neutral core
    tx.go              Tx, Runner, WriteOptions, Classifier
    fence.go           the installation state check and the fence check: one copy
    errors.go          ErrNoRows, the error classes, Translate
    page.go            keyset page tokens and limits
    audit.go           the audit append in the transaction of a write
    inventory.go       the inventory publish in the transaction of a write
    q/                 GENERATED: the shared Querier interface and its param and row types
  tables/              the table list: one declaration for each table (see "The table list")
  <area>store/         one package for each area: the logic of that area, written once
    stackstore/  workspacestore/  accessstore/  identitystore/  auditstore/
    inventorystore/  runnerstore/  durablestore/  profilestore/  ...
    jobstore/  gatestore/  driftstore/  ...   (the existing packages, moved)
  sqlite/              the SQLite dialect
    open.go            driver, pragmas, the process guard
    runner.go          the one writer and the read pool
    classify.go        driver errors to error classes
    queries.sql  migrations/  internal/db/ (sqlc)  shim.go (GENERATED)
    backup.go          the file engine: VACUUM INTO, the staged file, the swap
  postgres/            the PostgreSQL dialect, the same parts
    backup.go          the COPY engine and the staging schema
  migrate/  codec/  baseline/  devstrip/   (existing helpers, moved)
pkg/dustpan            OpenSQLite, OpenPostgres (ADR 272): each returns the store
                       of the dialect, built from the area packages
```

The area packages implement the ports. For example `stackstore.Store`
implements `stack.UpdateStore`. The dialect package builds one value of each
area store with its own `Runner` and returns them as the store bundle (finding
2 of the review). A dialect package has no method that implements a port by
itself, except where the mechanism is the dialect's own: open, close,
migrate, backup and restore.

### The Querier

sqlc keeps one config for each dialect and generates `internal/db` as today.
A small generator, `internal/tools/sqlcshim` (standard library and
`go/types` only), reads the generated package of every dialect and writes:

- `internal/store/sqlstore/q`: one `Querier` interface with each query that
  every dialect has with the same signature, and one copy of each param and
  row struct.
- `internal/store/<dialect>/shim.go`: a type that implements `q.Querier` on the
  dialect's `*db.Queries`. Each method converts the param struct and the row
  struct. Go allows a conversion between two struct types with the same
  underlying type, so most methods are one line:

  ```go
  func (s shim) InsertJob(ctx context.Context, p q.InsertJobParams) error {
  	return s.db.InsertJob(ctx, db.InsertJobParams(p))
  }
  ```

The generator fails when a query of `q.Querier` has a different signature in
one dialect. So a drift between the dialects stops `make generate`, and `sqlc
diff` in `make check` then fails. A query that exists in one dialect only (for
example a lock query) stays out of `q.Querier`. The dialect uses it in its own
`Runner` or `Tx` methods.

Before the generator can share most queries, the generated types must match.
The first migration step does that:

- `emit_pointers_for_null_types: true` for both dialects. sqlc supports it for
  SQLite and for PostgreSQL with `pgx/v5`. This makes `*string` and `*int64`
  in both, and removes 160 of the 297 field differences.
- `overrides` in `sqlc.yaml` for the PostgreSQL `int4` columns (to `int64`)
  and for the SQLite flag columns that are not yet `bool` (23 fields).
- The 8 array parameters (`[]string` on PostgreSQL, a JSON text on SQLite)
  and the 6 `[]byte` / `string` pairs stay outside `q.Querier`. Each area
  that uses one has a one-method interface that the dialect implements by
  hand.

`sqlc.yaml` names each query file. There is no shared SQL file. The owner's
direction is that a dialect holds its queries.

### The Tx and the Runner

```go
package sqlstore

// Tx is one open transaction. Its queries run in that transaction.
type Tx interface {
	q.Querier
	// Exec and Query run a hand-written statement. The statement uses ?
	// placeholders; the dialect rebinds them. They return ErrNoRows and
	// the error classes, never a driver error.
	Exec(ctx context.Context, stmt string, args ...any) (int64, error)
	Query(ctx context.Context, stmt string, args ...any) (Rows, error)
	// Lock takes a row lock that lasts until the transaction ends. A
	// dialect with one database-wide write lock implements it as a no-op
	// and says so in its conformance run.
	Lock(ctx context.Context, row LockRow, mode LockMode) error
	// Now reads the clock of the store.
	Now(ctx context.Context) (time.Time, error)
}

// Runner opens transactions.
type Runner interface {
	// Write runs fn in one write transaction. Before fn, it runs the
	// checks that opts asks for (see fence.go). An error of fn rolls back.
	Write(ctx context.Context, opts WriteOptions, fn func(Tx) error) error
	// Read runs fn in one read transaction with one snapshot.
	Read(ctx context.Context, fn func(Tx) error) error
}

type WriteOptions struct {
	// Control skips the installation state check: the writes that must
	// run in maintenance mode (the control tables, the restore).
	Control bool
	// Fence, when set, is checked in the transaction before fn.
	Fence *namespace.Fence
	// Namespace is the lock on the namespace row that fn needs: none,
	// share (the fence) or update (a count that must be exact).
	Namespace LockMode
}
```

`Read` gives one snapshot on every dialect. This removes differences 1 and 2.
`WriteOptions.Namespace` makes difference 3 explicit: an area that needs an
exact count asks for `LockUpdate`, on every dialect.

### The fence and the error translation, in one place

`sqlstore.Write` is the only function that calls `Runner.Write`. It runs the
checks in this order, in the transaction, and fails closed:

1. Unless `opts.Control`: read the installation state with
   `q.GetInstallationState`. An error, a missing row or an unknown value
   returns `maintenance.ErrUnavailable`. Maintenance mode returns
   `maintenance.ErrInMaintenance`.
2. If `opts.Fence` is set: take the namespace row lock that `opts.Namespace`
   names (at least share), read the failover version, return
   `namespace.ErrNotFound` for no row, and `namespace.ErrFenced` for a
   different version.
3. Run `fn`.

Today each dialect has this code, and PostgreSQL reads the state and the
version in one batch with the advisory lock to save round trips. A dialect may
keep such a fast path: it implements the optional interface
`sqlstore.CheckReader`, which returns the raw state and version. The
comparison and the sentinels stay in `fence.go`. The domain decision is in one
place, and only the read is in the dialect.

Errors: each dialect implements `Classifier`:

```go
type Class int

const (
	ClassOther Class = iota
	ClassNoRows
	ClassUnique     // with the constraint name
	ClassForeignKey
	ClassCheck
	ClassBusy       // SQLITE_BUSY, a lock wait timeout
	ClassRetry      // a serialization failure or a deadlock
	ClassCanceled
)
```

The shim and `Tx.Exec`/`Tx.Query` call the classifier on each error. So no
driver error leaves the dialect package, and an area package checks
`sqlstore.ErrNoRows` in place of 403 driver-specific checks. An area maps a
class to its domain sentinel with `sqlstore.Translate(err, sqlstore.Map{
ClassUnique: stack.ErrExists})`. A class that the map does not name stays a
wrapped error, so an unknown error never becomes a domain answer by accident.

### The SQLite runner: one writer

Today every SQLite write is an IMMEDIATE transaction on one pool, and the file
lock serializes the writers. A writer that waits more than 10 s for the lock
gets `SQLITE_BUSY` (`busy_timeout(10000)`). An `RWMutex` (`s.mu`) guards the
pool only for the restore swap. The comments of `inTx` warn that a nested
write transaction deadlocks.

The proposal:

- **Two pools.** A write pool with `SetMaxOpenConns(1)` and
  `_txlock=immediate`, and a read pool with `query_only(true)` and several
  connections. WAL mode lets the readers run during a write.
- **One write lock in Go.** `Runner.Write` takes a semaphore (a channel of
  size 1) and honors `ctx` while it waits. The wait time is a metric. The
  restore swap takes the same semaphore and the read pool's `RWMutex`, so
  `s.mu` has one job.
- **Busy handling.** In one process, writers queue on the semaphore and never
  meet `SQLITE_BUSY`. `busy_timeout` stays as a second layer for a write from
  another process (the process guard should stop a second server, but the
  `sqlite3` shell of an operator can still write). `SQLITE_BUSY` becomes
  `ClassBusy`, and the area returns a retryable error. The store does not
  retry by itself, because a retry of a non-idempotent write is not safe.
- **Nested writes fail closed.** `Write` marks `ctx` with a value. A second
  `Write` with a marked `ctx` returns an error at once, in place of the
  deadlock that the comments warn about today. A test sends a nested write
  and expects the error.
- **Locks are no-ops.** `Tx.Lock` does nothing on SQLite, because the one
  writer holds the only write lock. The SQLite conformance run declares it,
  and the cases that need a row lock run against the writer.

### The table list

`internal/store/tables` declares each table once:

```go
type Table struct {
	Name    string
	Class   Class          // Data, Control, Emptied
	Access  bool           // the table is part of a keep-access restore
	Columns []Column       // portable name and kind, in schema order
	OrderBy []string       // the order of the rows in a backup file
	After   []string       // the tables that a foreign key needs first
	Owner   *Owner         // for dev data: the column that links to a principal
}

func All() []Table // in load order; the order is checked against After
```

Each area package declares its own tables in a function (`stackstore.Tables()`)
and `tables.All()` joins the lists in a fixed order. There is no `init`
registration, so the list is plain data that a test can read.

What reads it:

- **Backup.** The SQLite engine and the PostgreSQL `COPY` engine read the
  columns and the order from the list. `sqliteBackupTables` and `pgCopyTables`
  go away.
- **Portable backup.** `pkg/backup` keeps the format types (`Column`, `Kind`,
  `PortableTable`), which are part of the backup format. The list of the
  built-in tables moves to `internal/store/tables`, because it describes the
  built-in schema, which ADR 272 does not make SDK. The portable writer takes
  the list as an argument.
- **Restore.** The class decides what a restore loads, keeps or empties, and
  `Access` decides the keep-access restore.
- **Dev data.** `devstrip` reads `Owner` in place of its own class list.
- **Coverage.** `CheckCoverage` compares the list with the live schema
  (`pragma_table_info`, `information_schema.columns`), as today. A dialect
  supplies only the read of the live columns.

A new table then costs one declaration and one migration for each dialect.

## What a new dialect must supply, and what it gets

A dialect must supply:

1. `queries.sql` with each query of `q.Querier`, and an entry in `sqlc.yaml`.
2. `migrations/`, from one baseline, with the frozen-file checksums.
3. A `Runner` and a `Tx`: begin, commit, rollback, isolation, `Lock`, `Now`,
   and the placeholder rebind for hand-written statements.
4. A `Classifier` for its driver errors.
5. The backup engine: export, stage and swap, and the live column read.
6. The hand-written methods outside `q.Querier` (array parameters, the queries
   with dialect syntax).
7. A conformance run: `storetest` and each port suite, against a real server
   in CI, and the e2e suite with the dialect.
8. A constructor in `pkg/dustpan` and its user documentation.

A dialect gets, with no code of its own: every area's logic, the fence and the
state check, the audit append, the inventory publish, the paging, the
translation into domain sentinels, the table list, the portable backup
format, the dev-data strip, and the conformance suites.

## The plan against MySQL

This section checks the abstraction against MySQL 8.4. Each fact is to be
checked against the MySQL 8.4 manual before a MySQL RFC.

| MySQL fact | Where it touches the plan | Leak? |
| --- | --- | --- |
| No `RETURNING`. MySQL 8.0 and 8.4 do not have it, as far as the author knows; the brief says "before 8.0.21", which the author could not confirm. MariaDB 10.5 and later has `INSERT ... RETURNING`. sqlc's MySQL docs say to use `:execresult` or `:execlastid`. | 5 SQLite and 8 PostgreSQL queries use `RETURNING`. The `q.Querier` method of such a query returns the row. | **Yes.** The MySQL dialect implements those methods by hand (an insert, then a select in the same transaction), outside the generated shim. |
| No transactional DDL. Each DDL statement commits by itself (atomic DDL since 8.0 is atomic for one statement only). | `migrate` applies a file and records it in `schema_migrations` in one transaction. | **Yes.** A failed MySQL migration can leave half a file applied. The MySQL migration rule must be one DDL statement for each file, or statements that can run again. The `migrate` package needs a dialect flag for it. |
| Upsert is `INSERT ... ON DUPLICATE KEY UPDATE`, with a row alias since 8.0.19 (`VALUES()` is deprecated since 8.0.20). It fires on **any** unique key, not on one named conflict target. | 55 `ON CONFLICT` and 47 `excluded.` uses in the SQLite queries. | **Yes, in meaning.** A table with two unique keys can update the wrong row. The MySQL queries must be checked one by one. The conformance suite needs a case for each upsert on a table with more than one unique key. |
| JSON type and `JSON_TABLE` (8.0.4 and later) in place of `json_each` and `= ANY($1)`. | The array parameters already stay outside `q.Querier`. | No new leak. |
| `BIGINT` milliseconds. The clock is `UNIX_TIMESTAMP(NOW(3)) * 1000`. | Time columns are `BIGINT` already. `Tx.Now` hides the clock. | No. |
| `FOR SHARE` and `SKIP LOCKED` (8.0.1 and later). | `Tx.Lock`, and the durable claim queries. | No. MySQL takes the PostgreSQL path. |
| No transaction-scoped and no shared advisory lock. `GET_LOCK` is for the session and is exclusive only. | PostgreSQL takes `pg_advisory_xact_lock_shared` for every write and the exclusive form to enter maintenance mode. | **Yes.** Proposal: the gate is a row lock on the `installation` row (`FOR SHARE` for a write, `FOR UPDATE` to enter), on every dialect with row locks. Open question 4. |
| The default collation `utf8mb4_0900_ai_ci` ignores case and accents. | Names, keys and the name prefix filter compare bytes today. | **Yes.** Every name and key column needs a binary collation, set in the migrations. A conformance case with two names that differ in case only is needed (it may exist; the MySQL RFC checks). |
| `TEXT` cannot be a key without a prefix length. An index key has at most 3,072 bytes. | The SQLite and PostgreSQL schemas use `TEXT` keys. | **Yes, in the schema only.** MySQL needs `VARCHAR(n)` keys, so each key needs a maximum length. That is a domain limit that does not exist today. |
| Default isolation REPEATABLE READ with gap locks. A deadlock (1213) or a lock wait timeout (1205) aborts the statement or the transaction. | `ClassRetry` and `ClassBusy`. | Partly. The area code must treat `ClassRetry` as a retryable error, as PostgreSQL serialization failures need. |
| sqlc supports the `mysql` engine with `database/sql`. `emit_pointers_for_null_types` is not supported for MySQL. | The shared `q` types use pointers for null columns. | **Yes.** The MySQL config needs an override for each nullable column type to a pointer, or the shim converts. To be tried in a spike. |
| The Go driver `github.com/go-sql-driver/mysql` is MPL-2.0. | `make trivy` fails on a restricted license. | To check. A new dependency needs the owner's yes. |

So the abstraction holds for the logic, the fence, the errors and the table
list. It leaks in four places that a MySQL dialect must handle by hand:
`RETURNING`, the migration atomicity, the meaning of an upsert, and the
collation and key length of the schema.

## Migration plan

Each step is one pull request and one area. Each step is behavior-neutral: the
existing `storetest` suites and the port suites pass with no change to a case,
except where the step says it adds a case. A step that finds a difference
between the dialects adds a conformance case first, in its own pull request,
and the case decides the rule.

All steps wait for step 8 of RFC 103 (the store adapters move to
`internal/store/`). Step 8 is a scripted move. Today it waits for #1450 and
#1431. A step of this plan that lands first would make the move bigger, and
the move would make each open step conflict. Step 2 of RFC 103 (#1481) is
merged. Step 3 (#1482, ready for review) changes `unitimport.go` in both
adapters and `internal/codec`. Step 5 (#1483, draft) moves `storefault` and
changes no store file.

Sizes are the lines of the area in one dialect today. A step adds about 80 %
of them as shared code and deletes the two copies, so the diff is about 2.5
times the number.

| Step | Pull request | Lines | Risk | Waits for |
| --- | --- | --- | --- | --- |
| 0 | The ADR for this RFC | none | | the owner |
| 1 | Align the generated types: `emit_pointers_for_null_types`, `emit_interface`, the `int4` and flag overrides. Update the call sites. No logic changes | about 250 call sites (163 uses of the null types today) | low (the compiler finds each site) | step 8 of RFC 103 |
| 2 | `sqlcshim`, `sqlstore/q`, and the core of `sqlstore`: `Tx`, `Runner`, `Classifier`, `ErrNoRows`, page tokens. No caller yet | 900 new | low | 1 |
| 3 | The fence and the state check in `sqlstore.Write`. `inTx`, `inFencedTx`, `inControlTx` and the read helpers of both dialects call it | 400 | **high**: every write | 2 |
| 4 | The SQLite runner: two pools, the semaphore, nested-write detection | 300 | **high**: concurrency | 3 |
| 5 | The table list. Backup, restore, portable backup and dev data read it. Delete `sqliteBackupTables`, `pgCopyTables`, `portableSpec` | 1,900 deleted | medium | 3, #1485 item 2 if it changes the staging path |
| 6 | Collections and saved views | 470 | low | 3 |
| 7 | The private registry | 320 | low | 3 |
| 8 | Notification sinks and the sink lease | 430 | low | 3 |
| 9 | Git hosts | 490 | low | 3 |
| 10 | Object context | 520 | low | 3 |
| 11 | Unit imports | 440 | low | 3, #1482 |
| 12 | Gate and job glue: move `gatestore` and `jobstore` onto `sqlstore.Tx`; delete `gateTx` and `jobTx` in both dialects | 1,110 | medium | 3, #1392, #1431 |
| 13 | Drift, manual locks, maintenance, revocations, temporary administrators, IP allow lists | 1,000 | medium | 3, #1468 |
| 14 | Profiles and preferences. First, a conformance case for difference 1 | 420 | medium | 3 |
| 15 | Runners. First, a case for difference 2 | 1,050 | medium | 3 |
| 16 | Audit, the audit chain, the audit feed | 810 | **high**: the chain | 3 |
| 17 | Identity: principals, sessions, sign-in, access tokens, sign-in providers | 1,810 | medium | 3, #1465, #1486 |
| 18 | Access and reviews | 1,090 | medium | 3, #1465 |
| 19 | Inventory, lifecycle, units, the namespace page | 1,440 | medium | 3, the pull request for #1418 |
| 20 | Durable operations, schedules, heartbeats, host recovery, idempotency | 1,120 | **high**: two mechanisms | 3, 4 |
| 21 | Workspaces, blame, state at | 1,240 | **high**: OpenTofu writes | 3, #1468, #1452, #1457 |
| 22 | Stack updates, the journal, item keys, state at | 1,460 | **high**: the Pulumi CLI hot path | 3, #1435, #1450, #1454, #1458 |
| 23 | Backup and restore engines: the shared order of the steps in `sqlstore`, the engines stay in each dialect | 1,500 | **high** | 5, #1485 |
| 24 | Delete the empty helpers. Update `CLAUDE.md`, `GLOSSARY.md` and `.golangci.yml` | small | low | all |

The order puts the core first (1 to 5), because each area needs it. Then the
areas go from low risk and high duplication to high risk. The stack and
workspace paths go late, because the tool CLIs depend on them and many open
pull requests change them. Each step from 6 on is independent of the others,
so two authors can work in parallel on two areas.

### The in-flight work

- **Keyset paging, #1418.** #1418 is an issue with no pull request yet. It
  changes the unit list, the TFE workspace list and the stack list of the
  Pulumi CLI. Proposal: #1418 lands in the current layout, because it fixes a
  measured problem for users now (1.3 s for each page at 100,000 units). Steps
  19, 21 and 22 wait for its pull request. If #1418 starts after step 2, it
  should use the page token helper of `sqlstore` once, in place of a copy for
  each dialect.
- **Template databases, #1485.** #1485 is an issue with no pull request yet.
  Items 1, 3 and 4 change the test setup of `pkg/store/postgres`
  (`TestMain`, `isolatedURL`) and the CI jobs. No step of this plan changes
  them. Item 2 changes the staging path of a restore, which steps 5 and 23
  change too, so steps 5 and 23 wait for item 2, or go first and tell its
  author. The step 1 change to `sqlc.yaml` does not touch the test setup.
- **Generated code.** Step 1 regenerates both `internal/db` packages. Each
  open pull request that changes `queries.sql` (#1392, #1431, #1465, #1468)
  then gets a conflict in the generated files, which `make generate` solves,
  and may need its new call sites changed from `sql.NullString` to `*string`.
  Proposal: land step 1 when few such pull requests are open, and say so in
  the pull request.

## Options

### A. Do nothing

Keep two adapters with copied logic, and grow the shared packages when someone
touches an area.

- Gives: no migration cost.
- Costs: each change stays two changes (93 % to 100 % co-change). The
  behavior drift grows (seven differences today). A third dialect is a third
  copy of about 12,000 lines.

### B. Area interfaces, hand-written glue (the `jobstore` pattern, everywhere)

Each area defines a `Tx` interface with its own row types. Each dialect
implements it by hand.

- Gives: no generator. Each area's interface is small and reads well.
- Costs: the glue stays a copy in each dialect. `gate.go` shows it: 583 lines,
  97 % the same. A third dialect writes all glue again.

### C. A shared generated Querier (the proposal)

Keep one sqlc config and one query file for each dialect. Generate a shared
`Querier` from the queries that have the same signature in every dialect, and
a conversion shim for each dialect.

- Gives: the logic and the glue are written once. A drift between the
  dialects stops `make generate`. A new dialect writes SQL, not Go, for most
  areas.
- Costs: a generator to keep (about 400 lines), and step 1 changes about 250
  call sites. The shared interface has about 500 methods; an area package
  sees all of them. The array and `RETURNING` queries stay hand-written.

### D. Shared SQL

One query file with the portable subset, compiled by sqlc for each engine, and
a file for each dialect with the rest.

- Gives: the SQL is written once where it can be (65 % of the query lines are
  the same today).
- Costs: it goes against the direction that a dialect holds its queries. The
  portable subset is small for three dialects (no `RETURNING` on MySQL, three
  upsert forms, three JSON forms, three placeholder forms). A change to a
  shared query must be tested on every engine.

### E. A query builder or an ORM

- Costs: `CLAUDE.md` and depguard ban ORMs. A query builder adds a dependency
  and hides the SQL that a reviewer must read. Not considered further.

### Recommendation

C, with B's small interfaces where they help: an area package may declare a
narrow interface of the `q.Querier` methods that it uses, so that a test can
fake it. D can come later for a query that stays the same in every dialect,
if the owner wants it, without a change to the layout.

## Open questions for the owner

1. **The shared Querier: generated (option C) or hand-written glue
   (option B)?** Recommended: C. B keeps a copy of the glue in each dialect
   (`gate.go` today: 583 lines, 97 % the same).
2. **Start before or after step 8 of RFC 103 (the move to
   `internal/store/`)?** Recommended: after. Step 8 is a scripted move, and
   each step of this plan would conflict with it.
3. **SQLite: two pools and a Go semaphore for the one writer (as proposed), or
   one pool of one connection for reads and writes?** Recommended: two pools.
   One connection would serialize the reads of the console behind each write
   of a Pulumi CLI checkpoint.
4. **The maintenance gate: a row lock on the `installation` row for every
   dialect with row locks, in place of the PostgreSQL advisory lock?**
   Recommended: yes, in step 3. MySQL has no shared, transaction-scoped
   advisory lock, and one gate for all dialects is less code. This changes
   the PostgreSQL write path, so step 3 measures the write latency before and
   after with `make load`.
5. **Where the list of the built-in tables lives: `internal/store/tables`, out
   of the SDK, with the format types kept in `pkg/backup`?** Recommended: yes.
   Under ADR 272 the built-in schema is not a port, and a third-party store
   has its own tables.

## Outcome

Empty until the owner accepts or drops this RFC.
