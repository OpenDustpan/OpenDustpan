---
rfc: 105
title: Blob deletes while a backup runs, from the purge, the checkpoint replace and the journal build
status: accepted
adr: 280
created: 2026-10-25
---

# RFC 105. Blob deletes while a backup runs, from the purge, the checkpoint replace and the journal build

Status: accepted as [ADR 280](../adr/0280-milestone-7-high-availability-on-one-database.md),
option 3. This RFC started as the second half of issue #722: the
guard between the purge of a unit and a backup that runs at the same time. The
first half (one sweep at a time across instances) is in draft PR #1917. The
implementer gave four options in
[a comment on #722](https://github.com/OpenDustpan/OpenDustpan/issues/722#issuecomment-6402197660).
The review of this RFC found that the purge is not the only code that deletes
a blob that a backup can name. A checkpoint save and a journal build of a
Pulumi update do it too. So this RFC compares the options for every such
delete, and recommends one rule for all of them. It has no code. The decision
is for the project owner.

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
order). A snapshot names these blobs (`BackupBlobs` and `stagingBlobRefs` in
the store adapters):

- the content of each **finalized** OpenTofu state version (`state_versions`,
  `status = 'finalized'`);
- the blob of each Pulumi checkpoint row (`checkpoints`);
- the blob of each Pulumi journal batch row (`update_journal`);
- the blob of each item key that is not shredded (`item_keys`).

The sentence "Blobs are immutable and keys are never reused" is true. But
the consistency of the copy also needs a second fact: a blob that the
snapshot names stays in the live store until the copy has read it. ADR 30
states this only for retention. The code does not enforce it anywhere.

### Every delete of a blob that a snapshot can name

This table is derived from the code on `main` at 94b9669. The author made
it with `rg` for every `Delete` call on a blob store or a scoped blob view
outside tests, and followed each call to its callers. A delete is **in scope**
when the blob can be named by a snapshot that was taken before the delete.

