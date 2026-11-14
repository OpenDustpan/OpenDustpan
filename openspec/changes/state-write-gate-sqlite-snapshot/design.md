## Context

The state.write gate reads deleted-unit facts from SQLite. The issue reports that separate reads under a shared lock do not give a snapshot, so a commit can land between the reads. Two small cleanups from the same review ride along: one start-up log and one rollback pattern.

## Decisions

1. **One snapshot read per call.** GetDeletedUnitFacts opens one read-only SQLite transaction at method entry with a deferred close, runs every select on that transaction, then rolls back and closes before return. The deferred close runs on success, on error, on cancelled context, and on panic before the first select, so no read transaction leaks.
2. **No atomic check-and-write.** The fact read and the gated state write stay separate steps. The snapshot removes mixed before-and-after rows inside the read, but a commit after the read can still make the decision stale. Atomic check-and-write is out of scope.
3. **Fail closed on read failure.** A failed, busy, timed-out, cancelled, or rollback-failed fact read fails closed: the gate returns the store error and refuses the write path that needed the facts. No partial facts leave the method.
4. **Bounded wait on writer lock.** A writer that holds the lock for the full read waits under a bounded busy timeout; on timeout the read fails closed with the store error. The read does only the selects for one unit and holds no transaction across calls.
5. **One fixed start-up log line.** Log one INFO line per server start when the gate service is absent, with fixed key-value text, then keep the current no-gate path. A start with a gate service logs no such line. A log write failure never blocks start.
6. **Explicit rollback branch.** Replace the blank-assign rollback pattern with an explicit rollback-error branch that preserves the close behavior on the touched paths only.

## Structure

Unexported helper on the SQLite store that takes *sql.Tx created by the GetDeletedUnitFacts constructor path: only values built through that path run the selects, and the deferred close in that path releases the transaction, so separate reads cannot bypass the snapshot transaction.

## Risks

- A held read transaction delays a writer checkpoint; the read does only the selects for one unit under a bounded timeout and holds no transaction across calls.
- A new start-up line affects exact-text log parsers; parsers match the fixed substring key, not the full line.
