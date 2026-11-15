---
version: 1
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

- `appendAuditEntry` of the PostgreSQL store calls `q.LockServerAuditScope`
  first when `e.Namespace == audit.ServerScope`, before `readChainRecord`.
  The lock goes to the one function that each append passes through, so no
  caller can miss it.
- The callers that already take the lock keep their call, with a comment that
  it is a defense-in-depth layer: the lock must be held before the caller
  reads the state that it then writes, and `appendAuditEntry` takes it again
  at no cost.
- A namespace scope keeps its row lock of today. No change to the hash
  chain, to the entry or to the SQLite store, except one test.
- No new error code: a lock error is returned wrapped, and the transaction
  rolls back (fail closed).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter (append) | `appendAuditEntry` takes the `@server` lock for each entry of that scope. | `TestAppendAuditEntryLocksServerScope` (NEW): a transaction appends a `@server` entry without a caller lock; a second connection that tries `pg_try_advisory_xact_lock(418581342580)` gets false until the first commits. Mutation: remove the lock call; the second connection gets true and the test fails. |
| port adapter (callers) | Each caller that reads then writes under the lock keeps its own lock call. | `TestServerAuditConcurrentWriters` (NEW), one case for each caller family, with 16 parallel writers: provider configs, passkey use with a counter regression, generation bump, name locks, access tokens. Each write succeeds, and `audit.CheckScopes` (pkg/audit/chain.go:624) over `audit.ServerScope` finds no break. Mutation: remove the lock from `appendAuditEntry`; the provider, passkey and generation cases fail with a conflict. |
| store (fail closed) | An error of the lock rolls back the transaction, and the write stores nothing. | `TestAppendAuditEntryLockErrorStoresNothing` (NEW): a canceled context at the lock returns an error, and neither the entry nor the change of the caller is stored. |
| SQLite store | IMMEDIATE write transactions serialize the appends. | `TestServerAuditConcurrentWritersSQLite` (NEW) in pkg/store/sqlite: the same 16 parallel provider writes all succeed, and the chain checks. |

## Check

`go test -race ./pkg/store/sqlite/... -run ServerAudit` passes, and
`DUSTPAN_TEST_POSTGRES_URL=<url> go test -race -timeout 60m ./pkg/store/postgres/ -run 'ServerAudit|AppendAuditEntry'`
passes.

## ADR check

**ADR 250: defense in depth** (the rule of security code in CLAUDE.md): each
layer stops the race alone, so the callers keep their lock and
`appendAuditEntry` takes it too; each layer has its own test. No owner-tagged
decision on the audit chain changes: the order of the appends and the hash of
each entry stay the same. `rg -n "LockServerAuditScope|advisory" docs/adr`
finds no decision on where the lock is taken.

## Open questions

none. Considered: a lock in each caller (the name-lock fix); refused because
each new caller can miss it again. A retry of the conflict; refused because
it hides the race and a retry can fail again under load.
