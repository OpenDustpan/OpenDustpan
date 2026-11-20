## Context

`gate.Records.PruneExpired` exists (`pkg/gate/store.go:212`) and nothing calls it. The setting `decisionRecords.retentionHours` exists (`pkg/gate/retention_setting.go:12`). Other sweeps on main run as durable schedules: one `ScheduleSpec` in `Server.jobSchedules` and one `KindSpec` in `Server.jobKinds` (`pkg/dustpan/schedules.go:78`, `:149`). The durable `Scheduler` claims each tick with `ScheduleStore.ClaimTick`, one conditional update in one transaction, so one process runs each tick. The `Runner` holds the started operation under a lease and takes over an expired lease (`pkg/durable/runner.go`). Ticks run on the database clock. This change adds one more schedule of that kind. It adds no lease primitive.

## Decisions

1. **One durable schedule with a new Kind.** The schedule name is `decision-record-prune`, the Kind is `kindDecisionRecordPrune`, built with `jobKind`-like code beside `nameLockPruneKind` (`pkg/dustpan/schedules.go:302`). The Kind is registered by one `KindSpec` in `jobKinds` and one `ScheduleSpec` in `jobSchedules`, both only when `s.recordPrune != nil`. A duplicate Kind is refused at start (`pkg/durable/runner.go:223-234`), and the Kind list test (`pkg/dustpan/schedules_internal_test.go`) lists the new name. The `Scheduler` first delay is random up to the smaller of one minute and a tenth of the interval, so the first run follows the start by about a minute at most.
2. **Fixed hourly period.** `decisionRecordPruneEvery = time.Hour`, a constant like `deletedSweepEvery` (`pkg/dustpan/sweep.go:22`). There is no setting for the period. A period shorter than the retention floor of 24 hours has no use, and one hour makes a change of the retention take effect within the hour.
3. **Durable store only.** `startSweeps` runs a ticker on each instance when the server has no durable store (`pkg/dustpan/schedules.go:468-481`). A ticker has no lease, so two instances would run the same delete. This change registers the prune only in the durable list. A server without a durable store runs no prune, and the docs page says so. The check is in `jobSchedules`, which only the durable path calls.
4. **Namespace enumeration.** The run calls `namespace.Store.ListNamespaces` (`pkg/namespace/namespace.go:201`) once for each run. For each `ns` it uses the name and `Namespace.Fence()` (`pkg/namespace/namespace.go:173`). The state.write record of a namespace has that namespace (`pkg/gate/record.go`), and `audit.ServerScope` is the scope of installation objects only. The run never calls the method with `audit.ServerScope`, and the method refuses it as a second layer. A namespace that returns `namespace.ErrFenced` is skipped and logged. Any other error stops the run with a failed step, so the runner counts a failed attempt.
5. **Probe, then batch loop.** For a namespace, the run asks `Records.Query` for one `state.write` record with `At` in `[epoch, cutoff)`. If none, it makes no call and writes no entry, so an idle namespace costs one read each hour. Else it calls `PruneExpired` with `gate.NewPruneRequest(cutoff, gate.MaxPruneBatch)` and repeats until a batch is shorter than the batch size. Each run has a cap of `maxPruneBatchesPerRun` batches (100, so 100000 records) and a deadline of `decisionRecordPruneTimeout` (10 minutes, like `deletedSweepTimeout`). A run that stops at the cap or the deadline logs the stop reason, and the next tick continues.
6. **The cutoff.** `cutoff = now - period`, with `now` from the clock of the `Server`, the clock that writes the `At` of a record. The period comes from `gate.RetentionPeriod(item.Value)` where `item` is `setting.Service.Effective(ctx, setting.InstallationScope(), gate.RetentionKey)` (read as in `pkg/dustpan/settings_resource.go:140-175`). A parse error fails the run with no call. `keepForever` returns before the namespace list is read. `gate.NewPruneRequest` refuses a zero cutoff (`pkg/gate/prune.go:20`). A cutoff in the future cannot occur: the period is at least 24 hours (`gate.MinRetentionHours`) and the parse refuses a smaller value.
7. **The audit entry.** The entry of each call has the NEW action `decision-record.pruned`, the object `decision-records`, the scope `gate.NamespaceScope(ns.Name)`, the actor `history.ServerSubject` with the kind of service principal (`pkg/history/history.go:156`; the form of `internal/localusersvc/locks.go:356-364`), and a reason that names the period in hours. The count cannot be set before the delete, so the store adds the change `pruned` with the count in the same transaction. This follows `PruneNameLocks` (`pkg/store/sqlite/namelocks.go:240-262`, `internal/authgate/namelock.go:269`). The actor is open question 1 of the proposal.
8. **Where the code lives.** `pkg/dustpan/recordprune.go` holds the unexported `recordPruner`. The repository has no gate service package, so the pruner needs the settings, the namespace store and the clock that the server holds. A constructor makes the only valid value and returns nil for a store that is not a `gate.Store` (the form of `nameLocksOf`, `pkg/dustpan/schedules.go:344`).

