---
rfc: 106
title: The start gate for the services of the SDK server, not only for its handler
status: discussing
created: 2026-10-26
---

# RFC 106. The start gate for the services of the SDK server, not only for its handler

Status: discussing. This RFC comes from issue #1944. ADR 238 decisions 14
and 15 gate the handler of `dustpan.Server` until `Start` has succeeded. The
other exported entry points of `Server` have no gate. This RFC lists every
entry point, compares four options, and recommends one. It has no code. The
decision is for the project owner.

## Problem

### What ADR 238 decided

- Decision 14: `Server.Start(ctx) error` runs the start-up steps. `Run`
  calls `Start`. An embedder that serves `Handler` calls `Start`.
- Decision 15: `Handler` answers 503 until `Start` has succeeded, except
  `GET /health/live`. The reason in the ADR: "An embedder can forget a call,
  and a forgotten check fails open. With the gate, a forgotten `Start` gives
  503 at the first request."

ADR 35 makes `pkg/dustpan` the entry point of the SDK. ADR 272 keeps it as
the SDK surface. So each exported method of `Server` is a contract with an
embedder, and the reason of decision 15 applies to each of them. Today only
the handler has the gate.

### What `Start` does, in order

From `pkg/dustpan/start.go` (`startSteps`) and `pkg/dustpan/dustpan.go` on
`main` at d0f008a:

1. `startUp`: the claim of the installation kind (ADR 237), the warning for
   dev principals, and the key check (`secret.CheckWrapper` and
   `secret.CheckInstallationWrapper`). The key check refuses a
   key-encryption key that opens less than every wrapped key.
2. `failStoppedRestores`: in a process with the api component, it lists the
   restore rows. If a row is in `checking` or `applying`, it increments the
   failover version of every namespace. Then it marks every running restore
   row failed (`FailRunningRestores`).
3. `startUpAuditChain`: the check of the audit chain.
4. `bindSignInProviders`: the binding of the sign-in providers.

Note: issue #1944 says that `Start` also marks the running backups failed.
That is not true on `main`. `startUp` writes no backup row: a running backup
is a durable operation, and its claim holder owns the row. The backup
service marks the rows of ended operations failed itself (`failEnded` in
`Work`, and `FailEndedBackups` in `Run`). The race that the issue describes
is real for restores. For backups, the risk is different (see the table).

The text of ADR 238 does not match the code here either. Decision 14 says
that `Start` runs "the running backups and restores with the fence". The code
on `main` fails only the running restores at `Start`, and fences only after a
restore that stopped. It does not fail the running backups. This RFC does not
edit ADR 238 (ADR 1 forbids edits). The ADR that follows this RFC can state
the step as the code runs it.

### Every exported entry point of `Server`

The table lists every exported method of `*dustpan.Server` and the exported
methods of each service that an accessor returns. "Safe before Start" means
that a call before a successful `Start` cannot write data, and cannot act on
data that a start-up step has not checked yet.

| Entry point | Safe before Start | Reason |
| --- | --- | --- |
| `dustpan.NewServer` | Yes | It reads no database (ADR 238, Facts). It builds the services and the handler. |
| `Server.Start` | Yes | It is the gate. It runs the steps once, under a mutex. |
| `Server.Run` | Yes | It calls `Start` first and returns when `Start` fails. |
| `Server.Handler` | Yes | The value is gated: each route answers 503 until `Start` has succeeded, except `GET /health/live`. An embedder can mount it before `Start`. |
| `Server.Metrics` | Yes | It returns counters in memory. `Write` reads them. It writes no data and reads no row. |
| `Server.Workspaces` | No | It returns the `*workspace.Service`. Its write methods (`Create`, `Lock`, `BeginState`, `Upload`, `WriteState`, `Restore`, `Delete`, `PurgeDeleted`, `SetOverrides` and the other lock methods) seal state with data keys that the key check has not checked. With a wrong key-encryption key, a write to a new scope can wrap a new data key with that key, before `Start` refuses the key. The writes also can land in a database of the other installation kind before the kind claim refuses it, and can append audit entries before the chain check. Its read methods (`Get`, `List`, `Query`, `Current`, `Open`, `History`, `ReadState`, `ReadResources`, `DiffStates` and others) write nothing, but they can open state with a key that the key check would refuse. |
| `Server.Backups` | No | It returns the `*backup.Service`, or nil. `Work` starts the durable runner and the `failEnded` loop. It can take over a backup operation from the last process and copy data before the kind claim and the key check. On a fresh database, `readKind` fails before the kind claim, so the backup fails with a misleading error. `Start` and `Run` create a backup row and an operation before the checks. `List`, `Get`, `Check`, `ReadManifest` and `MaxSQLBytes` read only. `PreserveAfterRestore` writes. |
| `Server.Restores` | No | It returns the `*backup.RestoreService`, or nil. A `Start` of a restore before `Server.Start` first calls the admission check of the audit chain (`admitRestore` in `pkg/dustpan/auditchain.go`). That check refuses the restore with `audit.ErrChainMigrating` while a chain migration runs, and with `audit.ErrChainChangeWaits` while a change of the chain waits for an approval. When neither is true, the restore creates a row in `checking`. Then `failStoppedRestores` sees that row as a restore that stopped. It fences every namespace and marks the row failed while the restore goroutine still runs on it. This is a race of two writers on the same row. `Get`, `List`, `ReadManifest` and `Running` read only. |

