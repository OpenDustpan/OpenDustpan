---
rfc: 104
title: Dialect-neutral store logic, so that a third SQL dialect can come later
status: decided
created: 2026-10-22
adr: 274
---

# RFC 104. Dialect-neutral store logic, so that a third SQL dialect can come later

Status: decided. [ADR 274](../adr/0274-dialect-neutral-store-logic.md) records
the decisions. The owner accepted all seven recommendations of this RFC on
22 October 2026. This RFC answers finding 3 of the architecture review. The
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
migration plan, and asks the owner seven questions. It has no code.

### The ADR that this RFC changes

ADR 3 ("SQL persistence: hand-written per dialect, sqlc for fixed queries")
is the governing decision. This RFC keeps most of it: each dialect keeps its
own schema, migrations and queries, no SQL is shared, sqlc generates the fixed
queries, and dynamic queries stay hand-written. The ADR of step 0 supersedes
two parts of ADR 3:

- **"Each dialect is its own adapter with its own schema, migrations and
  queries."** After this RFC, a dialect is not a full adapter. The area
  packages hold the logic, and a dialect supplies the queries, the
  migrations, the `Runner`, the `Classifier` and the backup engine. The
  schema, migrations and queries stay in the dialect.
- **"Adapters never parse driver-specific error codes."** The `Classifier` of
  each dialect parses driver error codes (`ClassUnique`, `ClassForeignKey`,
  `ClassCheck`, `ClassBusy`, `ClassRetry`). The drift exists already: today
  each dialect has its own constraint classifier (20 and 21 call sites). The
  new rule: only the `Classifier` of a dialect parses driver error codes, and
  no driver error or code leaves the dialect package. `ON CONFLICT DO NOTHING`
  with a check of the affected rows stays the first choice for a duplicate
  insert.

ADR 3 also says that replicas are a later option, and that a read after a
write in the same flow must go to the writer. This RFC keeps that as a
contract of `Runner.Read` (see "The Tx and the Runner"): `Read` sees every
write that a `Write` committed before `Read` started, in this process. A
dialect that adds a replica must keep this contract, for example by reading
from the writer.

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

Measured on `main` at 00b31b4 (2026-10-22), on the files that are not tests.
A "line" is a line that is not blank and not a comment.

### How it was measured

- **Same lines.** For each file name that both adapters have, a line diff
  (Python `difflib`, no junk heuristic) after the dialect names are replaced
  with one token. "Same" is the size of the longest common subsequence.
  "Non-trivial" leaves out lines of 12 characters or fewer (`}`, `return nil`,
  `if err != nil {`), so that the count does not grow from syntax only.
- **Patterns.** `rg -o ... | wc -l` on the Go files of each adapter, not
  tests and not `internal/` (`-g '*.go' -g '!*_test.go' -g '!internal/**'`).
  The table of cross-cutting logic names the pattern of each count. A count
  includes the definition of the helper.
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

Call sites in the Go files of each adapter (not tests). The pattern column
gives the regular expression or the names that were counted.

| Concern | Pattern | SQLite | PostgreSQL | Where it is today |
| --- | --- | --- | --- | --- |
| Write transaction with the state check | `\binTx\(` | 47 | 42 | one helper for each dialect |
| Fenced write | `\binFencedTx\(` | 40 | 31 | one helper for each dialect |
| Other fenced helpers (control, locked and audited) | the `inFenced*Tx` and `inLocked*Tx` names other than `inFencedTx` and `inFencedAuditedTx` | 6 | 3 | |
| Control write | `\binControlTx\(` | 22 | 28 | |
| Read snapshot | `\b(inReadTx\|inSnapshotTx)\(` | 18 | 16 | different names |
| Fence comparison that returns `namespace.ErrFenced` | `namespace\.ErrFenced` on a comparison line | 13 | 15 | copied, not one function |
| Inventory publish helpers | `\b(publish\w*\|upsert\w*Inventory\|remove\w*Inventory\|move\w*Inventory\w*\|appendInventoryChange\|writeInventoryLock)\(` | 42 | 42 | copied |
| Audit append | `\b(appendAuditEntry\|appendRunnerAudit\|appendMemberAudit\|insertAuditEntry\|placeAuditEntry)\(` | 40 | 41 | copied |
| "No row" checks | `(sql\|pgx)\.ErrNoRows` | 201 | 202 | at each call site |
| Constraint checks (unique, foreign key, job index) | the calls of the classifier of each dialect | 20 | 21 | one classifier for each dialect, copied callers |
| Page token helpers | the `encode*Token` and `decode*Token` names | 11 | 11 | copied |
| Hand-written dynamic SQL statements | the `ExecContext`, `QueryContext`, `Exec` and `Query` calls with a string literal | 70 | 92 | `?` against `$n` placeholders |

