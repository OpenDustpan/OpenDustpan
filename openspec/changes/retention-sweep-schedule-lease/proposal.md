---
version: 5
---

No issue: spec rewrite of an existing change. The earlier version (v3) named internal/cron as the schedule home, said that main has no retention setting and named a lease primitive that does not exist. This version is checked against main at 344dec65.

## Why

**Persona: operator.** An operator runs an installation with several server processes. Each OpenTofu lock request writes one `state.write` decision record. The setting `decisionRecords.retentionHours` is on main, and so is the method that deletes expired records, `gate.Records.PruneExpired`. Nothing calls the method. The records grow without a limit, whatever the setting says. The operator needs one prune at a time, on a known period, with an audit entry for each prune that names the count.

The user path this change must make work:

1. **Setting.** The operator sets `decisionRecords.retentionHours` through `PATCH /api/dustpan/v1/settings`, for example `8760`. The default `0` keeps records forever.
2. **Prune.** About once an hour, one server process prunes the `state.write` records that are older than the period, in each namespace, in batches. The operator does nothing else. A server without a durable store runs no prune.
3. **Audit.** The operator reads the audit log and finds one entry for each namespace and each batch that the prune ran. The entry names the namespace, the reason and the count of the deleted records.
4. **Docs page.** The operator reads `docs/guides/decision-record-retention.md`. It says when the prune runs, what it does with `0`, and how to read the entries.
5. **E2e.** A step starts the real binary with two server processes on one database, ages records past the 24-hour minimum, and checks that the records are gone, that the entries name the count, and that no second prune ran at the same tick.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The stored retention text is not `0` or a whole number of hours from 24 to 87600. | The prune deletes nothing, logs the key, the text and the allowed range, and the run counts as failed. The settings resource already refuses such a value, so only a database row from another version reaches this. | `GET /api/dustpan/v1/settings` |
| A namespace is fenced while the prune runs. | The prune skips that namespace and logs its name. The next run tries it again. | `GET /api/dustpan/v1/settings` |
| Another process holds the claim of the run. | No error. The process does not run the schedule at this tick. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above (commands run on main 344dec65):

- The prune method exists and nothing calls it. Proof: `rg -n "PruneExpired" pkg internal cmd --type go` finds the interface (`pkg/gate/store.go:212`), the shared store code (`internal/store/gatestore/records.go`) and tests only.
- The setting exists. Proof: `pkg/gate/retention_setting.go:12` (`RetentionKey`), `:41` (`RetentionPeriod`) and `pkg/dustpan/settings_resource.go:53` (`decisionRecordRetentionDeclaration`).
- `internal/cron` holds a parser only. The pattern for a scheduled sweep is the constant `deletedSweepEvery` in `pkg/dustpan/sweep.go:22` and the durable schedule list in `pkg/dustpan/schedules.go:78`. Proof: `rg -n "durable|Lease" internal/cron` finds nothing.
- `pkg/dustpan/area_retention.go` holds job retention only (`history.RetentionSettings`). Proof: it has no reference to `gate` or to decision records.
- The lease is the durable schedule claim. `ScheduleStore.ClaimTick` (`pkg/durable/schedule.go`) lets one process claim each tick in one conditional update, and the `Runner` holds the started operation under a lease and takes over an expired lease (`pkg/durable/runner.go`). Proof: `internal/store/sqlite/durable_schedules.go` and `internal/store/postgres/durable_schedules.go:99`.
- Without a durable store each other sweep runs as a ticker on each instance, with no lease (`pkg/dustpan/schedules.go:468-481`). A prune of evidence must not run twice at once, so this change does not add that fallback.
- The count cannot be in the caller-built entry today. `PruneExpired` appends the entry that the caller gives before the delete count is known (`internal/store/gatestore/records.go:337-345`, `Runner.Write` at `internal/store/gatestore/tx.go:187`). The prune of name counts solves this in the store (`internal/store/sqlite/namelocks.go:240-262`, `internal/authgate/namelock.go:269`).

## What Changes