The doc of `Server.Handler` tells an embedder to call `Work` on the service
that `Backups` returns. Nothing tells the embedder that this must come after
`Start`, and nothing refuses it. The e2e harness (`e2e/harness.go`,
`startServing`) does it in the correct order, but only because its author
knew the order.

### A second gap: the code of the HTTP refusal

The HTTP gate refuses with the writers of maintenance mode
(`writeRefusal` in `pkg/dustpan/maintenance.go`). So a refusal before
`Start` carries the code `maintenance.in-maintenance`. The installation is
not in maintenance mode. Under "No magic" in `CLAUDE.md`, a refusal must
name its real cause. A client, an operator or a log search cannot tell a
server that did not start from a server in maintenance mode. Each option
below can fix this with a new code, for example `server.not-started`. This
RFC counts it as part of options (a), (b) and (d).

## Options

### (a) Every service refuses with `ErrNotStarted` until `Start` succeeds

The accessors stay. Each service that `NewServer` builds gets a start check.
Each exported method that writes, or that reads data that the start-up steps
check, returns `dustpan.ErrNotStarted` (or a domain sentinel that wraps it)
until `Start` has succeeded. The HTTP adapters map it to the code
`server.not-started`.

- Safety: good at run time. A call before `Start` fails closed with a coded
  error. But the check is in each method. A new method that a contributor
  adds without the check fails open, the same failure that decision 15
  wants to stop. A test per method is the only protection.
- SDK ergonomics: no change in shape. An embedder finds the mistake at the
  first call, at run time, not at compile time.
- What breaks: no signature changes. Each guarded method gets one new error
  value. No embedder exists yet, so no embedder breaks.
- Testability: one test for each guarded method, about 40 methods in three
  services. The domain packages `pkg/workspace` and `pkg/backup` must take
  a start check in their `Config`. That is a concept of the server in a
  domain package. It can be tool-neutral (a `func(context.Context) error`,
  as `backup.RestoreConfig.Admit` is today), so ADR 12 allows it.

### (b) The accessors exist only on the value that `Start` returns

`Start` changes to `Start(ctx) (*Started, error)`. The type `Started` (the
name is open) has the methods `Workspaces`, `Backups` and `Restores`. They
move from `Server` to `Started`. `Server` keeps `Start`, `Run`, `Handler`
and `Metrics`. A `Start` that fails returns nil and the error. A second
`Start` after a success returns the same value.

- Safety: the compiler enforces the order. An embedder cannot get a service
  without a successful `Start`, so `Backups().Work` before `Start` cannot be
  written. No method of a service needs its own check, so a new method
  cannot fail open. One gap stays: code inside `pkg/dustpan` holds the
  services directly, so a future internal caller (for example a new
  background loop that `NewServer` starts) is not stopped by the type.
- SDK ergonomics: one more value to keep. An embedder writes
  `started, err := srv.Start(ctx)` and then `started.Backups().Work(ctx)`.
  The order is visible in the code of the embedder.
