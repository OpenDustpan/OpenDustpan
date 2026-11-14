Roadmap: `docs/roadmap.md:729` (A failure in one unit).

## Why

**Persona: developer.** A Developer runs `terragrunt run-all apply` over three workspaces on a runner and one unit fails at apply. The Developer needs the failure contained to that unit, with prior state kept and a clear record, so the team can see which unit failed.

The user path this change must make work:

1. **API.** Run the run-all wrapper job on a runner over three units and read unit status through `GET /api/dustpan/v1/namespaces/{}/units`.
2. **API.** Confirm no new current state version for the failed workspace through `ANY /api/v2/workspaces/{}/state-versions`.
3. **Docs page.** Read the Terragrunt failure page to find the failed unit.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply job for one workspace fails with a provider error. | The server marks the apply job failed and keeps the prior current state version. Run `GET /api/dustpan/v1/namespaces/{}/units` to read the unit status. | `GET /api/dustpan/v1/namespaces/{}/units` |
| A later claim finds the failed workspace locked after a failed apply job. | The server releases the workspace lock after the failure. Run `ANY /api/v2/workspaces/{}/state-versions` to confirm the prior current state version. | `ANY /api/v2/workspaces/{}/state-versions` |

Evidence for the claims above:

- No e2e file in the pack names an apply job. Proof: Keyword Apply job has e2e count 0 with an empty e2e list in the keyword evidence.
- State version routes exist on main for workspaces and state versions. Proof: Main lists ANY /api/v2/workspaces/{}/state-versions and ANY /api/v2/state-versions in the route table or API description.
- RFC 115 is a draft stub with no proposal for discovery, order, or failure in one unit. Proof: RFC 115 excerpt: Status: draft. A stub. ADR 304 decides that, on OpenDustpan runners, a shim turns `terragrunt run-all` into one job for each Terragrunt unit, under a change order, started by a discovery job.

## What Changes

- A failed or cancelled apply job in a fan-out change order keeps the prior current state version for its own workspace.
- The history entry and the audit entry of the failed apply job name the workspace stable ID and the failure reason without secret values.
- The workspace lock of a failed or cancelled fan-out apply releases.
- A docs page shows how to find the failed unit through the units list and the state version list.

Out of scope:

- Unit discovery and mapping of Terragrunt units to workspaces
- Order of apply jobs across dependent units
- Result for the two units that did not fail, pending the owner decision
- Change order status after a partial failure, pending the owner decision
- Retry route and authorization for the failed unit, pending the owner decision
- Whole-order retry, plan revision change before a retry and concurrent retries, pending the owner decision
- Lock handling for normal runs without failure, owned by run-all-on-runner-discovery
- Terragrunt guide and profiles recommendation page

Depends on:

- run-all-on-runner-discovery
- run-all-on-runner-fanout-plan-apply-jobs
- record-names-cause
- review-holds
- `record-names-cause` (order of the milestone review)
- `review-holds` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- After a failure the Developer sees which unit failed without opening each job.
- A partial apply can change real infrastructure while the old state version stays current, so the docs page says that state versions alone do not show infrastructure truth.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A test on main checks that a round deadline ends cleanly without failure or backoff in audit shipping. (`pkg/audit/ship_test.go:1542 (func TestARoundDeadlineEndsCleanlyWithoutFailureOrBackoff)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 304: Native run-all: fan-out applies in milestone 9** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current), 10 (current).
- Changed by: ADR 310 changes decision 8. Chain to the end: ADR 310.
- Current rule: this ADR, with ADR 310.
- Quote: "Each unit applies in its own apply job, with its own state version and its own history entry."
- Quote: "A failure in one unit: what happens to the other units when one apply job fails."
- Effect: The change keeps a failed apply on its own workspace with the prior current state version kept. It leaves the result for the units that did not fail to RFC 115.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. When one of the three apply jobs fails, what happens to the two units that did not fail and what status does the change order report?
   - Options: Fail only the failed unit and let the apply jobs that did not fail run to completion; Stop apply jobs that have not started when one apply job fails; Roll back applied units when one apply job fails.
   - Provisional decision (owner review 14 Nov): Fail only the failed unit and let the apply jobs that did not fail run to completion.
   - Reason: ADR 304 decision 10 leaves this point to RFC 115 and the RFC holds no proposal, so no ADR answers it. Benchmark: AWS practice for multi-stack deploys: a failed stack stops only its own update while independent stacks complete, which limits the blast radius.
   - Persona impact: No persona is upset by asking first: the Developer keeps prior state on the failed unit either way. Escape hatch: A namespace administrator applies the failed unit by hand against its own workspace.
   - Owner-tagged ADRs involved: ADR 304 (their words do not change).
2. Which API route and authorization actions start a retry of only the failed workspace, and what does a whole-order retry, a changed plan revision or two concurrent retries do?
   - Options: A NEW retry route on the change order scoped to one workspace ID, which skips applied units and refuses a stale plan revision; Retry through the existing job create path with the failed workspace ID; No server retry; the person applies the failed unit outside the change order.
   - Provisional decision (owner review 14 Nov): A NEW retry route on the change order scoped to one workspace ID, which skips applied units and refuses a stale plan revision.
   - Reason: The pack names no change order retry route, so no existing route answers it. Benchmark: AWS practice: a retry is a named operation on the failed resource only, with the caller checked at the point of use and an idempotency token against duplicates.
   - Persona impact: No persona is upset by asking first: the Developer still sees the failed unit from the units list. Escape hatch: A namespace administrator applies the failed unit by hand until the retry route exists.
   - Owner-tagged ADRs involved: ADR 304 (their words do not change).
3. How does the server detect a runner crash during a fan-out apply, and what happens to a held lock?
   - Options: A lease timeout marks the job failed and releases the lock; A runner heartbeat marks the job failed on the next claim; An operator marks the job failed by hand.
   - Provisional decision (owner review 14 Nov): A lease timeout marks the job failed and releases the lock.
   - Reason: No ADR in the pack states the crash detection path for apply jobs. Benchmark: AWS practice: a lost worker lease marks the task failed after a timeout and the lock releases for the next claim.
   - Persona impact: No persona is upset: the failed unit keeps its prior state either way. Escape hatch: A namespace administrator clears a held lock by hand after the timeout.
   - Owner-tagged ADRs involved: ADR 304 (their words do not change).

Questions considered and answered:

- Does a failed fan-out apply promote a new current state version for its workspace? ADR 304 decision 5 gives each unit its own apply job with its own state version and its own history entry, so a failed apply keeps the prior current state version of its workspace.
- Do the apply jobs that did not fail run to completion when one apply job fails? ADR 304 decision 10 leaves this to RFC 115. The RFC is a draft stub with no proposal, so this change leaves it as an open question for the owner.
- Which route starts a retry of only the failed workspace? The pack names no retry route for a change order. The retry path stays an open question for the owner.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