- Add `pkg/dustpan/recordprune.go` (NEW). An unexported `recordPruner` holds the gate store, the namespace store, the setting reader and the clock. Its constructor returns nil when the namespace store is not a `gate.Store` or the server keeps no settings. The file defines NEW `decisionRecordPruneEvery = time.Hour` (a fixed period like `deletedSweepEvery`), the schedule name `decision-record-prune` and the Kind `kindDecisionRecordPrune`.
- Register the schedule in `Server.jobSchedules` and the Kind in `Server.jobKinds` (`pkg/dustpan/schedules.go:78`, `:149`), beside `nameLockPruneKind` (`:302`). The durable `Scheduler` and `Runner` already take their lists from these two functions. A duplicate Kind is refused at start (`pkg/durable/runner.go:223-234`).
- The run reads the retention with `setting.Service.Effective(ctx, setting.InstallationScope(), gate.RetentionKey)` and `gate.RetentionPeriod`. A parse error fails the run and deletes nothing. `0` makes no call and writes no entry.
- The run lists the namespaces with `namespace.Store.ListNamespaces`. For each namespace it asks `Records.Query` for one record of the decision point state.write before the cutoff. If there is none, it makes no call. Else it calls `Records.PruneExpired` with the namespace name, the request, the fence of the namespace and the entry in batches of `gate.MaxPruneBatch` until a short batch, a cap of batches for the run or the deadline. A fenced namespace is skipped. The server scope is never given to a call.
- Change `PruneExpired` so that the entry carries the count: add NEW method `WriteCounted` to `Runner` in `internal/store/gatestore/tx.go`. It appends the change `pruned` with the count of the deleted records in the same transaction. Implement it in `internal/store/sqlite/gate.go` and `internal/store/postgres/gate.go`, and add a case to the records conformance suite `pkg/gate/gatetest/run_records.go`.
- Replace the text "No job deletes decision records yet" in the declaration of the setting (`pkg/dustpan/settings_resource.go:48-62`).
- Update `docs/guides/decision-record-retention.md`: the sections "Why the records grow", "The prune method" and "What is not available yet".
- Add an e2e step to the HA suite (`e2e/ha_schedules_test.go`) and an internal test that ages records with the server clock.

Out of scope:

- The retention volume and the content of the decision record.
- A new lease primitive, and a ticker fallback for a server without a durable store.
- A sweep on a runner pool: the prune runs in the server worker only.
- The deletion of audit entries and the chain anchor (ADR 68 decision 2 leaves "how" to a later ADR).
- The housekeeping principal of this prune: the prune moves to it later, as a follow-up in `housekeeping-principals-sweeps`.
- A setting for the period of the prune, and a manual prune route.

Depends on: nothing that is open. `add-decision-record-retention-setting` and `retention-volume-state-write-decision` are on main (see "What main already does"). Related and not blocking: `housekeeping-principals-sweeps` (the later move of the actor).

## Usability

Where the path still costs the person time or surprises them:

- `0` keeps records forever and writes no entry. The docs page and the setting text say so.
- A shorter period deletes at the next run, within the hour, not at once. The docs page says so and names the audit entry as the proof.
- A server without a durable store never prunes. The docs page says so and names the durable store as the condition.
- One entry for each batch can look noisy on a large backlog. A batch holds up to 1000 records, so 100000 expired records write 100 entries, and the entry of each batch names its count.

## What main already does

Each fact names its file, line and function on main 344dec65. The change specifies only the difference.

- The retention setting is declared. (`pkg/dustpan/settings_resource.go:53 (func decisionRecordRetentionDeclaration)`; `pkg/gate/retention_setting.go:12`, `:41 (func RetentionPeriod)`)
- `PruneExpired` deletes at most `req.Batch()` records of one namespace at `state.write`, lowest ID first, and appends the caller's entry in the same transaction, also with no match. It refuses `audit.ServerScope`, an invalid name and a wrong fence. (`pkg/gate/store.go:212`; `internal/store/gatestore/records.go:309-358`)
- `gate.NewPruneRequest` refuses a zero cutoff and a batch outside 1 to 1000. (`pkg/gate/prune.go:20`)
- The durable schedule list and Kind list are `Server.jobSchedules` and `Server.jobKinds`. (`pkg/dustpan/schedules.go:78`, `:149`)
- A prune with a count in its entry exists for name counts. (`internal/localusersvc/locks.go:356-364`, `:488-548 (func PruneLocks)`; `internal/authgate/namelock.go:269 (func PrunedChange)`)
- The deleted-unit sweep has a fixed hourly period and a timeout. (`pkg/dustpan/sweep.go:22`, `:26`, `:185 (func sweepDeletedOnce)`)
- The e2e schedule suite sets the schedule interval with a database trigger and waits for the runs. (`e2e/ha_schedules_test.go:39-62`, `:172 (func haWaitForSchedules)`)

## Owner decisions that apply

- **ADR 370 (owner, 2026-11-14)**: see the ADR check.
- **Owner decision 6**: the prune writes as `@server` now. It moves to the housekeeping principal later (ADR 370). Nothing in this change waits for the principal.
- **Owner decision, 23 Nov 2026 (ADR 396, replaces the 13 Nov 2026 answer P14)**. The owner said: "Sweeps must run on the user's own runners as we most recently re-designed it." A sweep runs on a runner pool of the team. The pool has the sweep purpose. The host identity of the pool has no write access. ADR 164 decisions 1 and 5 and ADR 343 decisions 3 to 5 say so. The server has no installation sweep pool. The pool management API accepts the sweep purpose on a pool of a namespace. It applies the same rules as for any other purpose. This prune runs in the server worker and uses no runner pool, so the decision does not change it.