- What breaks: the signature of `Start`, and the three accessors move. No
  embedder exists. These call sites in this repository change, on `main` at
  d0f008a:
  - 31 calls of the accessors in 11 test files of `pkg/dustpan`. Search:
    `rg -n '\.(Workspaces|Backups|Restores)\(\)' pkg/dustpan -g '*_test.go'`.
  - 6 calls of `Server.Start` in 3 test files of `pkg/dustpan`
    (`started_test.go`, `signin_wiring_internal_test.go` and 4 in
    `start_test.go`). Most tests start the server through the helper
    `startedHandler` in `started_test.go`, so its one call covers them.
    Search: `rg -n '\bStart\(' pkg/dustpan -g '*_test.go'`, then only the
    calls on a `*dustpan.Server`.
  - `e2e/harness.go`: one call of `Start` (`startServing`) and one call of
    `Backups` (`startBackupWorker`). `load/main.go`: one call of `Start`.

  The search `rg 'Start\(' pkg/dustpan -g '*_test.go'`, without the calls of
  `StartUpdate`, `cmd.Start`, `ts.Start` and `Start()`, gives 28 lines in 13
  files. That count is larger, because it also matches names that only
  contain `Start(`: test names such as `TestHandlerServesAfterStart`,
  `CheckStart`, `AdmitStart`, `ManualLockPhaseStart`, and `Start` on other
  types (the durable operation store, the session service). Those lines do
  not change. The doc of `Handler` changes. The surface check of ADR 272
  sees the change.
- Testability: few tests. The compiler covers the order. Tests cover a
  failed `Start` (nil value), a repeated `Start` (same value) and the
  services on the value.

### (c) Document the order only

The doc of `Start`, `Handler`, `Workspaces`, `Backups` and `Restores` says
that `Start` must succeed first. The code does not change.

- Safety: none. A forgotten or late `Start` fails open, with the race on the
  restore row and the writes before the key check. This is the case that
  ADR 238 decision 15 rejected for the handler.
- SDK ergonomics: no change.
- What breaks: nothing.
- Testability: nothing to test, so no test can catch a wrong order.
- "No magic": a call in the wrong order gives no refusal and no record.

### (d) Mixed: (b) as the first layer, a start check in the services as the second

This option takes (b), and adds two parts:

1. A start check, as a second layer, on the write entry points that a
   start-up step protects: `workspace.Service` writes, `backup.Service`
   `Start`, `Run`, `Work` and `PreserveAfterRestore`, and
   `backup.RestoreService` `Start`. `NewServer` gives each service the same
   tool-neutral check (`func(context.Context) error`) in its `Config`. The
   check returns `ErrNotStarted` until `Start` has succeeded. The restore
   service already has this hook (`RestoreConfig.Admit`, which the audit
   chain uses today), so the restore check adds to it. `Work` returns at
   once with a logged refusal when the check fails, because it has no error
   result. The read methods get no check: the first layer covers them, and
   a read writes no data.
2. A coded refusal: the code `server.not-started` in `internal/errcode`, for
   the HTTP gate in each error shape and for `ErrNotStarted` in each adapter.
   The HTTP gate stops using `maintenance.in-maintenance`. Each refusal of the
   second layer writes a log record with the code and the method, so that an
   operator can see it.

- Safety: two layers, as ADR 250 asks for this code (keys, backups and
  restore are security code). The type stops an embedder. The check stops a
  future internal caller that the type does not reach. Each layer stops the
  call alone.
- SDK ergonomics: the same as (b). An embedder never sees the second layer
  when the order is correct.
- What breaks: the same as (b). Also `workspace.Config` and `backup.Config`
  get one optional field. A nil check means "no check", as `Admit` does
  today, so the conformance suites and the package tests keep their setup.
  `NewServer` always sets it.
- Testability: the tests of (b), plus one test for each guarded write method
  that calls it before `Start` from inside the package, as if the type layer
  had failed. That is about 20 methods, fewer than (a), because reads have
  no check.

### Comparison

| | (a) check in each service | (b) value of `Start` | (c) doc only | (d) (b) plus a check on writes |
| --- | --- | --- | --- | --- |
| A forgotten `Start` | Refused at run time | Does not compile | Fails open | Does not compile, and refused at run time |
| A new method without a check | Fails open | Covered by the type | Fails open | Covered by the type; a write also needs the check |
| A future internal caller | Refused | Not covered | Not covered | Refused for writes |
| Layers (ADR 250) | One | One | None | Two |
| Signature changes | None | `Start`, three accessors | None | `Start`, three accessors, two `Config` fields |
| Call sites that change in this repository | None outside the services | 31 accessor calls and 6 `Start` calls in tests, 2 in `e2e`, 1 in `load` | None | The same as (b) |
| Tests | About 40, one per method | About 3 | None | About 3 plus about 20 |
| Coded refusal | Yes | Compile error only, plus the HTTP code | No | Yes |

