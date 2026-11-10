## Context

The owner accepted this design on 2026-11-08. The proposal gives the user
path and the ADR check.

## The record

One table, `unit_state`, keyed by the unit ID (with `namespace_id`), holds
`version_id` (the stale check) and the final-version fields that triggers
read (`final_version_id`, `final_commit`, `final_job`, `final_at_ms`). It
replaces `unit_state_versions` and `unit_final_versions`.

It is not a column of the inventory entry: the inventory key is still
(namespace, tool, name) (audit NK-15), so a rename, or a delete and a new
unit with the same name, would carry or lose the version (ADR 362). Keyed by
unit ID, the record needs no part of the NK-15 or ADR 220 work.

The table was never the bug. The bug was that no product path wrote it.
What changes is who may write it (next sections).

## The version ID of each tool

- OpenTofu: the ID of the state version that `SetWorkspaceCurrent` makes
  current. Each such write changes the version. The release of the state
  lock marks it final for write-back and triggers.
- Pulumi CLI: the digest of the checkpoint as stored, on every checkpoint
  write that commits. Not the update ID: `PutCheckpoint` overwrites the
  checkpoint of a running update in place, so the update ID alone would keep
  a plan fresh after later saves of the same update. A failed or canceled
  update changes the state through its checkpoints, so its writes change the
  version too. A checkpoint with `invalid=1` also changes the version (fail
  closed: the state is not the one the plan read). A write with the same
  digest keeps the version, so a plan stays fresh after a run that changed
  nothing.
- Final (Pulumi): the end of an update, whatever its result. Triggers keep
  the filter of ADR 267 decision 12 (an update of the kind `update`) on top
  of it; the write-back close reads the result as today.

Task 1 checks these points against the stores before any other task starts.

## The one write path

A store-internal function, `publishUnitState(ctx, q, unitID, version, final)`,
is the only writer of `unit_state`. It is called at the points that move the
state, inside their transaction, in each dialect:

- OpenTofu: next to `SetWorkspaceCurrent`, in the adapter, not in each
  caller. Two call sites serve every caller: `writeFinalize`
  (sqlite/workspaces.go:1003; postgres :973) for the finalize and the
  restore or rollback, through `FinalizeStateVersion` (sqlite :682;
  postgres :660) and `FinalizeStateVersionExpecting` (sqlite :698;
  postgres :673); `publishFencedState` (sqlite :794; postgres :765) for
  the fenced force unlock, through `FenceAndUnlock` (sqlite :716;
  postgres :689). The TFE-compatible API, the HTTP backend and
  `Restore`/`RestoreExpecting` all reach one of the two.
- Pulumi CLI: both checkpoint write paths, `PutCheckpoint`
  (pkg/stack/updates.go:1171; store sqlite/updates.go:294, postgres :350)
  and `PutBuiltCheckpoint` from the journal (pkg/stack/journal_build.go:282;
  store sqlite/journal.go:609, postgres :646).
- Pulumi CLI final: the end of an update, whatever its result, in
  `SaveUpdate` (sqlite/updates.go:162; postgres :179), next to
  `endMutatingUpdate` (sqlite :196; postgres :206), which runs only for a
  mutating update that ended and releases the stack lock. `Complete`
  (pkg/stack/updates.go:484) and `Cancel` (:494) both reach it; a failed
  update is `Complete` with `StatusFailed` (pkg/stack/update.go:131).
- OpenTofu final: every store method that releases the state lock, in both
  dialects, calls `publishUnitState` with `final=true`: `SaveWorkspace`
  (sqlite/workspaces.go:221; postgres :203), which serves the
  TFE-compatible unlock and the HTTP backend UNLOCK; `SaveReleased`
  (sqlite :208; postgres :193) and `ReleaseUnfenced` (sqlite :182;
  postgres :174), the force unlock without a state write; and
  `FenceAndUnlock` (sqlite :716; postgres :689), the force unlock with a
  state write. Two cases:
  - A lock and an unlock with no state write in between (a plan, a
    canceled run) makes no new final version: the release publishes
    `final=true` only when the current state version differs from the last
    final version of the unit.
  - A write that the workspace allows without a lock (allowed unlocked
    write; the flag is `UnlockedWrite`, set at pkg/workspace/service.go:2618
    and pkg/workspace/write_state.go:371) has no release, so the write
    itself is final at the `writeFinalize` call above. Otherwise a
    write-back job of such a workspace would never close.