| # | Deleter | File and function | What it deletes | When | In scope |
| --- | --- | --- | --- | --- | --- |
| 1 | Checkpoint replace | `pkg/stack/updates.go`, `Updates.afterCheckpointSave` | The blob of the previous checkpoint of the same update. `PutCheckpoint` is an upsert on `(stack_id, update_id)`, so the new row replaces the key that the snapshot read. | After each checkpoint save that commits a new key (`SaveCheckpoint`, and `SaveCheckpointDelta` in `delta.go`, both through `commitCheckpoint`), and after a journal build (`journal_build.go`, `commitBuiltRow`) | Yes |
| 2 | Update sweep | `pkg/stack/updates.go`, `Updates.sweepUpdateBlobs` and `sweepCandidates` | Blobs of one update that the current checkpoint row does not name and that are older than `sweepGracePeriod` (1 hour) | After each checkpoint save, and on a retried save of the same content | Yes |
| 3 | Stack sweep | `pkg/stack/updates.go`, `Updates.sweepStackOnStart` and `sweepStackBlobs` | Blobs under the stack directory that no checkpoint row names and that are older than 1 hour | At the start of each update of the stack, previews and dry runs included (`StartBy` calls it after every `StartUpdate`, and a preview takes no stack lock) | Yes |
| 4 | Journal build | `pkg/stack/journal_build.go`, `Updates.commitBuiltRow`, then `pkg/stack/journal_entries.go`, `JournalEntries.RemoveBlobs` | The journal batch blobs of the update, after `PutBuiltCheckpoint` removed their `update_journal` rows | When the server builds the checkpoint of a journaled update | Yes |
| 5 | Pulumi purge | `pkg/dustpan/lifecycle.go`, `pulumiLifecycle.purgeUnit`, then `pkg/stack/updates.go`, `Updates.DeleteStackBlobs` | Every blob of the purged stack | At once after the purge commits (D1). The admin purge of one unit and the retention sweep both take this path. | Yes |
| 6 | OpenTofu purge | `pkg/dustpan/lifecycle.go`, `opentofuLifecycle.purgeUnit`, then `pkg/workspace/service.go`, `Service.DeletePurgedBlobs` | Every state blob of the purged workspace | As row 5 | Yes |
| 7 | Retention sweep | `pkg/inventory/sweep.go`, `Sweeper.finishOne`, through rows 5 and 6 | The blobs of a blob purge record, again | When the record is older than `BlobPurgeGrace` (24 hours) (D2) | Yes |
| 8 | Shred | ADR 151 decision 16 | Item key blobs and the blobs under the prefixes of a tombstone or a redaction | After a purge or a redaction commits. The `history` package has the types and the port, but the author found no production caller yet. | Yes, when built |
| 9 | OpenTofu pending content | `pkg/workspace/service.go`: `deleteExpiredBlobs`, `sweepUnreferenced`, `refusedRestore`, `copyBlob`, `storeContent`, `deleteOnCleanup`; `pkg/workspace/fence.go`: `dropFence` | Content of pending or refused versions, and blobs that no version names | Upload, restore, force-unlock, declare | No: a snapshot names finalized versions only. `DeleteStateVersion` deletes pending rows only. The only delete of finalized rows is the purge (`DeleteStateVersionsOfWorkspace`, row 6). |
| 10 | Refused journal batch | `pkg/stack/journal_entries.go`, `JournalEntries.Append` | The blob of a batch whose row the store refused | Journal append | No: no committed row names it |
| 11 | Item key cleanup | `pkg/secret/itemsealer.go`, through `ItemKeyBlobs.Delete` | The key blob of a seal whose SQL write failed | Seal | No: no committed row names it |
| 12 | Other stores | `pkg/job/artifactblobs.go` (`DeletePlanPart`), `internal/registry/service/publish.go`, `internal/unitimport/upload/upload.go` | Plan parts, registry objects, import uploads | Various | No: a snapshot does not name these blobs |
| 13 | Backup and restore | `pkg/backup/service.go` (`clearPartial`, `copyPlainBlob`), `pkg/backup/restore.go` (`putRestoreBlob`) | Objects in a backup target, or a bad copy that the restore just wrote | Backup, restore | No: not a live blob that a snapshot names |
| 14 | Restore as a durable operation | `pkg/backup/restore_operation.go`: the steps `copy-blobs`, `after-apply` and `cleanup` | No live key. It writes live keys with bytes that it checked first, and deletes only its temporary keys and its staging areas. It counts as running for the guard: while its row is checking or applying, rows 1 to 8 wait. | Restore | Yes, as a running party of the guard (amendment of 2026-10-27, below) |

Rows 1 to 8 need the guard. Rows 9 to 13 do not, for the reason in the last
column. Row 14 deletes no live key, but the guard must count it as running.

A new deleter must be added to this table, and the conformance tests below
must cover it.

Rows 2 and 3 have a separate bug that this guard does not fix: issue #1923.
The sweeps list the journal directories too, but keep only the keys that
checkpoint rows name. So they can delete journal batch blobs that
`update_journal` rows still name, with or without a backup. The guard of
this RFC has the fix of #1923 as a precondition (A0 below). The fix of #1923
must come first, because the bug can lose the result of an apply.

Row 3 has a second bug that this guard does not fix: issue #1941. The stack
sweep lists the blobs by the stack ID, but it read its keep set by the stack
name. A preview takes no stack lock, so a rename or a remove of the stack can
commit between the two reads. The keep set was then empty, and the sweep
deleted every checkpoint and journal batch blob of the stack that was older
than 1 hour. A soft-deleted stack, which the deleted-units API can restore,
lost its state in the same way. The sweep now reads the keep set by the stack
ID, and a stack row that is missing or soft-deleted is an error that stops the
sweep. The rule for every deleter: a deleter reads its keep set by the same
key that it lists by, and a missing owner row is an error, not an empty keep
set.

### The race

A backup takes its snapshot at S. A write commits at C after S, and that
write removes or replaces a row that names a blob. Then a deleter of rows 1
to 8 deletes the blob while the backup copies. The snapshot still names the
blob, so the copy reads a missing key.

For the purge (rows 5 to 7), C is the purge commit P. For the checkpoint
replace (row 1), C is the commit of the new checkpoint row. For the journal
build (row 4), C is the commit of the built checkpoint. Rows 2 and 3 delete a
blob that no row names now. An earlier commit after S could have removed the
name.

