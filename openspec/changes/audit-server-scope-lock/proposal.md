---
version: 2
---

Short form: bug, security code. It covers #3116.

## Problem

**Persona: operator.** Two writes at the same time that each append an audit
entry of the scope `@server` can both read the same last entry of the hash
chain. One insert then conflicts, and the request fails closed with 500. A
person who changes a sign-in provider while another person signs in with a
passkey can get this 500. The PostgreSQL store takes the lock that orders the
`@server` appends in each caller, and some callers do not take it.

## Evidence

- `appendAuditEntry` (pkg/store/postgres/audit.go:239) says: "The caller runs
  it in a transaction that orders the appends of the scope." It takes no lock
  itself.
- The lock is `LockServerAuditScope` (pkg/store/postgres/queries.sql, the
  generated `func (q *Queries) LockServerAuditScope` at
  pkg/store/postgres/internal/db/queries.sql.go:18810): `SELECT
  pg_advisory_xact_lock(418581342580)`. A transaction advisory lock that the
  same transaction takes again is granted at once, and all of its holds end at
  the commit or the rollback.
- Callers that take the lock: `AppendServer` (pkg/store/postgres/audit.go:66),
  accesstokens.go:301, access.go:412, preferences.go:118, settings.go:198 and
  :261, hostrecovery.go:44, breakglass.go:422, restore_operation.go:485.
- Callers that do not take it, with an entry of `@server`:
  `CreateProviderConfig` (pkg/store/postgres/providers.go:101),
  `UpdateProviderConfig` (:181), `DisableProviderConfig` (:238),
  `DeleteProviderConfig` (:280), all through `appendProviderEntry` (:326);
  `RecordUse` (pkg/store/postgres/passkeys.go:43, after
  `validServerEntry`); `BumpGeneration`
  (pkg/store/postgres/principalstate.go:140, the append at :91). The name
  locks got the lock one caller at a time; the class stays open for each new
  caller.
- SQLite: `inWriteTx` (pkg/store/sqlite/durable.go:689) runs each write in an
  IMMEDIATE transaction (`_txlock=immediate`), which takes the write lock of
  the database at the start. Two appends cannot interleave, so the SQLite
  store has no such race. `appendAuditEntry` (pkg/store/sqlite/audit.go:188)
  stays as it is.

## Fix boundary

The first design (version 1) made `appendAuditEntry` take the `@server` lock itself. F4's review of its implementation (#3136) found that this gives two lock orders, and the PM changed the design on 15 November 2026 (rule 16; benchmark: one global lock order, lock before row, is the deadlock-avoidance practice of the PostgreSQL documentation, "Deadlocks", and of Google and AWS engineering guidance):