The first version of this RFC gave 37 / 38 audit appends and 28 / 28
inventory publishes without a pattern. Those counts do not reproduce, so
this version gives the counts of the named patterns.

### Where the dialects already differ

These differences are in the code today. The conformance suite does not see
them, because each one needs a race or a specific concurrent write.

1. **Profile revision reads.** `Profiles.Revision` and `Profiles.Revisions`
   read the namespace, the profile and the revision in one read snapshot on
   SQLite (`inReadTx`). On PostgreSQL they run without a transaction
   (`p.read` calls the pool directly, `postgres/profile.go:411`), so each
   statement has its own snapshot. In normal operation this gives no wrong
   result, because revisions are append-only and a delete is a soft delete.
   But a restore swap between two statements gives `ErrNotFound` for a
   profile that exists in both states. SQLite is safe, because the swap holds
   `s.mu`. This is a defect, filed as #1490. Many other PostgreSQL methods
   read on the pool directly (`grep 's.queries\.' postgres/*.go`), so it is a
   class of defect, not one method.
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
the migration plan). This RFC does not fix item 1. #1490 fixes it, and
`Runner.Read` removes the class: an area package can read only through
`Read` or `Write`, so it cannot read on the pool directly.

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
  every dialect has with the same types, and one copy of each param and row
  struct.
- `internal/store/<dialect>/shim.go`: a type that implements `q.Querier` on the
  dialect's `*db.Queries`. Each method converts the param struct and the row
  struct. Go allows a conversion between two struct types with the same
  underlying type, so a `:one` or `:exec` method is one line:

  ```go
  func (s shim) InsertJob(ctx context.Context, p q.InsertJobParams) error {
  	return s.db.InsertJob(ctx, db.InsertJobParams(p))
  }
  ```

  A `:many` method is not one line: Go cannot convert `[]db.X` to `[]q.X`.
  87 of the shared query names return a slice of structs (counted in the
  generated `internal/db` of both dialects: `func (q *Queries) Name(...)
  ([]Struct, error)`). The shim converts them with one generic helper,
  `convertRows[From, To any](rows []From, conv func(From) To) []To`, which
  allocates one new slice for each call.

**The generator compares types, not text.** The same signature text does not
mean the same types: of 562 param and row structs with the same name in both
dialects, 130 differ. `sqlcshim` loads each generated package with
`go/types` and compares each method and each struct with
`types.Identical` on the underlying types: the field names, the field order
and the field types. Only a query whose types are identical in every dialect
goes into `q.Querier`. The generator fails when a query of `q.Querier` stops
being identical in one dialect. So a drift between the dialects stops `make
generate`, and `sqlc diff` in `make check` then fails. A query that exists in
one dialect only (for example a lock query) stays out of `q.Querier`. The
dialect uses it in its own `Runner` or `Tx` methods.

**The `q` package imports no driver package.** It must not import
`database/sql`, `github.com/jackc/pgx/...` (with `pgtype`) or a SQLite
driver. The generator refuses a type from such a package, and depguard
enforces the same rule on `internal/store/sqlstore/...` and the area
packages. So driver types stay in the dialect, as driver errors do.

**One interface for each area.** The generator also writes one narrow
interface for each area, from a section marker in each dialect's
`queries.sql` (for example `-- area: stack`). The marker must be the same in
every dialect, and a shared query without a marker fails the generator. An
area's `Tx` embeds only the interface of its area and the core queries
(state, fence, audit, inventory). Open question 7 asks the owner to choose
this or one interface of about 500 methods.

**Why `emit_interface`.** sqlc writes a `Querier` interface for each dialect
with `emit_interface: true`. `sqlcshim` does not need it, because it reads
the methods of `*db.Queries` with `go/types`. Step 1 sets it for one reason:
the dialect's own `Runner` and its tests can depend on `db.Querier` in place
of `*db.Queries`, so a test can give a fault-injecting fake to the shim. If
the owner prefers fewer generated lines, step 1 can drop it with no other
change.

Before the generator can share most queries, the generated types must match.
The first migration step does that:

- `emit_pointers_for_null_types: true` for both dialects. sqlc supports it for
  SQLite and for PostgreSQL with `pgx/v5`. This makes `*string` and `*int64`
  in both, and removes 153 of the 297 field differences (the
  `sql.NullInt64` / `pgtype.Int8` and `sql.NullString` / `pgtype.Text`
  rows). The 7 `int64` / `pgtype.Int8` fields stay different: they are a
  difference in nullability, and become `int64` / `*int64`. The step 1 pull
  request fixes the nullability of each in the schema of one dialect, or
  keeps the query out of `q.Querier`.
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

