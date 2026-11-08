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
  caller. That one point serves finalize and restore or rollback
  (sqlite/workspaces.go:794 and :1003; postgres :765 and :973), so the
  TFE-compatible API, the HTTP backend and `Restore`/`RestoreExpecting` all
  get it.
- Pulumi CLI: both checkpoint write paths, `PutCheckpoint`
  (pkg/stack/updates.go:1171) and `PutBuiltCheckpoint` from the journal
  (pkg/stack/journal_build.go:282, sqlite/journal.go:609).
- OpenTofu final: every store method that releases the state lock (the
  TFE-compatible unlock and the HTTP backend UNLOCK, `FenceAndUnlock`, the
  force unlock), in both dialects, calls `publishUnitState` with
  `final=true`. Two cases:
  - A lock and an unlock with no state write in between (a plan, a
    canceled run) makes no new final version: the release publishes
    `final=true` only when the current state version differs from the last
    final version of the unit.
  - A write that the workspace allows without a lock (allowed unlocked
    write) has no release, so the write itself is final. Otherwise a
    write-back job of such a workspace would never close.
- Portable backup restore writes `workspaces` and `checkpoints` rows
  directly. In the same restore transaction it derives `unit_state` from the
  restored rows (the current state version ID; the digest of the last
  checkpoint) and never restores a stored version value. `unit_state` is
  not in the portable table list. When `final` is true it also runs the close of awaiting
write-back jobs (today `observeIn`) in the same transaction.

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

## Who wrote the version (final_job, final_commit)

`job.StateVersion` carries `Job` and `Commit`. On the real path:

- The HTTP handler has the principal of the request. Its `JobID`
  (`identity.Principal.JobID`) is the job whose job token the tool used, or
  empty for any other credential.
- The tool service puts a value `StateOrigin{Job, Commit}` (tool-neutral
  core type; task 1 names its package) into the write argument that it hands
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
4. Server test: a final write of unit A starts the `follows` plan of unit B
   through the real dispatcher (`pkg/dustpan/triggers.go`).
5. e2e (OpenTofu, Pulumi CLI): Priya's path with the real CLI. The second
   apply from a "laptop" uses the CLI against the state backend directly.
6. A store test reads `queries.sql` of each dialect and fails when a query
   other than `PublishUnitState` writes the version columns.