The purge rows have a 24-hour grace (`BlobPurgeGrace`), but it applies only
to row 7. Row 5 and row 6 delete at once after the commit. Rows 1 and 4 delete
at once after the commit. Rows 2 and 3 wait 1 hour from the write of the blob,
not from the commit that removed its name. So no grace protects a backup
today.

The race of rows 1 and 4 is the common one. It needs only a Pulumi update
that saves a checkpoint while a backup copies, and no purge. Issue #1920 holds
it as a bug, derived from code and not reproduced.

### What happens to the backup

This RFC read the copy code. `streamCopy` calls `Get` on the live store. A
missing blob makes `Get` fail, the step fails, and `failRow` marks the backup
`failed` with the text `read blob "<key>": ...`. When the target already
holds the blob from an earlier backup, the copy hashes the live blob again
(`hashStoredBlob`), and that read fails in the same way. The manifest is
written last, so a failed backup has no manifest, and `backup check` and
restore refuse it.

So this RFC found no path where the race gives a backup that has a manifest
and misses a blob. The effect today is a **failed backup**, not a silently
inconsistent one. That is fail closed, but the operator loses a backup. On an
installation with many Pulumi updates, backups could fail again and again.
The failure text names a blob, not the delete that removed it, so the
operator cannot see the cause. That is against "No magic".

ADR 151 decision 16 step 1 already decides a guard for row 8: "It waits until
no backup that started before the commit still runs." This RFC proposes one
rule that covers rows 1 to 8, in a form that needs no clock.

## Names used below

- **C**: the commit of a write that removes or replaces the last row that
  names a blob. For the purge, C is P.
- **D**: a delete of that blob by a deleter of rows 1 to 8.
- **T**: the time of the registry read that D makes before it deletes.
- **B**: the commit of the running backup row (`Registry.StartBackup`).
- **S**: the snapshot of the backup.
- **E**: the end of the backup row (`FinishBackup`, or `FailEndedBackups`).

A backup is at risk only when S < C and D runs while the backup still copies.

## Options

### Option 0. Do nothing

- **Correctness.** A backup never gets a manifest with a missing blob (see
  above). A backup fails when D runs between S and the copy of the blob.
- **Operator impact.** Lost backups, with an error text that does not name
  the cause. ADR 30 decision 2 stays not enforced, and ADR 151 decision 16
  needs a guard anyway.
- **Schema.** None.

### Option 1. A shared advisory lock

The backup takes a shared lock before S and keeps it until E. Each D tries
the exclusive lock and skips its delete when it does not get it.

- **Correctness.**
  - Sweep first (C and D before B): the backup takes the lock after D. Its
    snapshot is after C, so it does not name the blob. Correct.
  - Backup first (S before C): D cannot get the lock until E. Correct.
  - Both mid-way: correct only if every D of rows 1 to 8 takes the lock. The
    checkpoint save (row 1) then makes a lock call on each save.
  - Process crash: PostgreSQL releases a session advisory lock when the
    connection ends. The durable runner then gives the backup to another
    instance, which runs the write step again with a new snapshot
    (`clearPartial`, then `backup`). The new snapshot is after C, so the
    release is safe.
  - Connection loss: if the connection that holds the lock breaks while the
    backup copies, PostgreSQL releases the lock and the backup continues.
    The race is open again for the rest of that backup. PR #1917 states the
    same limit for the sweep lock. A pooler in transaction mode (for example
    PgBouncer) does not keep session locks at all.
- **Operator impact.** Blob deletes stop for the whole backup. The lock is
  visible only in `pg_locks`, not in the API, unless each skip is counted and
  shown. SQLite needs its own lock in the store (one process uses the file).
- **Schema.** None. A new port for the lock, with a suite for both dialects.
- **Comparable practice.** restic uses this model: `backup` takes a
  non-exclusive lock on the repository, and `prune` needs an exclusive lock
  and refuses to start while another lock exists. restic must find and remove
  stale locks after a crash, which shows the cost of a lock that lives outside
  the data.

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
  pin and the release. Rows 1 and 4 then read the pin table on each save.
  This option costs the most.