- Portable backup restore restores the `workspaces` and `checkpoints` rows
  through a staging image and one swap (sqlite:
  backup_stage_portable.go:23, restore.go:143; postgres:
  backup_stage_portable.go:21, restore.go:147). In the same swap
  transaction it derives `unit_state` from the restored rows (the current
  state version ID; the digest of the last checkpoint) and never restores a
  stored version value. `unit_state` is not in the portable table list.
  The derive goes through `publishUnitState` with the restore kind. A
  restore sets the version only: it records no final version and closes no
  write-back job. A later release of the lock with no write makes the
  restored version final, because the final version differs from it.

What stops the bug class from coming back:

- No port method sets a version. `job.Store.ObserveStateVersion`, the
  `jobtest` harness method `SetStateVersion` and `PutUnitStateVersion` are
  deleted. The job store only reads.
- No exported method of a port or a store sets a derived fact. A store test
  reads `queries.sql` of each dialect and fails when a query other than
  `PublishUnitState` writes `unit_state` (tests below). This is a structure
  test, not a linter (rule 77: no new lint rule where a structure exists).
- The conformance suites of the job store get the version by writing state
  through the tool's own store in the harness (`Harness.WriteState(unit,
  final)`, implemented by each store test with the real workspace or stack
  store). A store that forgets to call `publishUnitState` fails the suite.
- The in-memory fake of the job store in `pkg/job/jobtest/mem_test.go`
  stops faking state versions. Tests that need a version use a real SQLite
  store.

## Write points (checked)

Task 1 checked every point of "The one write path" against the code, in both
dialects. The lines are the code at this change.

1. `SetWorkspaceCurrent` and every caller. Two call sites per dialect:
   `publishFencedState` (sqlite/workspaces.go:794; postgres :765) and
   `writeFinalize` (sqlite/workspaces.go:1003; postgres :973). The port
   methods are `FinalizeStateVersion` (sqlite :682; postgres :660),
   `FinalizeStateVersionExpecting` (sqlite :698; postgres :673) and
   `FenceAndUnlock` (sqlite :716; postgres :689). The domain callers are
   `Upload` (pkg/workspace/service.go:1361), `finalizeStream`
   (pkg/workspace/write_state.go:291), `finalizeRestore`
   (pkg/workspace/service.go:2220 and :2223), reached from `Restore`
   (:1759) and `RestoreExpecting` (pkg/workspace/rollback_change.go:188),
   and `releaseWithFence` (pkg/workspace/fence.go:81).
2. Every store method that releases a state lock. SQLite: `SaveWorkspace`
   (sqlite/workspaces.go:221), `SaveReleased` (:208), `ReleaseUnfenced`
   (:182), `FenceAndUnlock` (:716). PostgreSQL: `SaveWorkspace`
   (postgres :203), `SaveReleased` (:193), `ReleaseUnfenced` (:174),
   `FenceAndUnlock` (:689). The TFE-compatible unlock
   (internal/api/tfe/locks.go:222) and the HTTP backend UNLOCK
   (internal/api/tofuhttp/locks.go:421) reach `SaveWorkspace` through
   `Unlock` (pkg/workspace/service.go:874), `UnlockLock` (:889) and
   `change` (:2823). The force unlock reaches `SaveReleased` (:1844),
   `ReleaseUnfenced` (:2867) and `FenceAndUnlock`
   (pkg/workspace/fence.go:81). `FenceAndUnlock` is also a
   `SetWorkspaceCurrent` point (item 1), so one call with `final=true`
   covers both. A finalize does not release the lock; the unlock that
   follows it is a `SaveWorkspace` write.
3. The allowed unlocked write. The decision is in `declareOverrides`
   (pkg/workspace/service.go:1207, the unlock branch at :1225), called by
   `BeginState` (:1089) and `checkStreamAdmission`
   (pkg/workspace/write_state.go:126, the call at :133). The write stores
   `UnlockedWrite` (pkg/workspace/service.go:2618;
   pkg/workspace/write_state.go:371), and the store checks it in
   `checkFinalizeHolder` (sqlite/workspaces.go:915; postgres :886) and
   `checkUnlockedSwitches` (sqlite :1096; postgres :1065).
4. The Pulumi checkpoints. The service calls `PutCheckpoint` in
   `commitCheckpointRow` (pkg/stack/updates.go:1171), stored in
   sqlite/updates.go:294 and postgres/updates.go:350, and
   `PutBuiltCheckpoint` from the journal (pkg/stack/journal_build.go:282),
   stored in sqlite/journal.go:609 and postgres/journal.go:646. The row
   holds `digest` and `invalid` already
   (sqlite/migrations/0168_baseline.sql:485 and :486;
   postgres/migrations/0168_baseline.sql:160 and :161). The service sets
   the digest as the SHA-256 of the deployment (pkg/stack/updates.go:551;
   pkg/stack/journal_build.go:197) and `codec.ToCheckpointRow` keeps it
   (pkg/store/internal/codec/updates.go:197 and :202). The version needs no
   new digest. Each write overwrites the row of its update
   (pkg/store/sqlite/queries.sql:367; pkg/store/postgres/queries.sql:442).
5. The end of a Pulumi update. `Complete` (pkg/stack/updates.go:484) and
   `Cancel` (:494) reach `SaveUpdate` (sqlite/updates.go:162;
   postgres :179) and `endMutatingUpdate` (sqlite :196; postgres :206),
   which releases the stack lock. A failed update is `Complete` with
   `StatusFailed` (pkg/stack/update.go:131). Neither method publishes a
   version today.
6. The portable backup restore. SQLite: `StagePortable`
   (pkg/store/sqlite/backup_stage_portable.go:23), `loadPortableTables`
   (:164), `loadPortableTable` (:194), `insertBatch` (:239), `swapStaged`
   (pkg/store/sqlite/restore.go:143), `applyStagedFence` (:292).
   PostgreSQL: `StagePortable`
   (pkg/store/postgres/backup_stage_portable.go:21), `loadPortableStaging`
   (:94), `loadPortableTable` (:135), `insertPortableBatch` (:181),
   `swapStaging` (pkg/store/postgres/restore.go:147), `swapTx` (:171),
   `swapIn` (:220).
7. The readers and the writers of `unit_state_versions` and
   `unit_final_versions`. `PutUnitStateVersion` (sqlite/jobs.go:45;
   postgres/jobs.go:48). `StateVersion` on `jobTx` (sqlite :452;
   postgres :431). `FinalVersion` on `jobTx` (sqlite :466; postgres :445)
   and on the job store (pkg/store/internal/jobstore/store.go:273).
   `PutFinalVersion` on `jobTx` (sqlite :485; postgres :466).
   `ObserveStateVersion` (pkg/store/internal/jobstore/apply.go:244) and
   `observeIn` (:270, which calls `PutFinalVersion` at :287). The harness
   method `SetStateVersion` (pkg/job/jobtest/jobtest.go:76,
   pkg/job/jobtest/mem_test.go:224, sqlite/jobs_test.go:180,
   postgres/jobs_test.go:182). The queries `GetUnitStateVersion`,
   `PutUnitStateVersion`, `GetUnitFinalVersion` and `PutUnitFinalVersion`
   (sqlite/queries.sql:3903-3926; postgres/queries.sql:4309-4332). The
   tables are in each baseline (sqlite/migrations/0168_baseline.sql:916 and
   :923; postgres/migrations/0168_baseline.sql:1365 and :1347).
8. `identity.Principal.JobID` in the handlers. The workspace: the TFE
   declare `createStateVersion` (internal/api/tfe/state_write.go:168) and
   the restore `restoreStateVersion` (internal/api/tfe/states.go:434) have
   `p`; the TFE streamed finalize `uploadState`
   (internal/api/tfe/state_write.go:286) does not. The HTTP backend has
   `c.principal` (internal/api/tofuhttp/tofuhttp.go:142) in `postState`
   (internal/api/tofuhttp/state_write.go:36), the unlock
   (internal/api/tofuhttp/locks.go:343) and the force unlock
   (internal/api/tofuhttp/state_write.go:168). The stack: `startUpdate`
   (internal/api/pulumi/updates.go:412) and `cancelUpdate` (:627) have `p`;
   the checkpoint writes and `complete` take a `LeaseCall`
   (internal/api/pulumi/routes.go:57, :380, :402, :414, :426). The type
   `StateOrigin{Job, Commit}` fits in `pkg/identity`, the core package that
   holds the job claim: `pkg/stack`, `pkg/workspace` and `pkg/job` all
   import it. `pkg/job` cannot hold the type: `pkg/job` imports `pkg/stack`
   through `pkg/maintenance`.
9. The reconciler replan reason. `replan` (pkg/job/reconcile.go:622)
   makes the text: the audit reason at :634, "the plan is stale: the job
   plans again", through `entry` (:691). `Change.Reason` stays empty
   (:631), because it is the reason of a terminal state
   (pkg/job/store.go:22). The refusal errors of `CheckPutPlan`
   (pkg/job/record.go:505) and `CheckPlan` (pkg/job/artifact.go:159) name
   the two versions today.

Difference: the restore writes no live `workspaces` or `checkpoints` row.
`StagePortable` builds a staging image and the swap applies it.
`unit_state_versions` and `unit_final_versions` are portable tables today
(pkg/backup/backup.go:171-172; pkg/backup/portable_jobs.go:43 and :53;
pkg/store/sqlite/backup_jobs.go:51 and :55;
pkg/store/postgres/backup_jobs.go:46 and :51), so a restore loads the stored
version values and derives nothing. The derivation, and the removal of the
two tables from the list, are new work.

Difference: the checkpoint write and the journal checkpoint have no
principal: both routes are `ScopeUpdateLease` and take a `LeaseCall`
(internal/api/pulumi/routes.go:57, :402, :414 and :426). The server uses
the job ID of the update at the start (internal/api/pulumi/updates.go:431)
and does not keep it: the `Update` row holds `TokenID` only
(pkg/stack/update.go:270-273). Task 2.3 must keep the job ID of the update,
or of its lease, to report it at a checkpoint write.

Difference: the TFE-compatible streamed finalize has no principal. The
declare `createStateVersion` has `p` (internal/api/tfe/state_write.go:168)
and the finalize `uploadState` does not (:286). The origin of that finalize
must come from the declared version, or the design must name the declare as
the origin point.

Difference: the end of a Pulumi update is final in "The version ID of each
tool" and absent from the list of publish points and from task 2.2. The
point is `SaveUpdate` (sqlite/updates.go:162; postgres :179) with
`endMutatingUpdate` (sqlite :196; postgres :206).

Difference: the Pulumi commit has no source today. The create body reads
`options.dryRun` only (internal/api/pulumi/updates.go:35-46) and no row
holds a commit, so `git.head` is new work in task 2.3.

## Who wrote the version (final_job, final_commit)

`job.StateVersion` carries `Job` and `Commit`. On the real path:

- The HTTP handler has the principal of the request. Its `JobID`
  (`identity.Principal.JobID`) is the job whose job token the tool used, or
  empty for any other credential.
- The tool service puts a value `StateOrigin{Job, Commit}` (tool-neutral
  core type, in `pkg/identity`) into the write argument that it hands
  to the store, at each publish point: the state version of a workspace
  write, the lock release, the checkpoint write and the journal checkpoint.
  `publishUnitState` takes it as a parameter. The store never reads a
  request context.
- Commit: the commit that the tool reported with the write (the Pulumi
  update's `git.head` metadata), else the commit of the job's spec when
  `Job` is set, else empty. OpenTofu reports none.
- A laptop write records an empty Job and an empty Commit.
- The write-back close does not match on Job: it matches the unit, a final
  version and a time at or after the job's delivery (`job.Job.ClosedBy`,
  unchanged). Job and Commit only set the verification of the closed job:
  an empty Job gives the result "not from the delivered commit", as today.
- A restore records an empty origin.

## Failure behavior

- The state write and the version record are one transaction. A failed record
  fails the state write; the client retries as it does for any failed write.
- A unit with no state yet has the version `""`. A plan of a unit with no
  state reads `""`. If a state write lands before the apply, the versions
  differ and the dispatch refuses. This is the intended behavior.

## Tests (each guard has a test that fails without it)

1. Store suites, one row per caller of the write points (finalize,
   restore, rollback, `PutCheckpoint`, `PutBuiltCheckpoint`): a state write changes the version that the job
   store reads; an intermediate write changes the stale check; only a final
   write closes a write-back job and changes the trigger version.
2. Job suite: plan, then a state write through the tool store, then
   dispatch: `ErrPlanStale`. Without the state write: dispatch passes.
3. OpenTofu final: lock and unlock with no write gives no new final
   version; an allowed unlocked write is final; a release after a write is
   final with the origin of the releasing request.
3b. Origin: a job-token write records its Job; a laptop write records an
   empty origin, and still closes a waiting write-back job, unverified.
3c. Restore conformance: plan, then a portable restore of another state,
   then dispatch: `ErrPlanStale`.
4. Server test: a final write of unit A makes the real dispatcher
   (`pkg/dustpan/triggers.go`) fire the `follows` trigger of unit B. Moved
   to #3005.
5. e2e (OpenTofu, Pulumi CLI): Priya's path with the real CLI. The second
   apply from a "laptop" uses the CLI against the state backend directly.
6. A store test of each dialect reads `queries.sql` and every Go file of the
   store package. It fails when SQL that writes `unit_state` is outside
   `publishUnitState` and its restore helper, or when a function other than
   `publishUnitState` calls a query or the helper that writes the table.

Status in the code: tests 1, 2, 3, 3b, 3c and 6 are store and suite tests in
each dialect. Test 4 is moved to #3005, with the wiring of the `follows`
trigger after a state write. Test 5 has no e2e step yet: the server starts
plan jobs only (`pkg/dustpan/jobservice.go`), the approve of a job returns
`ErrUnsupported` (`pkg/job/service.go`), and the jobs API has no approve
route. The replan reason is covered by the store and suite tests.

## Notes on the code

Each point below is a place where the code does not match the text above, or
a decision that the text leaves open, and what the code does.
### The signature of `publishUnitState`

`design.md` writes `publishUnitState(ctx, q, unitID, version, final)`. The
write also needs the namespace, the origin and the time of the write, so the
function would take eight arguments. The lint rule for the count of arguments
allows five, and this change adds no lint exception. The function therefore
takes one `unitStateWrite` value: the namespace, the unit ID, the version, the
final flag, the origin and the time.

Nothing else changed. The function is unexported in each dialect. It is the
only caller of `PublishUnitState`, and every write point calls it in the
transaction of its own write.

### The close of the jobs that wait for a state version

`design.md` says that a final version runs the close of the waiting write-back
jobs in the same transaction, as `observeIn` does today. The close is
reachable from the workspace and the stack transaction, so this step needed no
substitute. `jobstore.CloseAwaiting` takes the narrow `CloseTx` interface
(`Now`, `Awaiting`, `UpdateJob`, `StartOperation`), and each dialect gives it
a `stateCloseTx` over the queries of its transaction, as the item keys do.

Two consequences:

- The closing record names the unit as the job itself names it
  (`job.Spec.Unit`). The caller knows the unit by its stable ID only, and the
  job's own unit object stays valid after a rename. An inventory lookup by
  unit ID would add a read to the write path.
- The close reads the clock only when a job waits. A write that publishes a
  final version and closes no job pays no extra query.

### The SQLite `PublishUnitState` carries the final fields

sqlc does not rewrite a query parameter inside the `DO UPDATE SET` clause for
the SQLite engine. The PostgreSQL engine does. The SQLite query therefore
takes the four final fields as parameters, and the caller reads the stored row
with `GetUnitState` and passes its values when it publishes no final version.
The write transaction of the SQLite store is IMMEDIATE, so no other write runs
between that read and the upsert. The PostgreSQL query keeps the conditional
update, so it needs no read.

### `unit_state` in the portable backup

`design.md` says that `unit_state` is not in the portable table list and that
a restore derives it. A backup leaves the table out too, so the table is in
`EmptiedTables` and in the emptied-table list of each dialect, next to the
other tables that a restore leaves empty and makes again.

### The place of `unit_state` in the SQLite baseline

The SQLite baseline holds the objects in the order in which they were
created, not in name order. `unit_state` replaces the two removed tables in
their slot, between `job_freezes` and `saved_views`. The PostgreSQL baseline
is in name order, and `unit_state` sits between `unit_settings` and
`update_events` there.

### Decisions the design leaves open

- The release of a lock. The store decides whether a save releases the lock
  with a read of the stored row before the compare-and-swap (SQLite:
  `GetWorkspaceByID`, PostgreSQL: `LockWorkspaceForFinalize`). The written row
  carries the record of an earlier release as well, so the new row alone
  cannot tell a release from the record of one. A stored row that is not there
  holds no lock, so a save that creates a workspace releases nothing.
- The version of an invalid checkpoint. The version of a checkpoint is the
  digest of its deployment. A checkpoint whose row is marked invalid carries
  the digest with the fixed suffix `:invalid`, so it never equals the version
  of a valid write, and a plan of the state that it replaced is stale.
- The derived versions of a restore. A restore takes the version of each
  workspace from its current state version ID, and the version of each stack
  from the digest of the last checkpoint of its last update that has one, as
  `GetLatestCheckpoint` reads it. A stack with no checkpoint holds no state
  and gets no row: the job store reads the empty version for it, as it does
  for a unit with no row. A workspace with no current state version gets a row
  with the empty version. The final fields of each derived row are empty.
- The time of a checkpoint write. That write pays no round trip for the time
  of the version: it uses the clock that the upsert returned, as the check of
  the attempt window does.

### Points where `design.md` and the task list differ

`design.md` says that the end of a Pulumi update is final, and it records that
the list of publish points and the task list do not name it. The code
publishes it in `SaveUpdate`, next to `endMutatingUpdate`, as `design.md`
says.

### Shapes that the lint rules required

- The pair (digest, invalid) of a checkpoint comes to its version through the
  method `checkpointDigest.stateVersion`. The lint rule against a flag
  parameter refuses a function that takes the two values.
- The publish helpers name their namespace parameter `namespaceID`. The lint
  rules against shadowing an import refuse the name `namespace`, and these
  files import the `namespace` package.
- `saveWorkspaceRow`, the close of a waiting job and the read of the derived
  versions are split into small helpers (`lostWorkspaceSave`, `closeRow`,
  `scanUnitVersions`). The lint rules against deep nesting and high complexity
  refused the longer bodies. The behavior did not change.

### The origin of a write (step 3)

- The domain service carries the job of a job token in the context
  (`workspace.WithJob`), and `originFrom` turns it into a `StateOrigin` with
  an empty commit. The store takes the commit from the spec of the job in the
  transaction of the publish.
- The TFE declare stores the job in the new column `state_versions.origin_job`.
  The finalize reads it back (`stateVersionOrigin`).
- A release reads the origin from the new field `Workspace.Origin`. The store
  does not keep it: a read gives it as zero. The field exists only so that the
  store method keeps its signature.
- The Pulumi update row has two new columns, `job_id` (set at the start) and
  `git_head` (from `metadata.environment["git.head"]` of the create body).
  Every checkpoint write, the delta save included, and the end of the update
  publish `Update.Origin()`.
- `updates` and `state_versions` add their columns at the end. Neither table
  keeps its columns in name order.
- A restore passes an empty origin, and so does the release that ends it.
- The replan reason is the text of the `CheckPlan` error, in the job and in
  the audit entry.

### The conformance suite writes state (step 3, first half)

- The harness gives the write through the new field `Harness.WriteState` of
  the function type `StateWriter`. The `Domain` interface no longer has
  `SetStateVersion`. The in-memory harness leaves the field nil, and the suite
  runs the cases that need a state version only when the field is not nil. No
  case skips.
- The SQL harnesses implement the OpenTofu path: the write locks the
  workspace, declares a state version, records the upload attempt, finalizes
  the version, and, for a final write, releases the lock. The version is the
  ID of the state version, so the suite cannot choose its text. A Pulumi unit
  returns `identity.ErrInvalidObject`: no case of the suite writes the state
  of a Pulumi unit.
- The suite cannot choose the time of a version. The cases of a merge, a late
  delivery and the final version take the time of the final version that the
  store records.
- The final-version round-trip case writes with no origin, because the
  harness has no job token. It checks an empty job and commit and a time that
  is not zero. The origin of a job-token write stays in the store tests.
- The fence-of-another-namespace case no longer calls
  `ObserveStateVersion`: a state write is a write of the store of the tool.
  The stale-fence case proves that the write is refused.
- The first write of a unit uses the lineage `lineage-1` and serial 1. A later
  write takes the serial after the current state version and its lineage.
- The in-memory job store no longer keeps a current state version. Task 2.4
  removed its final version map too (see below).

### The old writers of state versions (task 2.4)

Deleted:

- `job.Store.ObserveStateVersion`, with the close loop that it ran.
  `jobstore.CloseAwaiting` stays: every state write that publishes a final
  version calls it in its own transaction.
- `PutUnitStateVersion` on the SQLite and the PostgreSQL store.
- `PutFinalVersion` on `jobstore.Tx` and on both dialects. `observeIn` went
  with it, because only `ObserveStateVersion` called it.
- The final version map of the in-memory store in `pkg/job/jobtest/mem_test.go`,
  its `ObserveStateVersion`, and its `close` and `closeLate` helpers.
- `Store.logger` in `pkg/store/internal/jobstore/store.go`, because only
  `ObserveStateVersion` read it.
- The matching fakes in `pkg/job/service_test.go` and
  `internal/api/jobs/routes_test.go`.

No sqlc query block was left to remove. The old queries for
`unit_state_versions` and `unit_final_versions` were already gone, and
`make generate` produced no change.

The job store already reads both versions from `unit_state`:
`jobTx.StateVersion` and `jobTx.FinalVersion` call `GetUnitState`, and
`store.FinalVersion` reads through `jobTx.FinalVersion`. No read used another
source.

Points where the code did not match `design.md`:

- `design.md` does not say that the in-memory store stops implementing
  `job.FinalVersionReader`. With its final version map gone, it has no source
  for a final version, and a method that always answers "none" trips the lint
  rule against a constant result. The method is deleted. The suite runs its
  three reader-only cases only for a harness whose job store implements the
  reader. The SQLite and PostgreSQL harnesses still run them.
- `design.md` says that the close of the waiting jobs stays reachable in the
  state write transaction. It is: `publishUnitState` calls
  `jobstore.CloseAwaiting`. The update-time test called the old store method,
  so it now calls `jobstore.CloseAwaiting` with its fake transaction.