- Facts on main. `inAuditedTx` (pkg/store/postgres/audit.go:93-128) takes `LockServerAuditScope` as its first statement, and its comment says "Do not move it after fn". `RemovePasskey` (pkg/store/postgres/passkeys.go:76) and `AddPasskey` (:21) use it: lock, then the passkey row. `RecordUse` (:43-62) updates the passkey row (`UPDATE passkeys SET sign_count`, pkg/store/postgres/queries.sql:5865) and then appends. `BumpGeneration` (pkg/store/postgres/principalstate.go:140-163) updates `principal_session_generations` (queries.sql:5767) and then appends. With a lock inside `appendAuditEntry`, these two take the row first and the lock second. A `RecordUse` that holds a passkey row and waits for the lock, and a `RemovePasskey` of the same passkey that holds the lock and waits for the row, deadlock (SQLSTATE 40P01, which fails closed as a 500).
- One lock order: an `@server` append runs only inside a transaction that took the `@server` lock as its first statement. The NEW unexported type `serverAuditTx` (pkg/store/postgres) holds the queries of such a transaction, and only `inAuditedTx` and the NEW helper `inServerAuditTx(ctx, fn func(tx serverAuditTx) error)` construct it, each with the lock first. The NEW function `appendServerEntry(ctx, tx serverAuditTx, e)` is the one way to append an entry of `audit.ServerScope`; `appendAuditEntry` refuses an entry of that scope with `audit.ErrInvalidEntry` (a defense-in-depth layer: the type is the first layer). A writer that did not lock first has no `serverAuditTx`, so its append does not compile.
- Each writer of an `@server` entry moves onto `inServerAuditTx` or `inAuditedTx`: the provider config writers (pkg/store/postgres/providers.go:35, :107, :187, through `appendProviderEntry` at :326), `BumpGeneration`, and the writers that lock by hand today (`rg -n LockServerAuditScope pkg/store/postgres --glob '!*_test.go' --glob '!pkg/store/postgres/internal/db/*'`: audit.go, accesstokens.go, access.go, preferences.go, settings.go, hostrecovery.go, breakglass.go, restore_operation.go, audit_chain.go, namelocks.go, principals.go, group_claims.go, maintenance_revocations.go). Their order does not change: the lock stays first.
- `RecordUse` keeps the passkey sign-in free of the installation-wide lock. Transaction 1 updates the counter with no lock. When it finds a regression, it rolls back, and transaction 2 runs in `inServerAuditTx`: the lock, then the suspension of the passkey and its entry. So only the regression path takes the lock, and it takes it first.
- A namespace scope keeps its row lock of today. No change to the hash chain or to the entry.
- SQLite: each write transaction is IMMEDIATE (`inWriteTx`, pkg/store/sqlite/durable.go:689, `_txlock=immediate`), so the database write lock is taken at the start and no lock-order inversion is possible. `appendAuditEntry` of the SQLite store (pkg/store/sqlite/audit.go:188) does not change; a test proves the parallel writers.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter (type) | Only `inAuditedTx` and `inServerAuditTx` construct `serverAuditTx`, with the lock first; `appendServerEntry` needs it. | `TestServerAppendNeedsLockedTx` (NEW, pkg/store/postgres): a table over every function of the package that appends an entry of `audit.ServerScope` (found with go/ast in the test) fails when one builds the append outside the two helpers. Mutation: call `appendAuditEntry` with a `@server` entry in a plain `inTx`; the test fails. |
| port adapter (runtime) | `appendAuditEntry` refuses an entry of `audit.ServerScope` with `audit.ErrInvalidEntry`. | `TestAppendAuditEntryRefusesServerScope` (NEW): a direct call with a `@server` entry stores nothing and returns the error. |
| port adapter (order) | Lock before row on each `@server` writer; `RecordUse` locks only on the regression path, first. | `TestServerAuditNoDeadlock` (NEW): 50 rounds of `RecordUse` with a regression against `RemovePasskey` of the same passkey, and of `BumpGeneration` against a lock-first writer that touches the same principal, in parallel on PostgreSQL; no error wraps SQLSTATE 40P01. Mutation: move the lock of `inServerAuditTx` after fn; the test fails with 40P01. |
| store (chain) | Parallel `@server` writers all succeed, and the chain checks. | `TestServerAuditConcurrentWriters` (NEW): one case for each writer family, 16 parallel writers; `audit.CheckScopes` (pkg/audit/chain.go:624) over `audit.ServerScope` finds no break. |
| SQLite store | IMMEDIATE write transactions serialize the appends. | `TestServerAuditConcurrentWritersSQLite` (NEW, pkg/store/sqlite): the same 16 parallel provider writes all succeed, and the chain checks. |

## Check

`go test -race ./pkg/store/sqlite/... -run ServerAudit` passes, and
`DUSTPAN_TEST_POSTGRES_URL=<url> go test -race -timeout 60m ./pkg/store/postgres/ -run 'ServerAudit|AppendAuditEntry|ServerAppendNeedsLockedTx'`
passes, with no SQLSTATE 40P01 in `TestServerAuditNoDeadlock`.

## ADR check

**ADR 250: defense in depth** (the rule of security code in CLAUDE.md): each
layer stops its case alone: the type makes an unlocked append fail to
compile, the runtime refusal stops a caller that reaches `appendAuditEntry`
with an `@server` entry, and the one lock order stops the deadlock; each layer
has its own test. No owner-tagged
decision on the audit chain changes: the order of the appends and the hash of
each entry stay the same. `rg -n "LockServerAuditScope|advisory" docs/adr`
finds no decision on where the lock is taken.

## Open questions

none. Considered: a lock in each caller (the name-lock fix); refused because
each new caller can miss it again. A retry of the conflict; refused because
it hides the race and a retry can fail again under load. A lock inside
`appendAuditEntry` (version 1 of this change); refused because it takes the
lock after the rows of `RecordUse` and `BumpGeneration` and so deadlocks
against the lock-first writers. A lock on every passkey use; refused because
each sign-in would wait on one installation-wide lock.
