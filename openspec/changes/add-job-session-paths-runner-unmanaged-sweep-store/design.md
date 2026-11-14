## Context

This part adds persistence only: ports in pkg/unmanaged, an in-memory reference adapter, a conformance suite and SQL adapters for SQLite and PostgreSQL. The claimer and the sweep routes ship in the last part.

## Decisions

1. **Ports and suite first.** Define FindingStore and ScanStore in pkg/unmanaged with the operations of ADR 164 decision 18 and ADR 165 decision 13. Write the conformance suite and the memory adapter before the SQL adapters.
2. **Fence in the transaction.** Each SQL write reads the namespace row with a row lock, compares the failover version, then writes, all in one transaction. A stale version, a missing namespace or a soft-deleted namespace returns namespace.ErrFenced and changes nothing. An equal version passes.
3. **Pool check.** CreateTarget and UpdateTarget read the pool in the same transaction. The store refuses a pool of another namespace, a pool with the jobs purpose and a namespace pool with the sweep purpose. Only an operator pool with the sweep purpose passes (Owner decision, 13 Nov 2026 (queue P14)).
4. **Busy check.** StartScan refuses with ErrScanBusy when the target has a sweep whose status is not succeeded, failed, canceled or expired. A unique index on the open sweep of a target makes two concurrent starts safe.
5. **Backup class.** The sweep tables are normal tables in the table list. The backup round trip covers them. They are not projection tables.

## Structure

An unexported type fencedTx wraps the SQL transaction. Only its constructor makes a value, and the constructor reads and checks the fence. The write methods take a fencedTx, so no write path can skip the fence. A rule that visibility cannot express stays in the golangci-lint depguard configuration.

## Risks

- The adapters drift from the port. The shared suite runs on all three and stops the drift.
- Two writers race on the fence. The row lock and a concurrent test cover it on PostgreSQL.
- A crash after the fence check and before the row change. The single transaction rolls back, and a test proves it with a forced error.
- The tables are missed by backup. The round trip test covers them.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | The port checks the page limit (at most 500), the Record batch (at most 1,000) and the namespace ID form before any SQL. | A table test sends a page of 501, a batch of 1,001 and a bad namespace ID straight to each adapter. Each call is refused with no SQL. |
| service | The store checks the fence and the pool (namespace, purpose, operator pool) in the same transaction, even if a later service also checks. | A table test writes with a stale fence, a missing namespace, a soft-deleted namespace, a jobs pool, a namespace sweep pool and a pool of another namespace. Each is refused and changes nothing. |
| HTTP handler | This part has no handler. The last part adds the routes and their authorization actions, and its tests cover the handler layer. | None in this part. The conformance suite sends hostile input straight to the store. |