- **Comparable practice.** etcd keeps the pages of an open bbolt read
  transaction while compaction runs, so `etcdctl snapshot save` reads a
  consistent file. That is a pin, but inside the storage engine, at no cost
  to the caller. S3 Object Lock and legal holds are pins on objects. Both
  pin at the storage layer, not in an application table.

### Option 3. Wait while any backup runs

Before each D, read the backups registry. If a backup row is `running`, skip
the delete. The `backups` table already holds the running row and its
`started_at`, so the "backup running since" read model of the issue comment
exists.

This RFC proposes a stricter form than the issue comment. The issue comment
compares the start of the backup with the end of the grace. That compares
the clocks of two instances, and it does not cover the deletes that run at
once after the commit (rows 1, 4, 5 and 6). The proposed rule uses no clock:

> D deletes a blob that a committed row named only when a registry read that
> starts after the commit that removed or replaced that row finds no running
> backup.

The proof and its assumptions are in the next section.

- **Long backups.** The issue comment asks whether a backup that runs longer
  than the grace needs option 2. With this rule, it does not. D waits for
  the end of the backup, however long it runs. For row 7, the grace and the
  wait are two separate conditions: D needs both.
- **Operator impact.** No backup waits and no backup is refused. Blob deletes
  wait while any backup runs, also a backup that started after C. Such a wait
  is not necessary, but only one backup runs at a time, so it ends with that
  backup. Blobs that a delete skipped stay in the live store for longer (see
  "Where a skipped delete goes" below). The operator must see each skip (see
  "Visibility").
- **Cost.** One read of the `backups` table before each delete batch. For
  rows 1 and 4, that is one more read on each checkpoint save that replaces
  a key. The table is small, and a read of its running row can use an index
  on the state. The load harness (`make load`) must measure the cost before
  the change merges.
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
    them. That is a grace period, as `BlobPurgeGrace` and `sweepGracePeriod`
    are.
  - S3 lifecycle rules do not coordinate with a reader. AWS Backup for S3
    requires bucket versioning, and the usual practice is to expire
    noncurrent versions only after some days. That is also a grace, with no
    check of a running reader. A grace alone is not sufficient here, because
    a backup can run longer than any grace.
  - ADR 151 decision 16 step 1 already accepts this rule for the shred
    operation: "It waits until no backup that started before the commit
    still runs." Option 3 gives rows 1 to 7 the same rule, in the
    conservative form above.

### Option 4. Deletes and backups exclude each other

The sweep runs only when no backup runs. A backup refuses to start while a
sweep runs.

- **Correctness.** Two separate checks race: the sweep reads "no backup",
  the backup reads "no sweep", and both start. A correct form needs one lock
  for both, and so it becomes option 1. Rows 1 to 4 run inside Pulumi
  updates, which the server cannot refuse because a backup runs. So this
  option does not cover them.
- **Operator impact.** The worst of the four. A scheduled or manual backup
  is refused or waits for up to 30 minutes (`deletedSweepTimeout`). The
  backup is the safety net, and a refused backup can be missed. The purges
  also wait, so a unit can stay past the end of its retention.
- **Schema.** None. It uses the `SweepLocker` of PR #1917 (not on `main`
  yet) and the running row.

### Summary

| | Option 1 lock | Option 2 pins | Option 3 wait | Option 4 exclude |
| --- | --- | --- | --- | --- |
| Delete first | correct | correct | correct | correct |
| Backup first | correct | correct | correct | correct with one lock |
| Both mid-way | correct if every D takes the lock | open window from S to the pins | correct | races without one lock |
| Crash | lock released, takeover safe; connection loss opens the race | stale pins block deletes | waits for the takeover or `FailEndedBackups` | as option 1 |
| Backup stalls | none | none | none | up to 30 minutes, or refused |
| Delete stalls | for each backup | pinned keys only | while any backup runs | while any backup runs |
| Schema change | none, new lock port | new table and migration | none, one registry read | none |
| Covers rows 1 to 8 | only if every deleter takes the lock | yes, with a pin read on each save | yes | no, not rows 1 to 4 |