// Runner opens transactions. A dialect implements it. Only package
// sqlstore can call its write method (see "Only sqlstore.Write opens a
// write").
type Runner interface {
	// beginWrite runs fn in one write transaction. It is unexported, so
	// only sqlstore.Write calls it. An error of fn rolls back.
	beginWrite(ctx context.Context, fn func(Tx) error) error
	// Read runs fn in one read transaction with one snapshot. The
	// snapshot includes every write that a Write committed before Read
	// started, in this process (read-your-writes). A dialect that reads
	// from a replica must keep this contract, for example by reading
	// from the writer.
	Read(ctx context.Context, fn func(Tx) error) error
	// Gate holds the maintenance gate until the transaction of tx ends:
	// GateWrite for a write (shared with other writes), GateEnter to
	// enter maintenance mode (exclusive: it waits for every write in
	// flight, and stops new ones). See "The maintenance gate".
	Gate(ctx context.Context, tx Tx, mode GateMode) error
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

A Go interface with an unexported method can be implemented only in its own
package. So the dialect does not implement `Runner` directly. It implements
`sqlstore.Dialect` (exported: begin, commit, rollback, `Gate`) and passes it
to `sqlstore.NewRunner`, which returns the `Runner`. The exact shape is for
step 2. The rule is that no code outside `sqlstore` can open a write
transaction without the checks.

### The fence and the error translation, in one place

`sqlstore.Write` is the only function that opens a write transaction. It runs
the checks in this order, in the transaction, and fails closed:

1. Unless `opts.Control`: take the maintenance gate with
   `Runner.Gate(GateWrite)`, then read the installation state with
   `q.GetInstallationState`. An error, a missing row or an unknown value
   returns `maintenance.ErrUnavailable`. Maintenance mode returns
   `maintenance.ErrInMaintenance`.
2. If `opts.Fence` is set: take the namespace row lock that `opts.Namespace`
   names (at least share), read the failover version, return
   `namespace.ErrNotFound` for no row, and `namespace.ErrFenced` for a
   different version.
3. Run `fn`.

### Only sqlstore.Write opens a write: the layers

This is security code: the fence, the maintenance check, the audit chain and
the restore move into one place. One mistake there affects every write. So
the rule "only `sqlstore.Write` opens a write" has more than one layer, and
each layer stops a bypass alone:

1. **The type system.** The write method of `Runner` is unexported, so no
   package outside `sqlstore` can call it. Test: a compile test in
   `sqlstore` (a `go/types` check of the area and dialect packages) fails
   if a package other than `sqlstore` calls a write method of `Runner` or
   `Dialect`.
2. **A lint rule.** A `forbidigo` rule in `.golangci.yml` refuses a call of
   `Dialect.Begin` (and the driver's own `BeginTx`, `Begin` and `Exec` on a
   pool) outside the dialect's own runner file and `sqlstore`. Test: a
   fixture file in `testdata` that the lint run must refuse.
3. **The check in the transaction.** `sqlstore.Write` reads the state and
   the fence in the same transaction as `fn`, not before it. Test: a
   conformance case that changes the state, or the failover version,
   between the call and the transaction, and expects the refusal.
4. **Fail closed.** An unknown state value, a missing row or an error of
   the read refuses the write. Test: a fake `Tx` that returns each of
   these, and expects `ErrUnavailable` or `ErrFenced`.
5. **The store keeps its own checks.** Each step of the migration plan keeps
   every existing redundant check and its defense-in-depth comment (for
   example the fence check in the SQL `WHERE` of a fenced update, and the
   checks of the restore). A step does not remove a check because
   `sqlstore.Write` now does it. Each step adds one test for each layer
   that it touches, which sends hostile input to that layer alone.

A denial of each layer writes an audit entry where the write had an audit
scope, as today.

Today each dialect has this code, and PostgreSQL reads the state and the
version in one batch with the advisory lock to save round trips. A dialect may
keep such a fast path: it implements the optional interface
`sqlstore.CheckReader`, which returns the raw state and version. The
comparison and the sentinels stay in `fence.go`. The domain decision is in one
place, and only the read is in the dialect.

### The maintenance gate

Today PostgreSQL takes `pg_advisory_xact_lock_shared` for each write and the
exclusive form to enter maintenance mode, so that maintenance mode starts only
after every write in flight ends. SQLite gets the same effect from its one
write lock.

The proposal: the gate is a primitive of each dialect's `Runner`
(`Runner.Gate`), with one shared contract and one conformance case. The rule
is written once in `sqlstore.Write`, and only the mechanism is in the
dialect, the same pattern as `CheckReader`.

The contract:

- `GateWrite` is shared: two writes do not wait for each other at the gate.
- `GateEnter` is exclusive: it waits until every transaction that holds
  `GateWrite` ends, and a new `GateWrite` waits until the transaction that
  holds `GateEnter` ends.
- The gate lasts until the end of the transaction, also on a rollback.
- An error of the gate refuses the write (fail closed).

The conformance case "enter maintenance waits for in-flight writes": a write
holds `GateWrite` and waits. `GateEnter` must not return until the write
ends. After maintenance mode starts, a new write returns
`maintenance.ErrInMaintenance`.

The mechanism of each dialect:

| Dialect | `GateWrite` | `GateEnter` |
| --- | --- | --- |
| PostgreSQL | `pg_advisory_xact_lock_shared` (as today) | `pg_advisory_xact_lock` (as today) |
| SQLite | nothing: the one writer is the gate | nothing, for the same reason |
| MySQL | `SELECT ... FROM installation ... FOR SHARE` | `... FOR UPDATE` on the same row |

PostgreSQL keeps its advisory lock. A `FOR SHARE` on the `installation` row
for every PostgreSQL write is rejected for two reasons:

- **A hot row.** PostgreSQL writes a row lock into the tuple. Every write in
  the installation then writes to one row, which is a point of contention
  that the advisory lock does not have.
- **MultiXact churn.** When two or more transactions hold a share lock on
  the same row, PostgreSQL makes a MultiXact ID for the set of holders. At
  high write concurrency this grows the MultiXact space and adds vacuum and
  wraparound cost. An advisory lock lives in the shared lock table and
  writes nothing to a tuple.

MySQL has no shared, transaction-scoped advisory lock (`GET_LOCK` is for the
session and exclusive only), so a row lock is the MySQL mechanism. InnoDB
keeps row locks in its lock system, not in the row, so it has no MultiXact
cost. The MySQL RFC measures the cost of the hot row at high write
concurrency with `make load`.

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

Today every SQLite write is an IMMEDIATE transaction, and the file lock
serializes the writers. Three places open a connection that can write:

1. `sqlite.go:483` (`openDatabase`): the pool of the store.
2. `restore.go:286`: its own connection (`SetMaxOpenConns(1)`) to fence the
   namespaces of a staged file.
3. `restore_access.go:774`: its own connection to the staged access tables.

Places 2 and 3 open the staged file of a restore, not the live database. The
live writes go through place 1. A writer that waits more than 10 s for the lock
gets `SQLITE_BUSY` (`busy_timeout(10000)`). An `RWMutex` (`s.mu`) guards the
pool only for the restore swap. The comments of `inTx` warn that a nested
write transaction deadlocks.

The proposal:

- **Two pools.** A write pool with `SetMaxOpenConns(1)` and
  `_txlock=immediate`, and a read pool with `query_only(true)` and several
  connections. WAL mode lets the readers run during a write.
- **One write lock in Go.** The SQLite runner takes a semaphore (a channel of
  size 1) before each write and honors `ctx` while it waits. The wait time is
  a metric. The restore swap takes the same semaphore and the read pool's
  `RWMutex`, so `s.mu` has one job.
- **Every writer connection.** "One writer" holds only if every place that
  opens a writer takes the semaphore. All three places above open their
  connection through the runner: place 1 is the write pool. The restore
  takes the semaphore once, for the whole restore, and places 2 and 3 get
  their connection from a runner method that refuses to open one when the
  caller does not hold the semaphore. So a staged connection cannot write at
  the same time as a live write, and the swap does not take the semaphore a
  second time. A lint rule refuses `sql.Open` in
  `internal/store/sqlite` outside the runner file, and a test lists the
  `sql.Open` calls of the package and fails on a new one.
- **Busy handling.** In one process, writers queue on the semaphore and never
  meet `SQLITE_BUSY`. `busy_timeout` stays as a second layer for a write from
  another process (the process guard should stop a second server, but the
  `sqlite3` shell of an operator can still write). `SQLITE_BUSY` becomes
  `ClassBusy`, and the area returns a retryable error. The store does not
  retry by itself, because a retry of a non-idempotent write is not safe.
- **Nested writes fail closed, in two layers.** A nested write would wait for
  the semaphore that its own caller holds: a deadlock.
  1. `Write` marks `ctx` with a value. A second `Write` with a marked `ctx`
     returns an error at once. Test: a nested write with the marked `ctx`
     expects the error.
  2. The marker misses a callback that starts from a fresh context
     (`context.Background()`). So the semaphore wait has a limit of its own:
     10 s, the same as `busy_timeout`, or the deadline of `ctx` if it is
     sooner. At the limit, the write returns `ClassBusy` as a retryable
     error, writes a log entry with the wait time, and does not run.
     Test: a nested write with a fresh context expects the error after the
     limit (the test sets a short limit), and no deadlock.

  The second layer stays when the first one catches every known case,
  because a new caller can start a fresh context.
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

Every declaration stays in `internal/store/tables`, one file for each group
of tables, and `tables.All()` returns them in a fixed order. The area
packages do not declare their own tables: step 7 lands before the area
packages exist (steps 8 to 24), and one central list is the one place that a
reviewer reads to see every table. There is no `init` registration, so the
list is plain data that a test can read.

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
   `Gate`, and the placeholder rebind for hand-written statements.
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

Only a dialect in this repository gets that list. `internal/store/sqlstore`
and the area packages are under `internal/`, which ADR 272 keeps out of the
SDK. A dialect in another repository can use only the ports and the
conformance suites of `pkg/`, and must implement the about 270 port methods
itself. Open question 6 asks the owner to decide this.

**The CI cost of a third engine.** CI runs the `store` job once for each
dialect (a matrix entry, with a PostgreSQL service container for
PostgreSQL), and the `e2e` job (timeout 70 minutes) with a PostgreSQL
service. A third engine adds one `store` matrix entry with its service
container, and one e2e run with that engine. The matrix entries run in
parallel, so the wall-clock time of a pull request grows little, but the
runner minutes of the `store` job grow by about one half (from two entries to
three), plus one e2e run. The MySQL RFC measures the real numbers.

## The plan against MySQL

This section checks the abstraction against MySQL 8.4. Each fact is to be
checked against the MySQL 8.4 manual before a MySQL RFC.

| MySQL fact | Where it touches the plan | Leak? |
| --- | --- | --- |
| No `RETURNING`. MySQL 8.0 and 8.4 have no `RETURNING`, in any statement. MariaDB 10.5 and later has `INSERT ... RETURNING`. sqlc's MySQL docs say to use `:execresult` or `:execlastid`. | 5 SQLite and 8 PostgreSQL queries use `RETURNING`. The `q.Querier` method of such a query returns the row. | **Yes.** The MySQL dialect implements those methods by hand (an insert, then a select in the same transaction), outside the generated shim. |
| No transactional DDL. Each DDL statement commits by itself (atomic DDL since 8.0 is atomic for one statement only). | `migrate` applies a file and records it in `schema_migrations` in one transaction. | **Yes.** A failed MySQL migration can leave half a file applied. The MySQL migration rule must be one DDL statement for each file, or statements that can run again. The `migrate` package needs a dialect flag for it. |
| Upsert is `INSERT ... ON DUPLICATE KEY UPDATE`, with a row alias since 8.0.19 (`VALUES()` is deprecated since 8.0.20). It fires on **any** unique key, not on one named conflict target. | 55 `ON CONFLICT` and 47 `excluded.` uses in the SQLite queries. | **Yes, in meaning.** A table with two unique keys can update the wrong row. The MySQL queries must be checked one by one. The conformance suite needs a case for each upsert on a table with more than one unique key. |
| The affected-row count of `ON DUPLICATE KEY UPDATE` is 1 for an insert, 2 for an update and 0 for an update that changes nothing (with the default client flags). | ADR 3 inspects the affected rows to see a duplicate insert. | **Yes, in meaning.** The MySQL `Tx.Exec` must give the same meaning as the other dialects (1 for a new row, 0 for a duplicate), or each caller must know the MySQL numbers. The MySQL RFC chooses one, with a conformance case. |
| `ON CONFLICT DO NOTHING` has no exact equivalent. `INSERT IGNORE` ignores a duplicate key, but also turns other errors (for example a truncation, a foreign key or a `NOT NULL` error) into warnings. | Each `ON CONFLICT DO NOTHING` insert. | **Yes.** `INSERT IGNORE` is not allowed: it hides errors, so it does not fail closed. Use `ON DUPLICATE KEY UPDATE id = id` (a no-op update of the key), and read the affected rows as above. |
| No partial index. The PostgreSQL migrations have 23 partial indexes, 13 of them unique (pattern: `CREATE (UNIQUE )?INDEX ... WHERE` in one statement), for example `grants_current ... WHERE (revoked_at_ms IS NULL)` and `group_members_current`. | A partial unique index is the conflict target of an `ON CONFLICT` and the rule "one current row". | **Yes.** MySQL needs a generated column (for example a key that is `NULL` when the row is not current, because a unique index allows many `NULL` values), or a different schema. That changes the `ON CONFLICT` targets of the queries. The 10 partial indexes that are not unique can be full indexes, with a cost in size. |
| JSON type and `JSON_TABLE` (8.0.4 and later) in place of `json_each` and `= ANY($1)`. | The array parameters already stay outside `q.Querier`. | No new leak. |
| The clock. `NOW(3)` is the time when the statement started, and stays the same in a statement. `SYSDATE(3)` is the time when it is read, the equivalent of `clock_timestamp()`. `UNIX_TIMESTAMP(NOW(3)) * 1000` is a `DECIMAL`, not an integer. | Time columns are `BIGINT` already. `Tx.Now` hides the clock. The sign-in failure window uses `clock_timestamp()` on PostgreSQL. | No, if the MySQL `Tx.Now` uses `CAST(FLOOR(UNIX_TIMESTAMP(SYSDATE(3)) * 1000) AS SIGNED)` or the equivalent. A query that reads the clock in SQL needs the same expression. |
| `FOR SHARE` and `SKIP LOCKED` (8.0.1 and later). | `Tx.Lock`, and the durable claim queries. | No. MySQL takes the PostgreSQL path. |
| No transaction-scoped and no shared advisory lock. `GET_LOCK` is for the session and is exclusive only. | PostgreSQL takes `pg_advisory_xact_lock_shared` for every write and the exclusive form to enter maintenance mode. | No, with `Runner.Gate`: the MySQL gate is a row lock on the `installation` row (see "The maintenance gate"). PostgreSQL keeps its advisory lock. |
| The default collation `utf8mb4_0900_ai_ci` ignores case and accents. | Names, keys and the name prefix filter compare bytes today. | **Yes.** Every name and key column needs a binary collation, set in the migrations. A conformance case with two names that differ in case only is needed (it may exist; the MySQL RFC checks). |
| `TEXT` cannot be a key without a prefix length. An index key has at most 3,072 bytes. Under `utf8mb4`, `VARCHAR(n)` can use up to 4n bytes, and the columns of a composite key share the limit (for example `namespace_id` and `name`: two `VARCHAR(255)` columns use up to 2,040 bytes). | The SQLite and PostgreSQL schemas use `TEXT` keys. | **Yes, in the schema only.** MySQL needs `VARCHAR(n)` keys, so each key needs a maximum length, and the sum of the composite keys must fit. That is a domain limit that does not exist today. |
| Default isolation REPEATABLE READ with gap locks. A deadlock (1213) rolls back the transaction. A lock wait timeout (1205) rolls back the statement only, by default (`innodb_rollback_on_timeout=OFF`): the transaction stays open with part of its work. | `ClassRetry` and `ClassBusy`. | **Yes.** On MySQL, `ClassBusy` must roll back the whole transaction before it returns, so that no part of a write commits. A conformance case: a write that meets a lock wait timeout after its first statement commits nothing. The area code treats `ClassRetry` as a retryable error, as PostgreSQL serialization failures need. |
| sqlc supports the `mysql` engine with `database/sql`. `emit_pointers_for_null_types` is not supported for MySQL. | The shared `q` types use pointers for null columns. | **Yes.** The MySQL config needs an override for each nullable column type to a pointer, or the shim converts. To be tried in a spike. |
| The Go driver `github.com/go-sql-driver/mysql` is MPL-2.0. | `scripts/trivy-license-gate.py` passes LOW and MEDIUM, and MPL-2.0 is MEDIUM. | No for `make trivy`: the gate passes it. A new dependency still needs the owner's yes. |

So the abstraction holds for the logic, the fence, the gate, the errors and
the table list. It leaks in five places that a MySQL dialect must handle by
hand: `RETURNING`, the migration atomicity, the meaning of an upsert (the
conflict target, `INSERT IGNORE` and the affected-row count), the partial
indexes, and the collation and key length of the schema. The rollback of a
lock wait timeout is a rule for the MySQL `Runner`, not a leak into the area
packages.

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
| 0 | The ADR for this RFC. It supersedes the two parts of ADR 3 that "The ADR that this RFC changes" names | none | | the owner |
| 1 | Align the generated types: `emit_pointers_for_null_types`, `emit_interface`, the `int4` and flag overrides. Update the call sites. No logic changes | about 250 call sites (163 uses of the null types today) | low (the compiler finds each site) | step 8 of RFC 103 |
| 2 | `sqlcshim`, `sqlstore/q`, and the core of `sqlstore`: `Tx`, `Runner`, `Dialect`, `Classifier`, `ErrNoRows`, page tokens, the depguard and `forbidigo` rules. No caller yet | 900 new | low | 1 |
| 3 | The fence and the state check in `sqlstore.Write`. `inTx`, `inFencedTx`, `inControlTx` and the read helpers of both dialects call it. Each dialect keeps its gate mechanism as today (behavior-neutral) | 400 | **high**: every write | 2 |
| 4 | A conformance case: "enter maintenance waits for in-flight writes". It passes on both dialects today | 150 | low | 3 |
| 5 | `Runner.Gate`: the gate becomes a primitive of each dialect's `Runner`, and `sqlstore.Write` calls it. PostgreSQL keeps its advisory lock | 200 | **high**: every write | 4 |
| 6 | The SQLite runner: two pools, the semaphore, the three writer connections, the two layers of nested-write detection | 300 | **high**: concurrency | 5 |
| 7 | The table list, central in `internal/store/tables`. Backup, restore, portable backup and dev data read it. Delete `sqliteBackupTables`, `pgCopyTables`, `portableSpec` | 1,900 deleted | medium | 3, #1485 item 2 if it changes the staging path |
| 8 | Collections and saved views | 470 | low | 3 |
| 9 | The private registry | 320 | low | 3 |
| 10 | Notification sinks and the sink lease | 430 | low | 3 |
| 11 | Git hosts | 490 | low | 3 |
| 12 | Object context | 520 | low | 3 |
| 13 | Unit imports | 440 | low | 3, #1482 |
| 14 | Gate and job glue: move `gatestore` and `jobstore` onto `sqlstore.Tx`; delete `gateTx` and `jobTx` in both dialects | 1,110 | medium | 3, #1392, #1431 |
| 15 | Drift, manual locks, maintenance, revocations, temporary administrators, IP allow lists | 1,000 | medium | 5, #1468 |
| 16 | Profiles and preferences. First, a conformance case for difference 1 | 420 | medium | 3, #1490 |
| 17 | Runners. First, a case for difference 2 | 1,050 | medium | 3 |
| 18 | Audit, the audit chain, the audit feed | 810 | **high**: the chain | 3 |
| 19 | Identity: principals, sessions, sign-in, access tokens, sign-in providers | 1,810 | medium | 3, #1465, #1486 |
| 20 | Access and reviews | 1,090 | medium | 3, #1465 |
| 21 | Inventory, lifecycle, units, the namespace page | 1,440 | medium | 3, #1489 |
| 22 | Durable operations, schedules, heartbeats, host recovery, idempotency | 1,120 | **high**: two mechanisms | 3, 6 |
| 23 | Workspaces, blame, state at | 1,240 | **high**: OpenTofu writes | 3, #1468, #1452, #1457, #1489 |
| 24 | Stack updates, the journal, item keys, state at | 1,460 | **high**: the Pulumi CLI hot path | 3, #1435, #1450, #1454, #1458, #1489 |
| 25 | Backup and restore engines: the shared order of the steps in `sqlstore`, the engines stay in each dialect | 1,500 | **high** | 6, 7, #1485 |
| 26 | Delete the empty helpers. Update `CLAUDE.md`, `GLOSSARY.md` and `.golangci.yml` | small | low | all |

A third engine adds an ongoing cost to CI. It needs an additional real-server
conformance job. The current CI starts real database servers as job services:
for example, a PostgreSQL job starts a `postgres:18` image
(`.github/workflows/ci.yml:183-188`). The new engine adds an additional job of
this kind. That job needs runner and server resources.

The order puts the core first (1 to 7), because each area needs it. Step 3
changes no behavior: it moves the checks into one function, and each dialect
keeps its own gate. The gate change is step 5, after the conformance case of
step 4 that proves it. Then the
areas go from low risk and high duplication to high risk. The stack and
workspace paths go late, because the tool CLIs depend on them and many open
pull requests change them. Each step from 8 on is independent of the others,
so two authors can work in parallel on two areas.

### The in-flight work

- **Keyset paging, #1418.** #1418 has a draft pull request, #1489. It
  changes the unit list, the TFE workspace list and the stack list of the
  Pulumi CLI. Proposal: #1489 lands in the current layout, because it fixes a
  measured problem for users now (1.3 s for each page at 100,000 units).
  Steps 21 (units), 23 (workspaces) and 24 (stacks) wait for #1489. If #1489
  is still open after step 2, it can use the page token helper of `sqlstore`
  once, in place of a copy for each dialect.
- **Template databases, #1485.** #1485 is an issue with no pull request yet.
  Items 1, 3 and 4 change the test setup of `pkg/store/postgres`
  (`TestMain`, `isolatedURL`) and the CI jobs. No step of this plan changes
  them. Item 2 changes the staging path of a restore, which steps 7 and 25
  change too, so steps 7 and 25 wait for item 2, or go first and tell its
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
  call sites. The shared interface has about 500 methods. With one
  generated interface for each area (open question 7), an area package sees
  only its own queries and the core queries. The array and `RETURNING` queries stay hand-written.

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

C, with one generated interface for each area (open question 7): the
generator writes `q.Querier` and, from a section marker in `queries.sql`, a
narrow interface for each area (for example `q.StackQueries`). An area
package sees only its own interface and the shared core queries, so a test can
fake it, and `workspacestore` cannot call a stack query. D can come later for a query that stays the same in every dialect,
if the owner wants it, without a change to the layout.

## Open questions for the owner

Each question stands alone. Each gives the options, the recommendation and
the reason in one line.

### 1. How do the dialects share the query code?

- Options: (A) a generated shared `Querier` and a conversion shim for each
  dialect (option C). (B) a hand-written `Tx` interface for each area, with
  hand-written glue in each dialect (option B).
- Recommendation: A, the generated `Querier`.
- Reason: hand-written glue stays a copy in each dialect (`gate.go` today:
  583 lines, 97 % the same in the two dialects).

### 2. When does this work start?

- Options: (A) after step 8 of RFC 103, which moves the store adapters to
  `internal/store/`. (B) now, in `pkg/store/`.
- Recommendation: A, after step 8 of RFC 103.
- Reason: step 8 is a scripted move, and each step of this plan would
  conflict with it.

### 3. How does SQLite make sure that only one write runs at a time?

- Options: (A) two pools, a write pool of one connection and a read pool,
  with a Go semaphore for the writer. (B) one pool of one connection for
  reads and writes.
- Recommendation: A, two pools and a semaphore.
- Reason: with one connection, each read of the console waits behind each
  checkpoint write of the Pulumi CLI.

### 4. How does each dialect hold the maintenance gate?

- Options: (A) a primitive of each dialect's `Runner` with one shared
  contract and one conformance case: PostgreSQL keeps its advisory lock, and
  MySQL uses a row lock on the `installation` row. (B) a row lock on the
  `installation` row (`FOR SHARE` for a write, `FOR UPDATE` to enter) on
  every dialect with row locks, PostgreSQL included.
- Recommendation: A, a `Runner` primitive, in its own step after a
  conformance case.
- Reason: on PostgreSQL, a share lock on one row for every write makes a hot
  row and MultiXact churn, and the advisory lock has neither.

### 5. Where does the list of the built-in tables live?

- Options: (A) `internal/store/tables`, out of the SDK, with every
  declaration in that one package and the format types kept in
  `pkg/backup`. (B) `pkg/backup`, as today. (C) each area package declares
  its own tables.
- Recommendation: A, central in `internal/store/tables`.
- Reason: the built-in schema is not a port under ADR 272, and one central
  list exists before the area packages do and is one place to review.

### 6. Can a third dialect live outside this repository?

- Options: (A) in-tree only: a new dialect is a package in this
  repository, and `sqlstore` stays internal. (B) `sqlstore`, `q` and the
  area packages become SDK later, so that a dialect in another repository
  gets the shared logic.
- Recommendation: A, in-tree only, for now.
- Reason: under ADR 272, `sqlstore` is internal, and to make it SDK would
  freeze about 500 query methods and the `Runner` as a public contract
  before a third dialect proves them.

### 7. One shared `Querier`, or one for each area?

- Options: (A) one generated `q.Querier` with about 500 methods, and each
  area declares a narrow interface by hand. (B) one generated interface for
  each area, from a section marker or a name prefix in `queries.sql`.
- Recommendation: B, one generated interface for each area.
- Reason: with one interface, `workspacestore` can call the stack queries,
  which weakens the separation of the tool models (ADR 12 and ADR 13), and
  the generator can enforce that separation at no cost to the areas.

## Outcome

The owner accepted answer A to questions 1 to 6 and answer B to question 7 on
22 October 2026. [ADR 274](../adr/0274-dialect-neutral-store-logic.md) records
them.
