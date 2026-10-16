---
rfc: 36
title: Scale out to several instances on one database
status: discussing
created: 2026-10-10
---

# RFC 36. Scale out to several instances on one database

Status: discussing. This RFC covers only the scale-out half of high
availability: several server instances on one PostgreSQL database and one
object store, in one region. Geo replication between regions stays with
ADR 4 and is out of scope here.

## Problem

The project owner decided that the server must scale horizontally: several
instances behind a load balancer, on one PostgreSQL database and one object
store. A rolling deploy and an autoscale step then start and stop instances
while other instances serve.

Most of the server already works with several instances. The review in
issue #670 lists what is already safe: migrations take an advisory lock,
update leases, workspace locks and the namespace fence are in the database,
maintenance state changes are a compare-and-set, audit sinks ship under a
lease per sink, the audit chain migration holds a claim, checkpoints are
create-only, sign-in state is in the database, CSRF tokens come from the
session secret, and each process has its own instance ID
(`internal/instance`).

These parts do not work, or change behavior, with several instances:

1. **A start fails the work of other instances (#667).** Each start marks
   every running backup and every running restore failed, and fences every
   namespace when a restore row is open. The queries have no filter for the
   instance that runs the work. A rolling deploy fails a backup or a restore
   that another live instance runs, and moves every fence while that restore
   still runs.
2. **Two starts can lose sealed OpenTofu state (#668).** The seal step at
   start writes a sealed blob before it commits the row. The orphan sweep of
   the same step deletes every blob under the workspace that no row names.
   When two instances seal at the same time, one sweep can delete the blob
   that the other instance wrote and has not committed yet.
3. **A restore does not wait for other instances (#669).** A restore enters
   maintenance mode and applies at once. Each instance caches the
   installation state for up to 2 seconds, so other instances can write
   during that time. Writes outside a namespace have no fence. ADR 48
   decides an instance registry for this wait, but it is not built. The
   backup and restore "slots" are in process memory, so two instances can
   run two backups at once, and only the instance that runs a restore can
   answer its status during the swap.
4. **Background jobs run on every instance (#670).** The hourly audit chain
   check and checkpoint write, and the retention sweep of deleted units, run
   on each instance. The sticky checkpoint mismatch and the
   `dustpan_audit_chain_invalid` metric are per instance.
5. **Limits are per instance (#670).** The auth failure budget is in
   process memory, so with N instances an attacker gets about N times the
   budget. The token policy cache and the maintenance state cache delay a
   change on other instances by their time limit.

## Background jobs

This table lists each job that runs without a request, today's mechanism,
and the proposal. "One runner" means a job lease in the database (see
"Proposal"). "Every instance" means that each instance runs the job, and
the job is safe and cheap enough when several instances run it.

| Job | Where | Today | Proposal |
| --- | --- | --- | --- |
| Key check at start | `startUp`, `secret.CheckWrapper` | Every start reads every wrapped key. Read only. | Every instance. Each instance must check its own key. |
| Backup crash recovery | `startUp`, `FailRunningBackups` | Every start fails every running backup. Unsafe (#667). | Every instance, but only for rows that no live instance holds: rows of an instance whose lease ended, or rows with no holder. |
| Restore crash recovery and fence | `failStoppedRestores`, `FailRunningRestores`, `BumpFailoverVersions` | Every start fails every running restore and fences every namespace. Unsafe (#667). | Same rule as backups. The fence moves only when the start fails a restore row. |
| Seal of unsealed OpenTofu state | `sealUnsealedStates`, `workspace.SealUnsealedStates` | Every start seals and sweeps. Unsafe when two starts overlap (#668). | One runner, under the lease `seal-unsealed-states`. A second instance skips the step. The sweep also skips blobs younger than a grace time, so a slow commit is safe without the lease. |
| Audit chain start-up | `auditChain.startUp` | Every start compares the setting with the stored record, and can enter maintenance mode. Writes are idempotent. | Every instance. Each instance must know the stored state before it appends. See open question 4 for a mixed setting during a rolling deploy. |
| Sign-in provider binding | `bindSignInProviders` | Every start binds each provider ID to its issuer. Idempotent, refuses a changed issuer. | Every instance. |
| Inactivity disable | `sweepInactive`, hourly, and once before serve | Every instance. The store locks the server audit scope and compares with the database clock, so a second run in the same hour finds nothing. | Every instance. The cost is one locked query each hour for each instance. |
| Retention sweep of deleted units | `sweepDeleted`, hourly, `inventory.Sweeper` | Every instance. An in-process `TryLock` stops two sweeps in one process only. A second instance repeats purges that fail as not found, and deletes the same blobs again. | One runner, under the lease `retention-sweep`. The API route that runs a sweep on request takes the same lease and answers 409 while another instance holds it. |
| Sign-in request prune | `pruneSignInRequests` | Every instance runs one `DELETE` for the expired rows each request lifetime. | Every instance. A lease would cost more than the query. |
| Audit chain check and checkpoint write | `auditChain.startCheck`, hourly | Every instance reads the full log of every scope each hour, and each one tries to write a checkpoint. Checkpoints are create-only, so the second write fails. The sticky mismatch is per instance. | One runner, under the lease `audit-chain-check`. The holder writes the result to the database: the invalid scopes and the first mismatch of each scope in the epoch. Every instance reads that row for `dustpan_audit_chain_invalid` and for the refusal to sign. |
| Audit chain migration | `auditChain.startMigrations` | A claim in the chain record with `ChainMigrationLease`. One runner already. | No change. |
| Audit sink shipping | `startAuditShipper`, `audit.Shipper` | A lease per sink in `audit_sink_leases`. One runner per sink already. | No change. The job lease table can take over these rows later, but this RFC does not ask for it. |
| Configuration reload | `cmd/opendustpan/reload.go` | Each instance polls its own file and writes its own audit entry. | Every instance. The file belongs to the instance. The audit entry names the instance ID, so N entries for one change are correct. |
| Instance lease renewal and registry sweep | ADR 48, not built | None. | Every instance renews its own row. The sweep of old rows runs under the lease `instance-registry-sweep`, so N instances do not write N audit entries for one deleted row. |
| Orphan blob sweep | issue 203, not built | None. | One runner, under the lease `orphan-blob-sweep`, when it lands. |

The job lease is the default for a new job that writes, deletes or reads
the full data set. "Every instance" needs a reason in the code comment of
the job.

## Options

### How one instance runs a job

1. **Do nothing.** Every instance runs every job. Cost: #667 and #668 stay
   real data loss, and the cost of the chain check grows with N.
2. **PostgreSQL advisory locks.** `pg_try_advisory_lock` for each job, as
   the migrations do. Cheap and released when the session ends. But the lock
   belongs to one connection of the pool, so the job must keep that
   connection for its whole run. SQLite has no such lock, so the port needs a
   second adapter anyway. No row shows who holds the lock.
3. **A job lease table.** One row for each job name: the holder instance ID,
   the lease end time in Unix milliseconds, and the time the holder took it.
   A claim is one conditional `INSERT ... ON CONFLICT DO UPDATE ... WHERE`
   the lease ended or the holder is the caller. The holder renews while the
   job runs, and releases the row at the end. A crashed holder blocks the
   job only until its lease ends. The same table works on SQLite, where it
   always succeeds for the one instance. The installation API can list the
   rows.
4. **Leader election.** One instance runs every job. Simple to reason
   about, but one slow job delays all others, and the leader must stop all
   jobs when it loses the lease.

### The auth failure budget

1. **Per instance, stated in the guide.** No change in code. The guide
   states that the budget is per instance, so N instances behind a load
   balancer allow about N times the burst and the refill. A setting for the
   burst and the refill lets the operator divide them by N.
2. **Shared in the database.** One row per source with a token bucket. Each
   miss is a write. An attacker who guesses tokens then causes one database
   write per guess on every instance, which moves the cost of the attack to
   the database that all instances share.
3. **Per instance with a shared penalty list.** Each instance keeps its own
   bucket, and writes a source to a shared table only when it goes over the
   budget. Other instances read the table and apply the penalty at once. The
   database sees one write per limited source, not one per miss.

## Proposal

1. **A job lease table, option 3.** A port `joblease.Store` in a new
   tool-neutral package, with `Claim(ctx, job, holder, until)`,
   `Renew(ctx, job, holder, until)` and `Release(ctx, job, holder)`, a
   conformance suite, and an adapter in each store. The holder is the
   instance ID of the process. A job that does not get the lease skips its
   run and tries at the next tick. A job that loses its lease while it runs
   stops at the next safe point.
2. **Crash recovery by the holder, not by the start.** Each backup and
   restore row records the instance ID that runs it. A start fails only the
   rows of instances that hold no live lease in the ADR 48 registry. Until
   the registry lands (#669), the backup and restore rows hold their own
   heartbeat and lease end, and the start fails a row only when that lease
   ended. This is the fix for #667, and it is the highest priority.
3. **The seal step under the job lease, plus a grace time in the sweep.**
   This is the fix for #668.
4. **The ADR 48 registry, as accepted.** The restore asks every instance.
   The status of a running backup or restore comes from the database, and
   the "slot" becomes a database claim, so one backup runs at a time across
   instances. This is #669.
5. **The chain check under the job lease, with its result in the
   database.** Every instance then agrees on `dustpan_audit_chain_invalid`
   and on the refusal to sign after a mismatch.
6. **The auth failure budget, option 1 now, option 3 later.** A per-instance
   budget keeps a database write out of the path of a failed sign-in. The
   guide for several instances states the N times factor, and new settings
   for the burst and the refill let the operator set the total. Option 3 can
   follow if the owner wants a shared limit without one write per miss.
7. **A guide for several instances.** It states the operator rules:
   - Every instance uses the same key-encryption key. Make the key once and
     give it to each instance. Do not use `--generate-key` on more than one
     instance: two instances that start together on an empty database can
     each make a different key.
   - Every instance uses the same bootstrap admin token and session secret.
   - The backup target is `s3://`. A `dir:` target is per instance.
   - The token policy cache (5 seconds) and the maintenance state cache
     (2 seconds) delay a change on other instances by that time.
   - Each instance writes its own start entry and reload entries.

The order of the work is #667, #668, #669, then the job lease for the chain
check and the retention sweep, then the guide.

## Open questions

1. **Lease times.** The proposal needs a lease length and a renew interval
   for each job. A proposal: a 60 second lease, renewed every 20 seconds, for
   every job, with the clock-skew bound of ADR 48 decision 7. Is one setting
   for all jobs enough?
2. **A lease that ends while the job runs.** The retention sweep and the
   chain check can stop at the next scope. The seal step can stop at the
   next workspace. Is "stop at the next safe point" enough, or must a job
   that lost its lease also roll back what it did since the last renewal?
3. **The auth failure budget.** Is a per-instance budget with a stated
   factor acceptable (option 1)? Or must the limit be shared across
   instances (option 2 or 3)?
4. **A mixed setting during a rolling deploy.** Instances that start with a
   different audit chain setting, a different `--config` file, or a
   different server version run at the same time. Must the server refuse a
   start when its setting differs from the settings of the live instances in
   the registry, or is the stored record enough?
5. **The registry first, or the row lease first, for #667.** The registry
   of ADR 48 gives one liveness signal for all work. A lease on each backup
   and restore row fixes #667 without the registry, and can move to the
   registry later. Which order does the owner prefer?
6. **The job lease table and the audit sink leases.** Must the sink leases
   move into the job lease table, so one table shows every holder? Or do
   they stay separate?
7. **Visibility.** Must the installation API list the job leases and the
   registry, with an action for each, so an operator can see which instance
   runs what?
8. **Instances in different versions.** A rolling deploy runs an old and a
   new server version on one database for some minutes. ADR 48 decision 4
   refuses a start against a newer schema. Must an old instance also stop
   when a new instance migrates the schema while the old one runs?

## Outcome

None yet.
