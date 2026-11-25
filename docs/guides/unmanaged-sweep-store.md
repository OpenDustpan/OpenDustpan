# Unmanaged sweep store

This guide is for a developer who writes a new adapter of the sweep store. It shows how to run the conformance suite against the adapter and what the sweep store cases check.
This guide does not describe a route. This part of the work adds no route and no credential for the sweep store, so a user cannot reach it through the API yet.

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
- `Failover` raises the failover version of a namespace and returns the new fence.
- `SetPool` changes the purpose of a pool, or deletes the pool when the purpose is empty. Leave it nil when the adapter keeps pools under a foreign key, because then no legal path reaches that state.
- `PurgeNamespace` deletes a namespace, as the purge of a namespace does. Leave it nil when the adapter has no purge.

The SQLite adapter shows a complete harness in `internal/store/sqlite/unmanaged_test.go`. Run its suite with this command from the root of the repository:

```sh
go test -count=1 -run TestUnmanagedScanStoreConformance ./internal/store/sqlite/
```

For your adapter, call the entry point from a test of your package, and give `go test` the path of that package. A run of a SQL adapter can take more than the default time limit of 10 minutes, so add `-timeout 60m`.

## What the cases check

- Fence: a write with a stale fence, or in a namespace that has no row, fails with `namespace.ErrFenced` and changes nothing.
- Pool purpose: a target refuses four kinds of pool. These are a pool of the purpose `jobs`, a pool of another namespace, a pool that does not exist, and a malformed name. These cases fail with `unmanaged.ErrInvalidTarget`. A start fails with the same error and leaves no sweep in two more cases. In the first, the pool changes purpose after the save. In the second, someone deletes the pool after the save. These two cases run only when you set `SetPool`.
- One open sweep: a target has at most one open sweep. A second start fails with `unmanaged.ErrScanBusy`. Of several concurrent starts, exactly one succeeds. A target accepts a new start after its sweep ended.
- Transitions: a sweep moves only along the allowed status changes. An event that does not fit, or an event after the sweep ended, fails with `unmanaged.ErrTransition`.
- Delete: a duplicate target fails with `unmanaged.ErrAlreadyExists`. A second delete fails with `unmanaged.ErrNotFound`. A delete of a target cancels its open sweep.
- Bounds: a page of sweeps keeps its size limit. When you set `Findings`, a batch of more than 1000 observations fails with `unmanaged.ErrLimit` and stores nothing.
- Concurrent fence bump: when you set `Failover`, a failover runs at the same time as several writes. Each write succeeds or fails with `namespace.ErrFenced`. After the bump, a write with the old fence fails, a write with the new fence succeeds, and a refused write leaves no row.
- Rollback: a write that fails after the fence check leaves no row, no audit entry and no sweep behind.
