## Context

Part 3 of the namespace states split. Parts 1 and 2 provide the deleted state, the filter, the operator routes and the timed steps. This part removes deleted namespaces after 30 days in bounded batches and documents the full lifecycle.

## Decisions

1. **Batched purge task.** Run the purge as a scheduled server task that selects a bounded batch of namespaces in state deleted with delete time older than 30 days, ordered by delete time, rechecks state and delete time inside the purge transaction, then purges each one and advances the stored cursor.
2. **Per-namespace commit with audit.** Purge one namespace at a time inside its own transaction with per-namespace commit, one audit entry per purged namespace, and single-runner fencing so two server processes never purge the same batch.

## Structure

Unexported type namespacePurger with constructor newNamespacePurger(store, clock, batchSize) that checks batch size and age threshold invariants; structure makes an unbounded or early purge impossible.

## Risks

- Purge deletes data that no restore returns after the batch commits.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| batch size | 100 | namespaces | one purge run |
| retention period | 30 | days | per deleted namespace |

## Contracts

**Namespace**

- Invariant: Purge removes only namespaces in state deleted with delete time at least 30 days past.
- Invariant: Purge never removes a namespace in state active, suspended or disabled.
- `PurgeDeletedNamespaces`: pre: Caller is the scheduled server task with installation purge permission.. Post: Only eligible deleted namespaces are purged in bounded batches..

**NamespacePurgeBatch**

- Invariant: Batch size is bounded and the cursor advances past each committed namespace.
- `PurgeNextBatch`: pre: Cursor points at or before the end of eligible rows.. Post: One bounded batch is purged and the cursor advances..