## The proof of option 3, and its assumptions

### Assumptions

A1 to A5 are facts of the code on `main` at 94b9669 or of an accepted ADR.
A0 is not a fact of the code yet. The implementation must keep each one, and
a conformance test must hold it (see "Conformance tests").

- **A0. The sweeps keep every blob that a row names, journal batches
  included (#1923).** Rows 2 and 3 delete a blob only when no committed row
  of any table that a snapshot reads names it: no `checkpoints` row and no
  `update_journal` row. On `main` at 94b9669 this is false: the sweeps read
  the checkpoint keys only. The proof below uses "the blob is unnamed" as its
  premise for rows 2 and 3. That premise holds only after the fix of #1923.
  Until then, rows 2 and 3 can delete a named blob, and no guard on the
  backup side can make that safe. One exception: a retried save that adopts
  a blob that its failed first attempt left under the same key
  (`adoptCheckpoint` in `pkg/stack/checkpoint_write.go`) makes an unnamed
  blob named again, so a sweep can find that blob unnamed before the retry
  commits. No snapshot names the blob before that commit, so the guard has
  nothing to protect there and does not change the case. It only makes the
  sweep delete later. Whether the sweep and the adopt race safely depends on
  `sweepGracePeriod` and the save, not on this guard, and this RFC does not
  analyze it.

- **A1. The row commits before the snapshot.** `Registry.StartBackup` stores
  the running row and starts the durable operation in one transaction. The
  operation runs only after that commit, and the snapshot is a step of the
  operation. So B < S for every snapshot of a backup, also for a snapshot of
  a takeover.
- **A2. The row stays running while a holder with the live claim copies.**
  The row ends only through `FinishBackup`, which checks the claim of the
  step and runs after the last copy, or through `FailEndedBackups`, which
  acts only on an operation that has ended failed or canceled. A takeover
  does not end the row. So for each copy by a holder with the live claim,
  the row is `running` from B to after that copy.
- **A3. One backup runs at a time across instances.** `CreateBackup` and
  `StartBackup` return `ErrAlreadyRunning` when another backup of the
  installation is running, on every instance on the database (ADR 30, ADR
  151). The proof does not need A3. A3 bounds the wait: at most one backup
  holds the deletes back at a time.
- **A4. The registry read sees every committed row.** D reads the registry
  in a new statement after C has committed. On PostgreSQL, a read-committed
  statement sees every row committed before it starts. On SQLite, one process
  holds the database and reads see every committed write. For row 7, the
  sweep first reads the blob purge record, so P committed before that read,
  and the registry read comes after it.
- **A5. The manifest is the last write, and a copy re-reads the live blob.**
  `backup` writes the manifest only after every blob copy has passed its
  size and MD5 check. A copy into a target that already holds the object
  still reads the live blob (`hashStoredBlob`). So a missing live blob always
  fails the copy, and a backup without a manifest is refused by `backup
  check` and restore.

### Proof for a holder with the live claim

Take a D whose read at T, with C < T, finds no running backup. Take any
backup, and any snapshot S of it that names the blob.

1. A snapshot names the blob only if S < C, because C removed the last row
   that names it.
2. By A1, B < S < C < T. So the row of that backup committed before T, and by
   A4 the read at T saw it.
3. The read found no running row, so the row had ended before T: E < T.
4. By A2, every copy by a holder with the live claim ran before E. So every
   such copy of the blob ran before T, and so before D.
5. A backup whose row commits after T has B > T > C, and by A1 its snapshot
   is after C. It does not name the blob.

The rule takes no lock, so a backup can start after the read at T and
before D. Step 5 covers that window: C is before T, so the snapshot of that
backup is after C and does not name the blob. D can delete it safely.

So no holder with the live claim reads the blob after D. This holds for each
order: delete first (step 5), backup first (steps 1 to 4), and both mid-way
(steps 1 to 4: a backup that is mid-way at T is running, so D does not
delete).

### Rows 2 and 3: no known commit

Rows 1 and 4 to 8 know their commit C: they run after it, in the same code
path. Rows 2 and 3 do not. They find a blob unnamed, and the commit that
removed its last name happened at some earlier time, maybe in another
process. Call K the read of the named keys (`CheckpointByUpdate` in
`sweepCandidates`, `ReferencedCheckpointKeys` in `sweepStackBlobs`, and,
after #1923, the read of the journal keys) that found the blob unnamed. The
listing of the blobs comes before K in both functions.

If the read K sees no row that names the blob, then by A4 the last such row
was removed by a commit C < K, or no row ever named the blob. In the second
case no snapshot names the blob. In the first case, the proof above holds
with C < K < T, but only if **T comes after K**.

The order matters. This interleaving loses a backup when T comes first:

1. The sweep reads the registry at T and finds no running backup.
2. A backup commits its row (B) and takes its snapshot (S). S names the blob
   X, because the checkpoint row of the update still names X.
3. A checkpoint save of the update commits a new key (C). No row names X now.
4. The sweep lists the blobs and reads the named keys (K). X is unnamed and
   older than the grace.
5. The sweep deletes X. The backup copies X later and fails.

With the order listing, K, T, delete, step 1 moves after step 4. The read at
T then finds the backup of step 2 running, and the sweep deletes nothing.
With C < K < T, steps 1 to 5 of the proof above apply.

So for rows 2 and 3, the registry read must come after the listing and after
every read of named keys that found the blob unnamed. A registry read that a
sweep made before its listing, for example at the start of a long sweep,
does not count.

### A holder that lost its claim

A holder can lose its claim and still run: for example a process that pauses
longer than the lease (`durable.DefaultLease`, 30 seconds) and then
continues. Its step does not stop at once, so it can still copy blobs from
its old snapshot. Two cases:

- The row is still `running`, because another instance took over. Then D
  still waits, by the proof above, and the late copy finds the blob.
- The row has ended, for example after a cancel and `FailEndedBackups`. Then
  D can delete while the late holder copies. The late copy reads a missing
  blob and fails. By A5 it writes no manifest. Its finish write fails the
  claim check (`durable.ErrClaimLost`), so it cannot change the row. The
  result is fail closed: no backup with a manifest misses a blob.

A late holder that copies every blob before D writes a complete manifest,
because every blob it names was present. The behavior of a late manifest
next to a takeover is a question of the backup service, not of this guard.
This RFC does not change it.

### Process crash

- **Crash of the backup holder.** The row stays `running`. After the lease,
  another instance claims the operation and runs the write step again with a
  new snapshot (`clearPartial`, then `backup`), or `FailEndedBackups` marks
  the row failed after the operation ends. D waits until then.
- **Crash between C and D.** The delete does not run. Where the blob goes
  then is the same as for a skipped delete (next section).
- **A failed registry read.** D does not delete (fail closed). The caller
  counts a failure with its error kind.

## Proposal

Option 3, in the clock-free form above, for rows 1 to 8 of the table.

1. **One guard for every in-scope delete.** A small port, for example
   `BackupWatch` with `RunningBackup(ctx) (backup.ID, bool, error)`, backed
   by the backups registry. Each deleter of rows 1 to 8 calls it after the
   commit and before the delete. Any failed read means no delete.
2. **No change to the backup, and no change to the commits.** The backup
   takes no lock and never waits. A purge, a checkpoint save and a journal
   build commit as today. Only the blob delete that comes after the commit
   can be skipped.
3. **Keep the graces.** Row 7 needs both conditions: the record is older
   than `BlobPurgeGrace`, and no backup runs. Rows 2 and 3 keep
   `sweepGracePeriod` and add the backup check.
4. **The order of reads in rows 2 and 3.** Each sweep lists the blobs, reads
   the named keys, then reads the registry, and only then deletes. The
   registry read comes after the listing and after every read of named keys
   that found a blob unnamed (see "Rows 2 and 3: no known commit"). A
   registry read from before the listing never allows a delete.
5. **A precondition.** The fix of #1923 merges before this guard: rows 2 and
   3 keep every blob that a row names, journal batches included (A0).

### Where a skipped delete goes

A skipped delete must not leave a blob that nothing collects.

- **Rows 5, 6 and 7.** The blob purge record already exists. A skipped D1
  leaves the record. Row 7 deletes the blobs after the grace and after the
  backup.
- **Rows 1 and 4.** The skipped blob becomes an orphan under the directory
  of the update. Row 2 collects orphans of the update on its next save, and
  row 3 collects orphans of the stack at the start of its next update. Both
  run the backup check too. A stack that never updates again keeps the
  orphan. That is the behavior today for an orphan after a crash between a
  blob write and its SQL commit. Open question 3 asks whether that is
  sufficient.
- **Row 8.** ADR 151 decision 16 gives the shred a durable operation that
  tries again.

### Visibility

Every skip must be visible to the operator:

- `inventory.SweepResult` gets a count of the records that wait for a
  backup, and the ID of that backup. The response of
  `POST /api/server/deleted-units/sweep` shows both, for example
  `"waitingForBackup": {"backup": "bkp-...", "blobPurges": 3}`.
- The sweep writes one INFO line for each sweep with waiting records, with
  the backup ID and the count, as `logPurged` does for purges.
- A purge (row 5 or 6) writes one INFO line when it leaves the blobs for the
  sweep because a backup runs, with the tool, the unit ID and the backup ID.
- Rows 1 to 4 run on each save, so a line for each skip would be noise. They
  count the skipped deletes in a metric with the deleter as a label, and
  write one INFO line for each backup that made them skip, with the backup
  ID and the count, when the backup ends.
- A metric gives the number of records that wait for a backup and the age of
  the oldest one, so an operator can alert when deletes wait for a long
  time.
- The audit entries do not change. The purge, the save and the build did
  happen. Only the blob delete waits.

### Conformance tests

New port method first (CLAUDE.md: a new port method gets conformance tests
before any adapter implements it):

- `storetest.RunBackup`, both dialects:
  - "the running backup read returns the running row": a started backup is
    returned with its ID.
  - "the running backup read returns none when every backup ended": rows in
    `succeeded` and `failed` do not count.
  - In the two-handle suite: "a backup started on one handle is running for
    the other handle". This holds A4 for two instances.
  - "the running row and its operation commit together": a start that fails
    leaves no row and no operation, and an operation is never claimable
    before its row is visible. This holds A1.
  - "a takeover leaves the row running": a claim that expires and is taken
    over does not end the row. This holds A2.
- `inventory` (fake ports, no sleep):
  - A record older than the grace, with a running backup: no call to
    `DeletePurgedBlobs`, the record stays pending, and the result counts it
    as waiting with the backup ID.
  - The same record after the backup ends: the sweep deletes the blobs and
    finishes the record.
  - A failed read of the running backup: no delete, `Failed` counts it, and
    the warning has an error kind and no blob key.
  - An unknown backup state in the read: no delete (fail closed).
- `stack` (fake ports, no sleep), one test for each of rows 1 to 4:
  - A checkpoint save that replaces a key while a backup runs commits, keeps
    the previous blob, and counts the skip.
  - The update sweep and the stack sweep with a running backup delete
    nothing, and delete the orphans after the backup ends.
  - A journal build while a backup runs commits the built checkpoint, keeps
    the batch blobs, and counts the skip.
  - The order of reads in rows 2 and 3: a fake registry and a fake checkpoint
    store record the order of the calls. Each sweep reads the registry after
    its listing and after its reads of named keys, and before its first
    delete.
  - The interleaving of "Rows 2 and 3: no known commit", for each of rows 2
    and 3: hooks in the fakes force the order registry read, backup start,
    checkpoint replace, listing, delete, as a sweep with the wrong order
    would run it. With the required order, the sweep sees the running backup
    and deletes nothing, and the blob that the snapshot names stays.
  - A0, for each of rows 2 and 3: a blob that only an `update_journal` row
    names is older than the grace, and no backup runs. The sweep keeps it.
    This test comes with the fix of #1923.
- `dustpan` lifecycle: a purge while a backup runs commits, does not delete
  the blobs, leaves the record, and logs the backup ID. The admin purge route
  and the sweep both take this path.
- Regression tests, both dialects. Start a backup and hold it after its
  snapshot (a blocking `Get` in the fault wrapper of the blob store,
  `internal/blob/blobfault`). Then:
  - purge the unit and run a sweep with a clock past the grace (issue #722);
  - save a second checkpoint of the same update (issue #1920);
  - build the checkpoint of a journaled update (issue #1920).

  In each case the blobs stay, the backup succeeds, and `backup check`
  passes. After the backup ends, the next delete or sweep removes them.
- Late holder: a holder loses its claim after its snapshot, the row ends, a
  delete runs, and the late copy fails with no manifest. This holds A5.
- Takeover: a backup row stays `running` after its holder stops. Deletes
  wait. The takeover runs the write step again with a new snapshot that does
  not name the deleted blob. After the row ends, the deletes run.

Backups are security code under CLAUDE.md, so the implementation PR also
needs a test for each defense-in-depth step that it adds, and a comment on
each check that looks redundant.

## Open questions

1. **Maintenance mode.** The issue says that background work pauses in
   maintenance mode. Maintenance mode refuses the checked writes, so the
   purge transaction does not run. This RFC did not confirm that row 7,
   which is not a fenced write, stops in maintenance mode. Should it also
   stop while a restore runs? A restore takes a backup before it applies
   (ADR 38 decision 8), so the guard of this RFC covers that backup, but a
   restore also swaps the `blob_purges` table.
2. **How long a wait can last.** A wait ends when the running row ends. A
   row ends through `FinishBackup` after the last copy, or through
   `FailEndedBackups`, which the backup service runs once in each lease
   period (`durable.DefaultLease`, 30 seconds) for an operation that ended
   failed or canceled. When the holder stops, another instance takes over
   the operation after the lease, and the row stays running until that
   backup ends. So the bound is the duration of the backup plus one lease
   for each takeover, as long as one instance runs the backup worker. If no
   instance runs it, the row stays running and the deletes wait for ever.
   This RFC proposes to show the wait (metric and log) and not to add a
   limit. Is a warning after a fixed age, for example two times the grace,
   sufficient?
3. **Orphans of a stack that never updates again.** Rows 2 and 3 collect
   orphans only when the stack saves or starts an update again. Is that
   sufficient, or does the skip of rows 1 and 4 need a durable record, as
   the blob purge record does for the purge? A durable record is a schema
   change.
4. **A precise form later.** The rule waits also for a backup that started
   after the commit. A precise form could compare a database sequence of the
   backup row with one of the commit, with no clock. Is the simple form
   sufficient until measurements show long waits?
5. **The shred operation of ADR 151.** Should the port of this RFC be the
   one that ADR 151 decision 16 step 1 uses, so that there is one guard?
   This RFC assumes yes.

## Outcome

Accepted on 2026-10-27 by the project owner: option 3, as the proposal
gives it. The decision is in
[ADR 280](../adr/0280-milestone-7-high-availability-on-one-database.md),
decision 7. The open questions above are not decided.

## Amendment of 2026-10-27: a restore counts as running

The design of the restore as a durable operation (milestone 7, work item W1)
adds row 14. A restore copies the blobs of the backup into the live store
before its swap, and the swap brings back rows that name them. A deleter that
read the guard before the restore, and stalls, can then delete a blob that
the restored data names. Neither order argument of the proof above applies,
because the restored snapshot has no order relative to the commit of the
deleter.

So the guard port answers "running" for a running backup **or a running
restore**: a restores row that is checking or applying. It is the same small
port and one more read of the same registry. This answers open question 1
for the restore: a restore counts as running.

The restore also follows these rules, because the blob store can lack
conditional writes:

- It checks the bytes of the backup before any write to a live key, through
  a temporary key of the restore and the claim token.
- It never deletes a live key. A failed check deletes only the temporary key.
- After the swap, and before it leaves maintenance mode, it checks that each
  blob that the restored data names exists, and copies a missing one again
  from the backup.

One window stays open: a deleter that read the guard before the restore
started, and stalls past the check after the swap, can still delete a blob
that the restored data names. The conformance tests of the restore cover a
deleter in flight across the restore.