## Contract

Ports and exported types that this change adds or changes:

```go
// pkg/store/internal/gatestore/tx.go: a method of Runner.
// WriteCounted runs fn in one transaction that checks fence first, appends e
// after fn with the change "pruned" set to the count that fn returned, and
// returns that count. An error of fn rolls the transaction back and appends
// nothing. It refuses a negative count.
WriteCounted(ctx context.Context, scope gate.Scope, fence namespace.Fence, e audit.Entry,
    fn func(Tx) (int64, error)) error
```

- `gate.Records.PruneExpired` keeps its signature. Its comment says that the entry carries the change `pruned` with the count of the deleted records. The count is the length of the returned IDs.
- `gate.PrunedChange(n int64) audit.Change` (NEW function, `pkg/gate`) returns the change `{Key: "pruned", New: n}`. The store uses it, so both SQL adapters write the same change.
- `recordPruner` and its constructor are unexported.

## Structure

The unexported `recordPruner` holds the gate store, the namespace store, the setting reader and the clock. Only its constructor makes a value, and it returns nil when a port is missing, so no value runs with a missing port. The run method returns an error for the step, and the step returns the JSON of the result (namespaces seen, namespaces skipped, batches, deleted, stop reason), as `nameLockPruneKind` does.

## Fail closed, layer by layer

Security code (ADR 250 and CLAUDE.md "Security code"): the prune deletes evidence. Each layer must stop a wrong delete alone.

| Layer | Guard | Hostile case that its test sends directly |
| --- | --- | --- |
| Setting read | `gate.RetentionPeriod` refuses a text that is not `0` or 24 to 87600 | `"-1"`, `"23"`, `"087600"`, `"1e3"`, `""`, a 4000-digit text: no `PruneExpired` call, the run fails |
| Setting read | The read fails when the setting service fails | A reader that returns an error: no call, the run fails |
| Request | `gate.NewPruneRequest` refuses a zero cutoff and a bad batch | Zero cutoff, batch 0, batch 1001: `gate.ErrInvalid` |
| Port | `PruneExpired` refuses `audit.ServerScope`, an empty or invalid name, a wrong fence | The existing cases of `pkg/gate/gatetest/run_records.go:617-660` stay and run for the new transaction path |
| Service | The run never gives `audit.ServerScope` | A namespace list that contains the server scope: the run makes no call for it |
| Store | The count and the entry commit with the delete | A failing append: no record is deleted (rollback) |
| Store | `WriteCounted` refuses a negative count | `-1`: error, nothing appended |
| Effect | Only point `state.write`, only records before the cutoff, only the namespace | Records at another point, at the cutoff, newer, and in another namespace stay |
| Effect | Batch cap and deadline | A backlog above the cap: the run stops with the stop reason, the next run continues |
| Fence | A fenced namespace is skipped, not pruned | A stale fence: `ErrFenced`, no delete, other namespaces still run |

Each guard has a test that fails when the guard is removed. Tasks 1.x name them. The checks that look redundant (the request check in the service and in the port, the server-scope check in both) carry the comment that they are a defense-in-depth layer.

## Risks

- A run that stops at the cap leaves a backlog. The next tick continues, so the backlog shrinks by at most 100000 records for each hour. A larger rate of writes needs a shorter period or a larger cap. The log line of the stop reason shows it.
- A process that dies during a run holds the lease until it expires. The runner then takes the operation over, and the delete is idempotent.
- The server clock and the database clock can differ. The cutoff uses the server clock because the record `At` uses it. A clock that is ahead by one hour deletes a record one hour early, and the floor of 24 hours bounds the effect.
- One entry for each batch grows the audit log by at most one entry for each 1000 records deleted.

## Alternatives considered

- **A ticker on each instance.** No lease, so two instances run the delete. Refused (decision 3).
- **A new lease primitive or an advisory lock.** The durable claim exists. Refused: it would be a second primitive for the same job.
- **The count in the caller-built entry.** Impossible: the count is not known before the delete.
- **A second read of the count after the delete.** Two transactions, so a crash between them leaves a delete with no entry. Refused: the store adds the count in one transaction.
- **A deterministic e2e through the test clock alone.** `dustpan.WithClock` (`pkg/dustpan/dustpan.go:505`) moves the server clock only. The schedule ticks on the database clock, so the clock cannot fire a tick. The deterministic step is in the HA suite, which sets the interval with `haFastSchedules` (`e2e/ha_schedules_test.go:45`) and ages `gate_records.at_ms` with SQL. The aging by the server clock is an internal test in `pkg/dustpan` that calls the run method with a fixed clock. This differs from the brief of the rewrite, which asked for the test clock in the e2e step.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Delete latency after a shorter retention | at most 1 hour plus 1 minute | time | one durable store |
| Idle cost of one run | 1 settings read, 1 namespace list, 1 query for each namespace | reads | each hour |
| Duplicate runs at one tick | 0 | runs | two or more processes |