## Proposal

The author recommends option (d).

- Option (c) keeps the failure open that ADR 238 decision 15 closed for the
  handler. It does not meet "No magic": a wrong order gives no refusal and
  no record.
- Option (a) has one layer, and that layer is in each method. A method that a
  contributor adds without the check fails open.
- Option (b) makes the order a property of the types, which is the
  strongest single layer. But it is one layer, and ADR 250 asks for two in
  code that handles keys, backups and restore.
- Option (d) keeps the ergonomics of (b) and adds a second layer only where a
  start-up step protects a write. It also fixes the code of the HTTP refusal.
  No embedder exists, so the breaking change costs only the call sites in
  this repository.

### Tests that (d) needs

In `pkg/dustpan` (SDK tests, black-box unless noted):

- `TestStartReturnsServices`: after a successful `Start`, the value has the
  workspace service, and the backup and restore services when a backup
  target is set (nil when not).
- `TestStartFailedReturnsNoValue`: a `Start` that fails (for example a wrong
  key) returns nil and the error. A second `Start` after the fix returns a
  value.
- `TestStartTwiceReturnsSameValue`: two calls, and calls from several
  goroutines, return the same value.
- `TestServicesRefuseBeforeStart` (internal test, the second layer): for
  each guarded write method of the three services, a call before `Start`
  returns an error that `errors.Is` matches with `ErrNotStarted`, and writes
  no row. Table-driven, one row for each method.
- `TestWorkBeforeStartClaimsNothing` (internal test): `Work` before `Start`
  returns, claims no operation and logs the refusal with the code.
- `TestRestoreBeforeStartIsNotFailedByStart` (internal test): a restore
  that the type layer let through is refused, so `failStoppedRestores` finds
  no new row and fences no namespace.
- `TestHandlerRefusesBeforeStart` (exists in `start_test.go`): it also
  checks the code `server.not-started` in each error shape (Pulumi,
  inventory, TFE, HTTP backend, runner), and that no response carries
  `maintenance.in-maintenance`.
- Each new test package has its `leak_test.go` with `goleak`.

In the conformance suites:

- `pkg/workspace` and `pkg/backup` package tests: a service with a start
  check that refuses gives `ErrNotStarted` from each guarded method and
  writes nothing. A nil check admits every call. These are package tests,
  not port conformance tests: the check is a `Config` field of the service,
  not a port.
- `internal/errcode`: the catalog test covers the new code
  `server.not-started` and its meaning.
- The ADR 272 surface check lists the new type and the new `Start`
  signature.

### What changes in ADR 238

If the owner accepts (d), a new ADR supersedes in part ADR 238 decisions 14
and 15:

- Decision 14: `Server.Start(ctx)` returns a value and an error. The
  workspace, backup and restore services are available only from that
  value. An embedder that serves `Handler` calls `Start` and calls `Work` on
  the backup service of that value.
- Decision 15: the gate covers the services too, as a second layer on their
  writes. Each refusal before `Start`, on HTTP and in the services, carries
  the code `server.not-started`.

Decision 13 (the container image) does not change. ADR 35 does not change.
ADR 272 does not change in its decisions; its surface list changes with the
code.

## Open questions

1. The name of the value that `Start` returns: `Started`, `Running` or
   another name. `GLOSSARY.md` gets the term.
2. Should `Handler` and `Metrics` also move to the value? This RFC says no:
   the handler is gated and an embedder may mount it before `Start`, and
   the metrics write no data. Moving them would make the order stricter but
   would stop a readiness probe from being served before `Start`.
3. Should the read methods of the services get the second-layer check too?
   This RFC says no, because a read writes no data. A read with a wrong key
   can fail when it opens the data key. The owner may prefer that every method
   is refused, for one simple rule.
4. Should `Work` refuse by a log record only, or should it get an error
   result (`Work(ctx) error`)? An error result is easier to test and to see,
   and costs one more signature change.

## Checklist

- [x] Tolerates asynchronous replication between installations: each
  namespace writes only its own half, under its own fence, and no write
  waits for another installation (ADR 248 decisions 3, 6 and 9). No option
  adds a write. Option (d) refuses writes before `Start`, so it removes the
  fence of `failStoppedRestores` that a restore started before `Start` can
  trigger.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
