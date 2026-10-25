---
rfc: 105
title: A purge and a backup at the same time, and when the purge may delete blobs
status: discussing
created: 2026-10-25
---

# RFC 105. A purge and a backup at the same time, and when the purge may delete blobs

Status: discussing. This RFC answers the second half of issue #722: the guard
between the purge of a unit and a backup that runs at the same time. The
first half (one sweep at a time across instances) is in draft PR #1917. The
implementer gave four options in
[a comment on #722](https://github.com/OpenDustpan/OpenDustpan/issues/722#issuecomment-6402197660).
This RFC compares them and recommends one. It has no code. The decision is for
the project owner.

## Problem

### What a backup promises today

ADR 30 decision 2 gives the promise:

> A backup reads the SQL data in one read transaction. It then copies each
> blob that the SQL data names. Blobs are immutable and keys are never
> reused, so the copy is consistent while the server writes. [...] Retention
> (RFC 18) must not delete a blob that a running backup can still copy.

ADR 42 decision 6 keeps it for the portable format: both dialects "read every
table at one point in time". PostgreSQL reads in one repeatable-read,
read-only transaction (ADR 30 decision 4). SQLite reads a `VACUUM INTO` copy.

So a backup is a SQL snapshot at time S, plus a copy of every blob that the
snapshot names. The copy runs after S and reads the live blob store
(`pkg/backup/service.go`, `backup`, then `copyBlob` for each reference in key
order). The blobs that a snapshot names are the content keys of finalized
state versions, the checkpoint blobs, the update journal blobs and the item
key blobs that are not shredded (`stagingBlobRefs` and `BackupBlobs` in the
store adapters).

The last sentence of ADR 30 decision 2 is a rule for retention. The code does
not enforce it yet. That is the gap.

### What the purge does

The final delete of a unit (ADR 47) has three steps:

1. **The purge transaction.** `stack.Service.PurgeExpired` or
   `workspace.Service.PurgeExpired` removes the rows of the unit and inserts
   a blob purge record (`blob_purges`, `inventory.BlobPurge`) in one fenced
   transaction. Call its commit time P.
2. **The first blob delete, D1.** At once after the commit,
   `pkg/dustpan/lifecycle.go` (`purgeUnit`, then `blobPurger.deleteBlobs`)
   deletes every blob of the unit. A failure only logs a warning, and the
   record stays.
3. **The second blob delete, D2.** The retention sweep
   (`inventory.Sweeper.finishBlobPurges`) deletes the blobs of each record
   again when the record is older than `BlobPurgeGrace` (24 hours). It
   finishes the record when no blob remains. The grace protects against a
   different race: a checkpoint save or a state upload that writes its blob
   after D1 and then fails its SQL commit.

The scheduled sweep and the admin route `POST /api/server/deleted-units/sweep`
run steps 1 to 3 for each expired unit. An admin purge of one unit (the
`unit:purge` route of the inventory API) runs steps 1 and 2 through the same
`purgeUnit` function. So the race is not only a race of the sweep.

The 24-hour grace does not protect a backup today. D1 runs at once after P,
not after the grace.

### The race

A backup takes its snapshot at S. A purge commits at P after S. The snapshot
still holds the rows of the unit, so it names the blobs of the unit. Then D1
deletes those blobs while the backup copies.

This RFC read the copy code to find what happens next. `streamCopy` calls
`Get` on the live store. A missing blob makes `Get` fail, the step fails, and
`failRow` marks the backup `failed` with the text `read blob "<key>": ...`.
When the target already holds the blob from an earlier backup, the copy
hashes the live blob again (`hashStoredBlob`), and that read fails in the
same way. The manifest is written last, so a failed backup has no manifest,
and `backup check` and restore refuse it.

So this RFC found no path where the race gives a backup that has a manifest
and misses a blob. The effect today is a **failed backup**, not a silently
inconsistent one. That is fail closed, but the operator loses a backup, and
a busy retention schedule could make backups fail again and again. The
failure text names a blob, not the purge, so the operator cannot see the
cause. That is against "No magic".

Two later changes make the guard more important:

- ADR 151 decision 8 makes a purge write a tombstone, and decision 16 makes
  a shred operation delete the blobs of the unit on the live store and on
  each backup target. That operation is a third delete of committed blobs.
  ADR 151 decision 16 step 1 already decides its guard: "It waits until no
  backup that started before the commit still runs." The `history` package
  has the `Tombstone` type and the `AppendTombstone` port, but this RFC
  found no production caller yet.
- With several instances on one database (the horizontal scaling work of
  #722), the purge and the backup can run on different instances. A guard in
  one process is not sufficient.

### Other deleters of live blobs

This RFC searched for other code that deletes a blob from the live store.
`secret.ItemSealer` and `unitimport/upload` delete only a blob whose SQL
write did not commit, so no snapshot can name it. This RFC found no other
delete of a committed blob. The guard below must cover D1, D2 and the future
shred operation of ADR 151.

## Names used below

- **P**: the commit of the purge transaction.
- **D**: any delete of the blobs of a purge record (D1, D2 or the shred).
- **B**: the commit of the running backup row (`Registry.StartBackup`).
- **S**: the snapshot of the backup. S is always after B, because the
  durable operation runs only after its row commits.
- **E**: the end of the backup row (`FinishBackup`, or `FailEndedBackups`).
  E is always after the last blob copy.

A backup is at risk only when S < P and D runs before E.

## Options

### Option 0. Do nothing

- **Correctness.** A backup never gets a manifest with a missing blob (see
  above). A backup fails when D runs between S and E.
- **Operator impact.** Lost backups, with an error text that does not name
  the cause. ADR 30 decision 2 stays not enforced, and ADR 151 decision 16
  needs a guard anyway.
- **Schema.** None.

### Option 1. A shared advisory lock

The backup takes a shared lock before S and keeps it until E. Each D tries
the exclusive lock and skips its delete when it does not get it.

- **Correctness.**
  - Sweep first (P and D before B): the backup waits for the lock or takes
    it after D. Its snapshot is after P, so it does not name the blobs.
    Correct.
  - Backup first (S before P): D cannot get the lock until E. Correct.
  - Both mid-way: correct only if the lock covers every D. A lock around the
    purge transaction alone is not sufficient, because D1 runs after the
    transaction. It must also cover the admin purge and the shred.
  - Process crash: PostgreSQL releases a session advisory lock when the
    connection ends. The durable runner then gives the backup to another
    instance, which runs the write step again with a new snapshot (`clearPartial`,
    then `backup`). The new snapshot is after P, so the release is safe.
  - Connection loss: if the connection that holds the lock breaks while the
    backup copies, PostgreSQL releases the lock and the backup continues.
    The race is open again for the rest of that backup. PR #1917 states the
    same limit for the sweep lock. A pooler in transaction mode (for example
    PgBouncer) does not keep session locks at all.
- **Operator impact.** Blob deletes stop for the whole backup. The lock is
  visible only in `pg_locks`, not in the API, unless each skip is counted and
  shown. SQLite needs its own lock in the store (one process uses the file).
- **Schema.** None. A new port for the lock, with a suite for both dialects.
- **Comparable practice.** restic and Borg use this model: `backup` takes a
  non-exclusive lock on the repository, and `prune` needs an exclusive lock
  and refuses to start while a backup holds one. Both tools must find and
  remove stale locks after a crash, which shows the cost of a lock that
  lives outside the data.

### Option 2. Pin the blob keys

The backup writes one pin row for each blob key of its snapshot. D skips
pinned keys. The pins go when the backup ends or fails.

- **Correctness.**
  - The pins exist only after the backup has read its snapshot, so they
    commit after S. A D that runs between S and the commit of the pins sees
    no pin and deletes. To close that window, D must also wait for a backup
    whose pins are not written yet. That is option 3 with a table added.
    A read-write repeatable-read transaction that exports and pins in one
    commit does not close the window either: the commit is still after S.
  - Process crash: the pins of a crashed backup must be released, by the
    takeover or by `FailEndedBackups`. A pin that stays blocks the deletes
    of its keys for ever.
- **Operator impact.** Deletes wait only for the keys of a running backup.
  The gain over option 3 is small, because only one backup runs at a time.
- **Schema.** A new control table (one row for each blob of each running
  backup, so possibly hundreds of thousands of rows), a migration in each
  dialect, a place in `backup.ControlTables`, and conformance tests for the
  pin and the release. This option costs the most.
- **Comparable practice.** etcd keeps the pages of an open bbolt read
  transaction while compaction runs, so `etcdctl snapshot save` reads a
  consistent file. That is a pin, but inside the storage engine, at no cost
  to the caller. S3 Object Lock and legal holds are pins on objects. Both
  pin at the storage layer, not in an application table.

### Option 3. Wait while a backup that may name the blobs runs

Before each D, read the backups registry. If a backup row is `running`, skip
the delete and leave the record pending. The `backups` table already holds
the running row and its `started_at`, so the "backup running since" read
model of the issue comment exists.

This RFC proposes a stricter form than the issue comment. The issue comment
compares the start of the backup with the end of the grace. That compares
the clocks of two instances, and it does not cover D1, which runs before the
grace. The proposed rule uses no clock:

> D deletes the blobs of a purge record only when a read that starts after
> the commit of that record finds no running backup.

- **Correctness.** Take a D that finds no running backup at time T, with
  P < T.
  - Every backup with S < P had B < S < P < T. Its row was visible at T, so
    it was not running at T: it has ended (E < T). It copies nothing more.
  - Every backup with B > T has S > B > T > P. Its snapshot does not name the
    blobs of the unit.
  - A backup with B < T that is not running at T has ended.
  - So no backup that names the blobs copies after D. This holds for each
    order: sweep first, backup first, and both mid-way. It holds on PostgreSQL
    because the registry read is a committed read after P, and on SQLite
    because one process holds the write lock.
  - Process crash of the backup: the row stays `running`. Another instance
    claims the operation after the lease and runs the write step again with a
    new snapshot, or `FailEndedBackups` marks the row failed. D waits until
    then. The wait ends when the lease ends plus the time of the backup.
  - Process crash of the purge: the record stays, and the next sweep runs D2
    with the same check.
  - A failed registry read: D does not delete (fail closed), and the sweep
    counts a failure with its error kind.
- **Long backups.** The issue comment asks whether a backup that runs longer
  than the grace needs option 2. With the rule above, it does not. D waits
  for the end of the backup, however long it runs. The grace and the wait are
  two separate conditions: D2 needs both.
- **Operator impact.** No backup waits and no backup is refused. Blob deletes
  wait while any backup runs, also a backup that started after P. Such a wait
  is not necessary, but it is short: one backup runs at a time, and the next
  sweep runs one hour later. Blobs of a purged unit can stay longer on the
  live store. They already stay up to 24 hours (the grace), and the data key
  of the unit goes in the purge transaction. If backups run without a gap,
  deletes could wait for a long time. The operator must see that (see
  "Visibility" below).
- **Schema.** None. One new read on `backup.Registry`, for example
  `RunningBackup(ctx) (Record, bool, error)`, with conformance tests first.
- **Comparable practice.**
  - PostgreSQL VACUUM against a `pg_dump` snapshot. `pg_dump` reads in one
    repeatable-read transaction. VACUUM does not remove a row version that
    the oldest running snapshot can still see (the xmin horizon). VACUUM does
    not ask when the snapshot started; it asks whether one is still open.
    The wait is visible: `pg_stat_activity.backend_xmin` names the holder,
    and `VACUUM VERBOSE` reports the removable cutoff. The cost is the same
    as here: a long dump delays cleanup. PostgreSQL once had
    `old_snapshot_threshold`, which made an old snapshot fail with "snapshot
    too old" so that cleanup could go on. PostgreSQL 17 removed it. This
    RFC does not propose such a limit.
  - Git `gc` keeps unreachable objects for `gc.pruneExpire` (two weeks by
    default), so that a process that runs at the same time can still use
    them. That is a grace period, as `BlobPurgeGrace` is.
  - S3 lifecycle rules do not coordinate with a reader. AWS Backup for S3
    requires bucket versioning, and the usual practice is to expire
    noncurrent versions only after some days. That is also a grace, with no
    check of a running reader. A grace alone is not sufficient here, because
    a backup can run longer than the grace.
  - ADR 151 decision 16 step 1 already accepts this rule for the shred
    operation: "It waits until no backup that started before the commit
    still runs." Option 3 gives D1 and D2 the same rule, in the conservative
    form above.

### Option 4. The sweep and the backup exclude each other

The sweep runs only when no backup runs. A backup refuses to start while a
sweep runs.

- **Correctness.** Two separate checks race: the sweep reads "no backup",
  the backup reads "no sweep", and both start. A correct form needs one lock
  for both, and so it becomes option 1. The admin purge of one unit and the
  shred operation are not sweeps, so this option does not cover them without
  more work.
- **Operator impact.** The worst of the four. A scheduled or manual backup
  is refused or waits for up to 30 minutes (`deletedSweepTimeout`). The
  backup is the safety net, and a refused backup can be missed. The purges
  also wait, so a unit can stay past the end of its retention.
- **Schema.** None. It uses the `SweepLocker` of PR #1917 (not on `main`
  yet) and the running row.

### Summary

| | Option 1 lock | Option 2 pins | Option 3 wait | Option 4 exclude |
| --- | --- | --- | --- | --- |
| Sweep first | correct | correct | correct | correct |
| Backup first | correct | correct | correct | correct with one lock |
| Both mid-way | correct if every D takes the lock | open window S to pin | correct | races without one lock |
| Crash | lock released, takeover safe; connection loss opens the race | stale pins block deletes | waits for the lease and the takeover | as option 1 |
| Backup stalls | none | none | none | up to 30 minutes, or refused |
| Delete stalls | for each backup | pinned keys only | while any backup runs | while any backup runs |
| Schema change | none, new lock port | new table and migration | none, one registry read | none |
| Covers admin purge and shred | only if wired to each | yes | yes | no |

## Proposal

Option 3, in the clock-free form above.

1. **One guard for every delete of committed blobs.** A new port in
   `inventory`, for example `BackupWatch` with
   `RunningBackup(ctx) (backup.ID, bool, error)`, backed by the backups
   registry. D1 in `purgeUnit`, D2 in `Sweeper.finishOne`, and later the
   shred operation of ADR 151 call it after the commit of the record and
   before the delete. It returns an error on any failed read, and the caller
   then does not delete.
2. **D1 waits too.** When a backup runs, `purgeUnit` does not delete the
   blobs. The record stays, and D2 deletes them after the grace and after the
   backup. No schema change is needed for this: the record already exists.
3. **Keep the grace.** D2 needs both conditions: the record is older than
   `BlobPurgeGrace`, and no backup runs.
4. **No change to the purge transaction or to the backup.** The backup takes
   no lock and never waits.

### Visibility

Every wait and every skip must be visible to the operator:

- `inventory.SweepResult` gets a count of the records that wait for a
  backup, and the ID of that backup. The response of
  `POST /api/server/deleted-units/sweep` shows both, for example
  `"waitingForBackup": {"backup": "bkp-...", "blobPurges": 3}`.
- The sweep writes one INFO line for each sweep with waiting records, with
  the backup ID and the count, as `logPurged` does for purges.
- D1 writes one INFO line when it leaves the blobs for the sweep because a
  backup runs, with the tool, the unit ID and the backup ID.
- A metric gives the number of waiting records and the age of the oldest
  one, so an operator can alert when deletes wait for a long time.
- The audit entry of the purge does not change. The purge did happen. Only
  the blob delete waits.

### Conformance tests

New port method first (CLAUDE.md: a new port method gets conformance tests
before any adapter implements it):

- `storetest.RunBackup`, both dialects:
  - "the running backup read returns the running row": a started backup is
    returned with its ID.
  - "the running backup read returns none when every backup ended": rows in
    `succeeded` and `failed` do not count.
  - In the two-handle suite: "a backup started on one handle is running for
    the other handle". This is the case of two instances.
- `inventory` (fake ports, no sleep):
  - A record older than the grace, with a running backup: no call to
    `DeletePurgedBlobs`, the record stays pending, and the result counts it
    as waiting with the backup ID.
  - The same record after the backup ends: the sweep deletes the blobs and
    finishes the record.
  - A failed read of the running backup: no delete, `Failed` counts it, and
    the warning has an error kind and no blob key.
  - An unknown backup state in the read: no delete (fail closed).
- `dustpan` lifecycle: a purge while a backup runs commits, does not delete
  the blobs, leaves the record, and logs the backup ID. The admin purge route
  and the sweep both take this path.
- Regression test of #722, both dialects: start a backup and hold it after
  its snapshot (a blocking `Get` in a fault wrapper of the blob store, as
  `internal/blob/blobfault` gives). Purge the unit and run a sweep with a
  clock past the grace. The blobs stay. Let the backup finish. The backup
  succeeds and `backup check` passes. The next sweep deletes the blobs and
  finishes the record.
- Takeover: a backup row stays `running` after its holder stops. Deletes
  wait. The takeover runs the write step again with a new snapshot that does
  not name the purged unit. After the row ends, the sweep deletes the blobs.

Backups are security code under CLAUDE.md, so the implementation PR also
needs a test for each defense-in-depth step that it adds, and a comment on
each check that looks redundant.

## Open questions

1. **Maintenance mode.** The issue says that background work pauses in
   maintenance mode. Maintenance mode refuses the checked writes, so the
   purge transaction does not run. This RFC did not confirm that D2, which
   is not a fenced write, stops in maintenance mode. Should D2 also stop
   while a restore runs? A restore takes a backup before it applies (ADR 38
   decision 8), so the guard of this RFC covers that backup, but a restore
   also swaps the `blob_purges` table.
2. **Waits that do not end.** If backups run without a gap, or a running row
   has no instance that can take it over, deletes wait for ever. This RFC
   proposes to show the wait (metric and log) and not to add a limit. Is a
   warning after a fixed age, for example two times the grace, sufficient?
3. **A precise form later.** The rule waits also for a backup that started
   after the purge. A precise form could compare a database sequence of the
   backup row with one of the purge record, with no clock. Is the simple
   form sufficient until measurements show long waits?
4. **The shred operation of ADR 151.** That operation also deletes on the
   backup targets. Should this RFC's port be the one that ADR 151 decision
   16 step 1 uses, so that there is one guard? This RFC assumes yes.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
