## Context

The counter port exists with an in-memory reference and a conformance suite. The SQL adapters for SQLite and PostgreSQL are missing, so submits cannot enforce units and requests limits in production stores.

## Decisions

1. **SQL counter inside the guarded transaction.** Add quota tables to the baseline of each dialect in name order, with SQL queries in name order in queries.sql, and generate with sqlc. The SQL counter is the default counter inside the store adapter that also holds the self-service records, so Reserve and Record run in the transaction of catalog.Store.Submit. Each key row is locked with SELECT FOR UPDATE after INSERT ON CONFLICT DO NOTHING for a missing key, then one conditional UPDATE with a WHERE count plus one below max guard. Keys run in sorted key order. A PostgreSQL 40001 or SQLite SQLITE_BUSY serialization failure retries once inside the transaction and never surfaces as quota.ErrExceeded.
2. **Inline expiry without a separate cleanup process.** Expiry is read at use: Reserve and Record delete rows past endsAt in the same transaction before they count, and ignore them when counting the requests window. There is no separate cleanup process in this change; the hourly quota.reconcile of decision 9 stays out of scope and later repairs any leftover row from inventory.
3. **Stable IDs in stored keys.** Stored key columns hold stable IDs: the namespace ID and the blueprint ID with kind and scope value. The display form of decision 3 with names stays a label for errors only.

## Structure

Unexported type sqlCounter with constructor newSQLCounter(store, clock) that only builds valid keys and statements; the transaction object checks the fence before each Reserve and Record.

## Risks

- Concurrent Reserve calls exceed max when the check and the insert are separate statements.
- Expired reservations keep counting when expiry is lazy only.
- Concurrent multi-key reserves deadlock when keys lock in different orders.