## ADR check

Each quote is copied from `docs/adr/`. The chains are those of the ADR files at 344dec65.

- **ADR 68, decision 2**: "Retention is a setting. An operator can set a retention period for the installation. The server then deletes entries that are older than the period. A change of the period writes an audit entry. The deletion keeps the chain check of ADR 60 decision 17 and ADR 64 valid. A later ADR decides how, for example with a retention anchor (RFC 22)." This change deletes decision records, not audit entries. Decision 1: "The server keeps audit entries forever by default." The default `0` keeps this. Conflict: none.
- **ADR 229, decision 5**: "A record of `state.write` or of an access limit lives for the audit retention (ADR 68)." ADR 229 has owner-tagged decisions (decision 1 "One record for each evaluation (owner)"). Changed by: ADR 286 (the words catalog and promotion only). The prune deletes a record only after the period of the setting. Conflict: none.
- **ADR 250, decision 3** (owner decision): "Fail closed and record: an unknown value, a parse error, missing configuration or a dependency error means deny. Every denial and every use of a privilege goes to the audit log." Changed by: ADR 382 (the audit record of repeated denials goes through the shared denial limiter). The prune fails closed on a retention parse error and on a fence error, and writes the entry of each call. A parse error fails the run and is logged once for each run. It is not a repeatable caller refusal, so the limiter of ADR 382 does not apply. Conflict: none.
- **ADR 370, decision 2** (owner, 2026-11-14): "Each housekeeping purpose has its own built-in service principal (owner, 2026-11-14). The housekeeping that no person configures runs as a built-in service principal of its purpose: for example the sweeps of blobs and checkpoints, the retention of history, the cleanup of artifacts, the sweep of access tokens, the expiry of temporary administrators, the cleanup of saved views and the reconcile of jobs. Each one is made at the install. The record of each run names it." The list of the decision does not name decision records. The principal change is not on main: the sweeps on main still write `@server` (`history.ServerSubject`, `pkg/history/history.go:156`; `pkg/dustpan/sweep.go:144`). Owner decision 6: the prune writes as `@server` now and moves to the housekeeping principal later. Conflict: none, because the owner decided the interim actor.
- **ADR 343, decision 3** (owner, 2026-11-07): "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays." The decision is about the inventory sweeps of cloud accounts. A prune of the server's own table is not such a sweep. Conflict: none.
- **ADR 396, decision 1** (owner, 2026-11-23): "A sweep runs on a runner pool of the team that has the sweep purpose." The prune of the server's own table is not a sweep of a runner pool. It names no pool. There is no conflict.
- **ADR 364** (owner): visibility and golangci-lint before an added check. The design uses an unexported type with a constructor and adds no lint rule or scanner.

## Open questions

Open questions: none. The one question of the earlier version is answered by the owner (decision 6): the prune writes its entries as `@server` (`history.ServerSubject`) now, and it moves to the housekeeping principal later (ADR 370). See "Owner decisions that apply".

Follow-up, not a task of this change: when the housekeeping principals land, this prune moves from `@server` to the principal of its purpose. That move belongs to `housekeeping-principals-sweeps`, which owns the list of sweeps that change actor.

Questions considered and answered:

- Does the change conflict with an ADR? No. ADR 370 decision 2 does not name decision records, and the owner decided that the prune writes as `@server` until the principal exists.
- Who is the actor of the entry? `@server` now (owner decision 6). The move to the housekeeping principal is a follow-up.
- Does the lease need a new primitive? No. The durable schedule claim and the runner lease are the primitive.
- What happens without a durable store? No prune. A ticker on each instance has no lease and would run the delete twice. Provisional PM decision (rule 88, not security semantics).
- Which period? Fixed hourly, like `deletedSweepEvery`. A setting for it is out of scope. Provisional PM decision.
- Which lease? One durable-operation schedule with a new Kind `decision-record-prune`. Provisional PM decision.
- How many entries? One for each `PruneExpired` call, which is one for each batch. The run calls the method only for a namespace where `Records.Query` finds an expired record, so an idle namespace writes no entry each hour. Provisional PM decision.
- Does the sweep read the server scope? No. `state.write` records always have a namespace. `PruneExpired` refuses `audit.ServerScope` and the run never gives it.
- What does `0` do? No call and no entry. The default install writes no entry each hour. This differs from v3, which wrote an entry for `0`.
