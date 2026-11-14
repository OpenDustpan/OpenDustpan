## Context

The counter port exists with an in-memory reference and a conformance suite. The SQL adapters for SQLite and PostgreSQL are missing, so submits cannot enforce units and requests limits in backed stores.

## Decisions

1. **SQL counter with dialect guards and whole-transaction retry.** Add quota tables to the baseline of each dialect in name order, with SQL queries in name order in queries.sql, and generate with sqlc. The SQL counter exposes quota.Tx inside the store transaction that the caller opened. On PostgreSQL each key row is locked with SELECT FOR UPDATE after INSERT ON CONFLICT DO NOTHING for a missing key, then one conditional UPDATE with a WHERE count plus one below max guard. On SQLite the transaction runs with BEGIN IMMEDIATE and the same conditional UPDATE guard, with no SELECT FOR UPDATE. Keys run in sorted key order. A PostgreSQL 40001 error or SQLite SQLITE_BUSY returns a retryable store error from quota.Tx, never quota.ErrExceeded; the InTx helper retries the whole transaction once, and only a limit failure returns quota.ErrExceeded. A second serialization failure returns the retryable store error.
2. **Inline expiry and submit pruning without a separate cleanup process.** Expiry and pruning are inline in the same transaction. Reserve and Record delete reservations past endsAt for the counted keys before they count, delete requests submits older than the window for the counted keys before they count, and ignore deleted rows. Release and settle follow the errors of the port `quota.Counter` (internal/quota/counter.go:126-132), which is the contract (PM decision, 14 Nov 2026): an unknown ID returns `quota.ErrNotFound`; a release of a settled or a settle of a released reservation returns `quota.ErrEnded`; a repeat of the same end changes nothing and returns no error. The hourly quota.reconcile of decision 9 stays out of scope.
3. **Stable IDs in stored keys with exact scope value.** Stored key columns hold the namespace ID, the blueprint ID, the rule kind, and the scope value string of decision 3 such as requester:alice. The scope value is stored as given and compared exactly, so a later blueprint version with the same kind and scope value counts the same key. Names stay labels for error text only.

