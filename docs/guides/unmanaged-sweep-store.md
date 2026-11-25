# Unmanaged sweep store

This guide is for a developer who writes a new adapter of the sweep store. It shows how to run the conformance suite against the adapter and what the sweep store cases check.
This guide does not describe a route. This version of the sweep store has no route, so a user cannot reach it through the API yet.

## What the sweep store is

The sweep store is the port `unmanaged.ScanStore` in `internal/unmanaged/ports.go`. It keeps the sweep targets of a namespace and the sweeps that run on them. A sweep runs on a runner pool of the purpose `sweeps`.

The built-in SQLite and PostgreSQL adapters implement the port. An adapter that you write must pass the same suite.

## Run the suite

The suite is the package `internal/unmanaged/unmanagedtest`. It has three entry points. Each takes a `testing.T` and a function that returns a fresh `unmanagedtest.Harness` for one subtest:

- `unmanagedtest.Run` runs all cases. Use it when the adapter holds the resource index, the links, the finding store and the sweep store.
- `unmanagedtest.RunSweepStore` runs the cases of the findings and of the sweeps. Use it when the adapter holds the finding store and the sweep store. Leave `Index` and `Links` nil.
- `unmanagedtest.RunScanStore` runs the cases of the sweeps only. Use it when the adapter holds the sweep store only. Leave `Index`, `Links` and `Findings` nil.

Set these fields of the harness for the sweep store cases:

- `Scans` is the adapter.
- `Namespace` makes the namespace that the test names. It also makes a runner pool of the purpose `sweeps` (`unmanagedtest.Pool`) and a runner pool of the purpose `jobs` (`unmanagedtest.JobPool`) in it. It returns the current fence of the namespace.
- `Pool` makes a runner pool of a given purpose in a namespace that exists.
- `Failover` raises the failover version of a namespace and returns the new fence. The suite calls it from another goroutine, so it returns an error and does not stop the test.
- `SetPool` changes the purpose of a pool, or deletes the pool when the purpose is empty. This is the state that a restore of another version can leave. When the adapter keeps pools under a foreign key, pass the key in the test schema. The SQLite harness turns the key checks off on one connection, and the PostgreSQL harness drops the key.
- `PurgeNamespace` deletes a namespace, as the purge of a namespace does. Leave it nil when the adapter has no purge.
- `RenameNamespace` changes the name of a namespace and keeps its ID.
- `FailAudit` makes each later write of an audit entry fail inside the write transaction. The SQLite adapter adds a database trigger for this.

The SQLite adapter shows a complete harness in `internal/store/sqlite/unmanaged_test.go`. Run its suite with this command from the root of the repository:

```sh
go test -count=1 -v -run TestUnmanagedScanStoreConformance ./internal/store/sqlite/
```

When the suite passes, the output holds the line `--- PASS: TestUnmanagedScanStoreConformance`.

For your adapter, call the entry point from a test of your package, and give `go test` the path of that package. A run of a SQL adapter can take more than the default time limit of 10 minutes, so add `-timeout 60m`.

## What the cases check

- Fence: a write with a stale fence, or in a namespace that has no row, fails with `namespace.ErrFenced` and changes nothing. A write in one namespace keeps the targets and the open sweep of another namespace. This is also true when the two namespaces have a target of the same name.
- Malformed input: before SQL, `DeleteTarget` refuses a malformed namespace or target name with `unmanaged.ErrInvalidTarget`. `StartScan` refuses an unknown kind, a state check with a target, a malformed starter or namespace name with `unmanaged.ErrInvalidScan`, and a malformed target name with `unmanaged.ErrInvalidTarget`. `AdvanceScan` refuses a malformed sweep ID, an unknown event or a malformed namespace name with `unmanaged.ErrInvalidScan`. A refused call leaves no sweep and does not change a status.
- Read of another namespace: the read of a sweep by its ID under the name of another namespace fails with `unmanaged.ErrNotFound`.
- Pool purpose: a target refuses four kinds of pool. These are a pool of the purpose `jobs`, a pool of another namespace, a pool that does not exist, and a malformed name. These cases fail with `unmanaged.ErrInvalidTarget`.
- Pool at start: when you set `SetPool`, a start fails with `unmanaged.ErrInvalidTarget` and leaves no sweep when the pool changed purpose after the save. A start also fails and leaves no sweep when someone deleted the pool after the save. The error is `unmanaged.ErrInvalidTarget` or `unmanaged.ErrNotFound`.
- One open sweep: a target has at most one open sweep. A second start fails with `unmanaged.ErrScanBusy`. Of 8 concurrent starts, exactly one succeeds. A target accepts a new start after its sweep ended with the status `succeeded`, `failed`, `canceled` or `expired`. A dispatched sweep that gets the `expire` event has the status `expired`.
- Transitions: a sweep moves only along the allowed status changes. An event that does not fit, or an event after the sweep ended, fails with `unmanaged.ErrTransition`.
- Delete: a duplicate target fails with `unmanaged.ErrAlreadyExists`. A delete of a target cancels its open sweep. A delete of a row with a malformed name, for example a row that a restore brought in, fails with `unmanaged.ErrInvalidTarget` and the row stays. This version has no other way to delete such a row.
- Bounds: a page of sweeps keeps its size limit. When you set `Findings`, a batch of more than 1000 observations fails with `unmanaged.ErrLimit` and stores nothing. The SQLite and PostgreSQL adapters have no finding store yet, so this case runs only on an adapter that has one.
- Concurrent fence bump: when you set `Failover`, a failover runs at the same time as 8 writes. Each write succeeds or fails with `namespace.ErrFenced`. After the bump, a write with the old fence fails, a write with the new fence succeeds, and a refused write leaves no row.
- Rollback: a write that the store refuses leaves no row and no sweep. When you set `FailAudit`, a delete and a create fail after they changed rows, and the target and its open sweep stay as before.
- Rename: when you set `RenameNamespace`, the case renames a namespace. Its targets and sweeps stay readable under the new name, because the rows keep the namespace ID.

The SQLite and PostgreSQL packages also have the test `TestUnmanagedSQLTextStaysData`. It sends SQL text as a namespace name, a target name and a pool name to the adapter. The writes refuse each name with `unmanaged.ErrInvalidTarget` before SQL. The reads that bind the text find no row of another name. A row that has the text as its name, and that the test writes with a placeholder, stays one row, and no table changes. The port has no path that sends a pool name to SQL without a check. So the test `TestSweepPoolIDKeepsSQLTextAsData` calls the pool lookup of the adapter directly with the text. Only the pool that has that exact name matches. Write the same tests for a new SQL adapter. Use the names of `unmanagedtest.HostileSQLText`.

## What the SQL adapters do not cover

- Soft-deleted namespace: the store has no soft delete of a namespace. The fence case covers a namespace with no row and a purged namespace only.
- Finding bounds: `RunScanStore` does not run the cases of the findings, and the SQLite and PostgreSQL adapters have no finding store yet. The 1,000-observation bound of `Record` runs only on the memory adapter of the suite. A later release adds the SQL finding store, and the suite then runs this case on SQLite and PostgreSQL.
- Finding action: the cases of `ErrStatusChanged` and of the audit entry in the transaction of an action are finding cases. They do not run on the SQL adapters for the same reason.
- Audit of a refused write: the store returns `namespace.ErrFenced` and writes no audit entry. The service audits the refusal. A later release adds that service.
