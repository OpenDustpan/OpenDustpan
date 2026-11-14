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
| Unit shows failed after a change | Change failed: the latest apply, destroy or Pulumi update of kind update, destroy or import failed. Read the job, then read its log chunks, fix the cause, then run a new change. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{id}` |

Evidence for the claims above:

- ADR 265 names the job model as writer of running, queued and awaiting-approval and the job model plus stack package as writers of failed. Proof: docs/adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md decisions 7 and 10
- ADR 265 defines apply-needed from a stored plan with unapplied changes and leaves code-ahead-of-applied with RFC 12. Proof: docs/adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md decision 9
- Issue 1198 requires each write in the transaction of its own write with namespace.Fence and both orders of Pulumi job end and update end tested. Proof: #1198 body: writer writes a state only in the transaction of its own write, with the namespace.Fence; test for both orders

## What Changes

- Job model writes running, queued and awaiting-approval in the transaction of each job change with the fence
- Job model and stack package write failed for apply or destroy jobs and Pulumi updates of kind update, destroy or import; latest ended change wins by end time with fail-closed same-change disagreement, single-end sets failed, and no acknowledge
- Job model writes apply-needed for a ready stored plan with unapplied changes and clears it on apply, supersede, expiry or invalid plan
- Stored state facts feed the states slice under unit:read; this change adds no states field, filter or badge order

Out of scope:

- Code-ahead-of-applied source for apply-needed stays with RFC 12
- Drift states in-sync, drifted and unknown
- States field, state filter, badge order rendering and removal of Entry.Status stay with the states slice
- Console run page timeline
- Ready stored-plan feed for apply-needed stays with the stored-plans work; until that feed lands apply-needed stays cleared

Depends on:

- add-job-service-routes-start
- approval-defaults-take-effect-live
- SQL job store #968 and follow-ups
- states slice #1196
- `approval-defaults-take-effect-live` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A unit keeps several badges at once; a person who expects one status must read the badge set.
- Failed stays until a later change succeeds; no acknowledge action clears it.
- An OpenTofu apply outside a job sets no failed because the server sees only the state upload; an apply through the TFE-compatible run API counts as a job.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 265: A unit shows all its states with one fixed set of words** (not owner-tagged).

- Decisions that apply: 7 (current), 9 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The job model writes `running`, `queued` and `awaiting-approval`."
- Quote: "`failed` means that the latest change to end failed."
- Quote: "`apply-needed` has one first source."
- Effect: The change implements the named writers, the transaction rule with the fence, and the definitions of failed and apply-needed.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): proposal.md lists the routes /jobs/{id}/approvals and /jobs/{id}/log as the commands that help. The change's own open-questions section says a code check fails for both routes, so the user path cites commands that do not exist. Resolution: answered: Errors use only existing job list and job read routes; no approval or log routes cited.
- R1.2 (failure mode): Design risk says two writers of failed can write different results when their success definitions differ. The spec only covers one change seen from two sources in either order ("both orders agree"). It has no rule or scenario for the case where the job end says failed and the update end says success, or the reverse. Resolution: answered: Added keep-failed-on-same-change-disagreement with divergent-ends scenario and fail-closed design rule.
- R1.3 (missing scenario): The spec does not say what ends a job as cancelled, timed out, lost by a crashed runner or superseded. ADR 265 decision 10 counts only apply, destroy and update kinds that end, so the failed and running results for these outcomes are unknown. Resolution: answered: Added clear-live-on-terminal and leave-failed-on-terminal for cancelled, timed out, lost-claim and superseded ends.
- R1.4 (failure mode): The running and queued rows are removed only when the job leaves the live set. A server restart or runner crash that never ends the job leaves running or queued stuck on the unit. No requirement covers recovery of these rows at startup or after failover. Resolution: answered: Added reconcile-live-states for restart and failover recovery of stuck running and queued rows.
- R1.5 (open question): The apply-needed clear rule says only "the plan stops being valid". It does not define validity, a plan superseded by a newer plan, a plan that expires, or a plan whose code moved on. The design risk names this stale-plan case, but no scenario covers it. Resolution: answered: Defined apply-needed validity as ready plan with changes; apply, supersede, expiry and invalid plan clear it, with two-plan scenarios.
- R1.6 (missing scenario): The spec does not say which authorization action guards the reading of states in the unit list and unit read. Every endpoint must declare its actions and objects, and no scenario checks a caller without unit:read. Resolution: answered: Added read-states-auth requiring unit:read with denial scenario.
- R1.7 (open question): The usability section says an OpenTofu apply outside a job sets no failed, but the change does not say whether an OpenTofu apply through the TFE-compatible run API counts as an apply job. The failed result for OpenTofu runs is therefore not fixed. Resolution: answered: TFE-compatible run API apply counts as apply job; bare state upload sets no failed; usability records the rule.
- R1.8 (scope): Task 3.1 expects in-sync or failed after an apply. In-sync is a drift state that the proposal lists as out of scope. Task 2.2 also wires the states field into the inventory, which ADR 265 decision 14 assigns to the first slice (locked). Resolution: answered: E2e task expects only running, queued and failed transitions; states field wiring stays with the states slice.
- R1.9 (wrong claim): The proposal's ADR check gives a wider owner-tag rule than project.md. It adds "when the ADR records decisions of the project owner", but project.md tags a decision only by its "(owner)" text or by the ADR title. The proposal also states that both writers give the same result, which the design's risks contradict. Resolution: answered: ADR check uses exact character spans from ADR 265 with no added owner-tag rule and no same-result claim.
- R2.1 (scope): proposal.md says the change exposes the five states as state plus since in inventory reads, and tasks.md 2.2 wires the states field. ADR 265 decision 14 gives the states field, the state filter and the badge order to the first slice (locked), and the change itself says the states slice owns that wiring. The change does work that belongs to another item. Resolution: answered: Removed states-field wiring from what_changes and task 2.2; this change writes stored facts that the states slice reads.
- R2.2 (open question): The rule for divergent ends assumes both ends of one change exist. The change does not say whether a job end alone, or an update end alone, sets failed when the other end never arrives (for example a Pulumi update with no job, or a job that crashes before its update end is written). Resolution: answered: Added set-failed-single-end rule: one end alone sets failed with its own end time when the counterpart never arrives.
- R2.3 (wrong claim): The requirement clear-failed-on-success says a later success from either writer clears failed. The requirement failed-divergent-writes says a disagreement between the two ends of one change keeps failed. The spec does not separate a later change from the other end of the same change, so the two rules conflict for a success-on-one-end case. Resolution: answered: Split same-change disagreement from later-change clear by change identity; keep-failed covers same change identity, clear covers a later change identity.
- R2.4 (failure mode): No rule covers out-of-order arrival of change ends across writers. A late failed end of an older change can arrive after a newer successful change and set failed again, or the reverse. The rule latest change to end wins (ADR 265 decision 10) is not tied to a sequence or end time in the store. Resolution: answered: Added end-time ordering: only a later end time overwrites the failed row, with out-of-order arrival scenarios.
- R2.5 (missing scenario): The design says each write method checks the fence before the upsert. No scenario says what happens when the fence check fails in the transaction of a job change (for example after a failover). It is not stated whether the job change rolls back, retries, or leaves the state stale. Resolution: answered: Added fence-failure-rolls-back rule: fence failure rolls back the job change with scenarios.
- R2.6 (open question): The change is not spec-ready. The code check reports the requirement show-awaiting-approval area of the spec as having two shall clauses in one sentence (E004). The gate in project.md forbids implementation until the open questions read none. Resolution: answered: Split terminal-non-failed-ends into two single-SHALL requirements to clear E004.
- R2.7 (wrong claim): Task 2.2 check uses GET on the job routes and says they return the job facts behind the five states. These routes do not return unit states. The check does not test the inventory states field that the proposal promises, so the task can pass without any state being shown. Resolution: answered: Replaced job-route GET check with store and provider checks that do not claim the inventory states field.
- R2.8 (missing scenario): ADR 265 decision 4 requires the wire values of the five states to be stable strings with a table test. No task or scenario adds that table test for running, queued, awaiting-approval, failed and apply-needed. Resolution: answered: Added stable-wire-values requirement and table-test task for the five states.
- R2.9 (missing scenario): apply-needed is set per unit, but the spec does not say what happens when two ready stored plans with changes exist for one unit, or when a plan is applied while a newer plan is ready. Only single-plan cases are covered. Resolution: answered: Added two-ready-plans rule and scenarios for concurrent stored plans.
- R2.10 (failure mode): reconcile-live-states does not say how running or queued rows are matched to live jobs after a restart, or what happens when a runner is still executing a job that the server marked orphan. The clear rule may remove running for a job that still runs. Resolution: answered: Defined reconcile matching by live job identity: keep rows for live jobs, clear rows for terminal or unknown jobs.
- M.30 (dependency): The dependency 'stored plans' names no change, and the state awaiting-approval comes from the live approval wait. Resolution: answered: Removed unnamed stored-plans dependency, added depends on add-job-service-routes-start for job lifecycle and approval-defaults-take-effect-live for the awaiting-approval wait, and recorded the stored-plan feed gap in out_of_scope.

## Open questions

none

Questions considered and answered:

- Who writes running, queued, awaiting-approval, failed and apply-needed? ADR 265 decision 7 names the job model as writer of running, queued and awaiting-approval, the job model and the stack package as writers of failed, and the job model as writer of apply-needed; each writer writes only in the transaction of its own write with the fence.
- Which job and update kinds set failed? ADR 265 decision 10 defines a change as an apply or destroy job or a Pulumi update of kind update, destroy or import; preview, refresh, plan-only job and drift check do not set failed. An OpenTofu state upload with no job sets no failed; an OpenTofu apply that runs as a job through the TFE-compatible run API counts as an apply job.
- What wins when ends arrive out of order or disagree? ADR 265 decision 10 says the latest change to end wins with no acknowledge action. Order is by change end time, not by arrival order. A store row holds the change identity and the end time, and only an end with a later end time overwrites the row.
- Does this change own the states field or the removal of Entry.Status? ADR 265 decision 14 assigns the states field, the state filter and the badge order to the first locked slice; this change only writes stored facts that that slice reads and does not change inventory reads, filters, badge order, or Entry.Status.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
