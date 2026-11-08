## Why

**Persona: a developer who approves changes.** They approve an OpenTofu plan
for the workspace `payments-prod`. While the job waits to apply, a colleague
runs `tofu apply` on the same workspace from a laptop. Then the approved job
applies.

What the developer expects: the server refuses to apply a plan that read
another state. The job plans again and asks for approval again. Today the
apply runs, and it can undo the colleague's change. Nothing in the API, the
console or the logs shows that the check had no data.

The user path this change must make work:

1. **API.** `GET /api/dustpan/v1/jobs/{id}` shows the job back in its plan
   phase. The reason names the state version that the plan read and the
   current one. The one call that helps: approve the new plan.
2. **CLI.** The tool's own CLI is not changed. A `tofu apply` or
   `pulumi up` from a laptop writes state as today. Its write now counts.
3. **Docs page.** `docs/guides/approval-rules.md` says that an apply refuses
   a plan whose state changed, and that the job plans again.
4. **Console.** The job page shows the replan and its reason from the API.
   No console change is in this change.

Two more paths read the same missing data and do nothing in a real
installation today:

- A write-back job never closes when its state version arrives.
- A run trigger of the kind `follows` never starts its plan when the unit
  that it follows applies.

**Root cause.** The job store reads two tables, `unit_state_versions` and
`unit_final_versions`. No product write path writes them. Only test hooks do:
`PutUnitStateVersion` in the store tests, and `SetStateVersion` and
`ObserveStateVersion` in the `jobtest` suite. The plan records `""`, the
dispatch compares it with `""`, and the check passes. The tests were green
because each test wrote the data itself.

## What changes

1. Each tool's state write records the state version of its unit in the same
   transaction. One store function, `publishUnitState`, is the only writer.
   No port method, store method or test hook can set it.
2. One table, `unit_state`, keyed by the unit ID, replaces the two tables.
3. A final state version closes waiting write-back jobs and feeds `follows`
   triggers, in the same transaction.
4. The conformance suites drive a real state write through the tool's own
   store.
5. One e2e step for each tool follows the path above with the real CLI.

Out of scope: the namespace ID change (ADR 220), the store move (RFC 103
step 8), concurrent applies on one state (ADR 302), and the stale behavior of
an apply of a change order (ADR 299 decisions 9 and 10 decide it; this change
only supplies the real state version that it reads).

## ADR check

**ADR 102 decision 7 (not owner-tagged).** "At the dispatch of an apply phase
that comes after a plan phase, the job store checks the plan in the
transaction of the dispatch with `job.CheckPlan`. The dispatch fails with
`ErrPlanStale` when ... the current state version of the unit is not the
version that the plan read ... The job then goes back to its plan phase
(`replan`), plans again, and asks for approval again." Changed by: ADR 190
(decisions 2, 7 and 8), ADR 353 (extends decision 7: the two plan parts must
agree), ADR 307, ADR 194, ADR 210, ADR 220, ADR 299 (decision 8). This change
implements decision 7 with real data. The replan is today's behavior of the
reconciler. No conflict.

**ADR 190 decision 9 (not owner-tagged).** "The job model checks that plan at
the dispatch with `job.CheckPlan` ... A stale plan fails the dispatch with
`job.ErrPlanStale`." Changed by: ADR 299 (decisions 4, 9, 10, 12 and 14;
owner-tagged there), ADR 222, ADR 225, ADR 300, ADR 344. This change does not
change the apply of a change order.

**ADR 299 decisions 9 and 10 (owner).** "In each level, the tree of the
merged commit must be the tree that was planned, or the server plans again."
"The approval floor (owner). At every level, a new delete, a new replace or
any operation that the approved set does not hold stops the apply for a new
approval." Not changed. They read the state version that this change makes
real.

**ADR 191 decision 7 (not owner-tagged).** "A unit plan is stale when the
current state version of its unit is not the version that it read." Changed
by: ADR 222, ADR 225, ADR 227, ADR 299 (decisions 4, 5 and 6), ADR 300. Not
decision 7. Implemented with real data.

**ADR 267 decision 12 (PM, under the owner's delegation).** "For OpenTofu
over the state backend, any final state version counts." Changed by: ADR 307,
ADR 273, ADR 299, ADR 310, ADR 338; none changes decision 12. Triggers get the
data. The filter of decision 12 stays.

**ADR 12 and ADR 13.** Shared code stays tool-neutral. "Each tool package
publishes to it in the same transaction as its own writes." The record is a
tool-neutral version for each unit ID, written by each tool's store, in the
same way as the inventory.

**ADR 362 decision 1 (owner).** "Every stored reference to an entity uses the
stable ID of the entity when a user can rename the entity, or delete and
recreate it." The record is keyed by the unit ID, not by the name-keyed
inventory entry. Changed by: ADR 363 (decision 2 only).

**ADR 322 decisions 1 and 2 (owner).** One baseline for each dialect until
the first tag, edited in place. Changed by: ADR 325, ADR 326, ADR 362. The
baselines are edited in place. No numbered migration.

**ADR 42 (not owner-tagged; the owner confirmed portable backups on
2026-11-08, ADR 362).** A portable backup format. The restore derives
`unit_state` from the restored rows. `unit_state` is not in the table list.

**ADR 302 decision 2 (owner).** "The server serves each job the state version
that the plan used as its base." Later work; this change gives it a real
version.

No conflict with an owner-tagged decision. The owner accepted this design on
2026-11-08 ("accept the DRV-1 design").

## Open questions

none

Decided by the PM on 2026-11-08 with the ADR check, and reviewed by the
implementation lead:

1. The Pulumi version is the digest of the stored checkpoint, on each
   checkpoint write that commits. A write with the same digest keeps the
   version. A checkpoint marked invalid changes it.
2. OpenTofu "final" is the release of the state lock after a write. A lock
   and unlock with no write makes no new final version. A write that the
   workspace allows with no lock is final by itself.
3. `final_job` comes from the job ID of the principal of the request. A write
   from a laptop records an empty job and commit. The close of a write-back
   job does not match on the job.
4. The record is keyed by unit ID, not on the inventory entry.
