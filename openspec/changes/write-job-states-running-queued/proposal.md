---
version: 2
---

Issue: #1198, #1199.

## Why

**Persona: developer.** A Developer runs plans and applies on runners and reads the stacks and workspaces of a namespace. The Developer needs to see whether a unit waits, runs, waits for approval, needs an apply, or the latest change failed, so the Developer knows the one next step without opening each job.

The user path this change must make work:

1. **API.** Run a plan or apply job for a stack or workspace through the API or the runner.
2. **API.** List units of the namespace and read the states of the unit.
3. **E2e.** Run the inventory check in the e2e suite against the real CLI for one Pulumi and one OpenTofu change.
4. **Docs page.** Read the unit states docs page for the meaning of each badge and the one next step.
5. **Console.** Watch the run page timeline and badges while the job runs.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unit shows queued and the job does not start | Job stays queued: no runner claimed the job. Check the pool and runners, then list jobs for the unit. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs?unit={unit}` |
| Unit shows awaiting-approval and the apply does not start | Apply waits for approval: the unit needs distinct approvals before the apply starts. Read the job, then approve it. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |
| Unit shows failed after a change | Change failed: the latest apply, destroy or Pulumi update of kind update, destroy or import failed. Read the job, then fix the cause, then run a new change. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- ADR 265 names the job model as writer of running, queued and awaiting-approval and the job model plus stack package as writers of failed. Proof: docs/adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md decisions 7 and 10
- ADR 265 defines apply-needed from a stored plan with unapplied changes and leaves code-ahead-of-applied with RFC 12. Proof: docs/adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md decision 9
- Issue 1198 requires each write in the transaction of its own write with namespace.Fence and both orders of Pulumi job end and update end tested. Proof: #1198 body: writer writes a state only in the transaction of its own write, with the namespace.Fence; test for both orders

## What Changes

- Job model writes running, queued and awaiting-approval in the transaction of each job change with the fence
- Job model and stack package write failed for apply or destroy jobs and Pulumi updates of kind update, destroy or import; latest ended change wins by server end time with tie break, fail-closed same-change disagreement, single-end sets failed, and no acknowledge
- Job model writes apply-needed for a ready stored plan with unapplied changes and clears it on apply, supersede, expiry or invalid plan
- Stored state facts feed the states slice under unit:read; this change adds no states field, filter or badge order

Out of scope:

- Code-ahead-of-applied source for apply-needed stays with RFC 12
- Drift states in-sync, drifted and unknown
- States field, state filter, badge order rendering and removal of Entry.Status stay with the states slice
- Console run page timeline

Depends on:

- add-job-service-routes-start-1
- approval-defaults-take-effect-live
- stored-plans routes and plan-validity feed
- states slice #1196
- `approval-defaults-take-effect-live` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A unit keeps several badges at once; a person who expects one status must read the badge set.
- Failed stays until a later change succeeds; no acknowledge action clears it.
- An OpenTofu apply outside a job sets no failed because the server sees only the state upload; an apply through the TFE-compatible run API counts as a job.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Job state machine test holds every state reachable from queued. (`pkg/job/state_test.go:135`)
- Namespace.Fence carries the namespace and failover version of a write. (`pkg/namespace/namespace.go:178`)
- DurableClaimer tracks running claims for jobs. (`pkg/job/claimer.go:674`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 265: A unit shows all its states with one fixed set of words** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The job model writes `running`, `queued` and `awaiting-approval`."
- Quote: "`failed` means that the latest change to end failed."
- Quote: "`apply-needed` has one first source."
- Effect: The change writes only the states that ADR 265 assigns to the job model and to the stack package, each in the transaction of its own write with the fence.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Who writes running, queued, awaiting-approval, failed and apply-needed? ADR 265 decision 7 names the job model as writer of running, queued and awaiting-approval, the job model and the stack package as writers of failed, and the job model as writer of apply-needed; each writer writes only in the transaction of its own write with the fence.
- Which job and update kinds set failed? ADR 265 decision 10 defines a change as an apply or destroy job or a Pulumi update of kind update, destroy or import; preview, refresh, plan-only job and drift check do not set failed. A bare OpenTofu state upload sets no failed.
- What wins when ends arrive out of order, disagree, or tie? ADR 265 decision 10 says the latest change to end wins with no acknowledge action. Order is by server end time; ties break by greater change identity.
- Does this change own the states field or the removal of Entry.Status? ADR 265 decision 14 assigns them to the first locked slice; this change only writes stored facts.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