4. **The storage (product manager, from the port on main, 14 Nov 2026).** Four tables hold what the port `quota.Counter` and `quota.Tx` (internal/quota/counter.go) read and write. Each stored key uses the stable namespace ID and the blueprint ID (`quota.Key` holds `Namespace`, `Blueprint`, `Kind`, `Scope`); the namespace name is a label only. Times are Unix milliseconds: `INTEGER` in SQLite, `BIGINT` in PostgreSQL. The tables go in the baseline of each dialect next to the self-service tables, in name order.

   | Table | Columns | Key and indexes |
   | --- | --- | --- |
   | `quota_keys` | `namespace_id` TEXT, `blueprint_id` TEXT, `kind` TEXT CHECK (`kind` IN ('units','requests')), `scope` TEXT, `settled` INTEGER/BIGINT NOT NULL DEFAULT 0 CHECK (`settled` >= 0) | PRIMARY KEY (`namespace_id`, `blueprint_id`, `kind`, `scope`); the row that a write locks |
   | `quota_reservations` | `namespace_id` TEXT, `id` TEXT, `state` TEXT CHECK (`state` IN ('open','settled','released')), `at_ms`, `ends_at_ms` CHECK (`ends_at_ms` > `at_ms`) | PRIMARY KEY (`namespace_id`, `id`); INDEX `quota_reservations_open` (`namespace_id`, `state`, `ends_at_ms`) |
   | `quota_reservation_keys` | `namespace_id` TEXT, `reservation_id` TEXT, `blueprint_id` TEXT, `kind` TEXT, `scope` TEXT | PRIMARY KEY (`namespace_id`, `reservation_id`, `blueprint_id`, `kind`, `scope`); FOREIGN KEY (`namespace_id`, `reservation_id`) to `quota_reservations`; INDEX `quota_reservation_keys_by_key` (`namespace_id`, `blueprint_id`, `kind`, `scope`) |
   | `quota_requests` | `namespace_id` TEXT, `blueprint_id` TEXT, `kind` TEXT, `scope` TEXT, `submitted_at_ms`, `seq` INTEGER/BIGINT | PRIMARY KEY (`namespace_id`, `blueprint_id`, `kind`, `scope`, `submitted_at_ms`, `seq`) |

   The NEW queries, in name order in the quota block of `queries.sql` of each dialect (each name below is NEW): `AddQuotaSettled` (NEW) (settled + 1 for each key of a reservation, in the transaction of the settle), `CountQuotaOpenReserved` (NEW) (open reservations of one key with `ends_at_ms` > the time), `CountQuotaRequests` (NEW) (requests of one key with `submitted_at_ms` in the window), `DeleteQuotaRequestsBefore` (NEW) (the inline pruning of decision 2), `GetQuotaReservation` (NEW), `GetQuotaSettled` (NEW), `InsertQuotaKey` (NEW) (INSERT ... ON CONFLICT DO NOTHING), `InsertQuotaRequest` (NEW), `InsertQuotaReservation` (NEW), `InsertQuotaReservationKey` (NEW), `ListQuotaReservationKeys` (NEW), `LockQuotaKey` (NEW) (PostgreSQL: SELECT ... FOR UPDATE; SQLite: the write transaction holds the lock), `SetQuotaReservationEnd` (NEW), `SetQuotaReservationState` (NEW), `SetQuotaSettled` (NEW).

   The derived counter `settled` is written only in the transaction of the write that it counts: `Counter.Settle` changes the state of the reservation and adds to `settled` in one transaction, and `Counter.SetSettled` writes it in its own. The reserved count is not stored: the NEW query `CountQuotaOpenReserved` counts it from the open reservations, so it cannot drift. Each write checks the fence first, in its transaction. A backup holds the four tables (`pkg/backup/portable_tables.go`).

## Structure

Unexported type sqlCounter with constructor newSQLCounter(store, clock) that only builds valid keys and statements; the transaction object checks the fence before each Reserve and Record.

## Risks

- Concurrent Reserve calls exceed max when the check and the insert are separate statements.
- Expired reservations keep counting when expiry is lazy only.
- Concurrent multi-key reserves deadlock when keys lock in different orders.

## Diagrams

After this change:

```mermaid
sequenceDiagram
participant C as Caller
participant T as Store InTx
participant Q as quota.Tx SQL
C->>T: open transaction
T->>Q: Reserve and Record
Q-->>T: ok or ErrExceeded or retryable
T-->>C: commit or retry whole transaction once
```

States and transitions after this change:

```mermaid
stateDiagram-v2
[*] --> reserved: Reserve
reserved --> settled: settle on applied
reserved --> released: release on rejected canceled failed expired
reserved --> expired: past endsAt
settled --> [*]: destroy applied lowers settled
expired --> [*]: inline delete
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| correctness | 0 reservations above max | reservations | Reserve on SQLite and PostgreSQL adapters |
| atomicity | 0 partial submits on one failing check | submits | Record across all keys of one call |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Reserve | Two Reserve commits race on one key at max 1 | The InTx helper retries the whole transaction once on a 40001 or SQLITE_BUSY error; the loser gets quota.ErrExceeded when the limit fails, or the retryable store error when the retry also conflicts | 1 reservation stored, 0 over-limit rows |
| Record | Record finds one failing requests check after storing nothing yet | Caller fixes usage or limit, then resubmits; no automatic retry | 0 submits stored for all keys of the checks |
| guarded commit | Guarded transaction rolls back after Reserve | Caller resubmits the guarded write in a new transaction | 0 reservations and 0 submits from the rolled-back transaction |
| SetEnd | SetEnd races with expiry delete in the same key | The InTx helper retries the whole transaction once on a serialization failure | Reservation carries the last committed endsAt or is expired and ignored |
