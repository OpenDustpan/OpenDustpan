# 100. A shared job model on durable operations

Status: accepted · 2026-10-17. An open question superseded by ADR 125. Decision 4 extended by ADR 148, ADR 258 and ADR 263. Decision 17 superseded in part by ADR 128. Decision 4 superseded in part by ADR 190. Decision 20 superseded in part by ADR 213

Changed by:

- ADR 304 changes decision 2: a discovery job is a namespace-scoped job
  kind with no unit.
- ADR 304 changes decision 20: the job token of a discovery job is limited to
  the namespace, with one stored grant.
- ADR 110 extends decision 17: a Catalog origin on a start request and a
  precondition port.
- ADR 125 changes: the open question on what Dusty is.
- ADR 128 changes decision 17: the agent reference and the credential of
  job.Actor.
- ADR 130 extends: answers the open question on the default runner lease.
- ADR 135 extends decision 5: the source snapshot of the commit of a job.
- ADR 139 extends decision 5: the job spec records the resolved image digest.
- ADR 144 changes decision 20: the job token holds job:write-cost.
- ADR 145 extends: answers the open question on the events of outgoing
  webhooks.
- ADR 148 extends decision 4: the `verify` phase kind.
- ADR 190 changes decision 4: a phase row for an apply job with job.Spec.Plan.
- ADR 213 changes decision 20: output reads of the job token.
- ADR 258 extends decision 4: a plan phase for a scheduled refresh job.
- ADR 263 extends decision 4: the phase table row of drift detection.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 299 changes decisions 12 and 15: the `job.review-watch` poll ends.
- ADR 313 extends: what a job is for.

## Context

The project owner decided that two executors come in parallel, on one job
model that comes first:

1. **Write-back.** OpenDustpan writes a change to git, as a direct push or
   as a review request. The team's existing CI applies it.
2. **Runners.** Processes that the team hosts claim work from OpenDustpan
   and run the steps of a job.

The Catalog of blueprints ([RFC 14](../rfc/0014-blueprints.md)), Dusty and
the runners all start or run jobs. So this model blocks them, and a design
mistake in it is hard to undo.

The owner gave these rules for the model:

- A job belongs to one unit: tool, namespace and the tool's own name
  ([ADR 13](0013-separate-tool-models-shared-read-model-and-access.md)). A
  job never merges tool models.
- With write-back, the state write from the team's CI closes the loop.
  OpenDustpan sees the new state version and marks the job done.
- Jobs run on the durable operations of
  [ADR 72](0072-scale-out-on-one-database.md). There is no second queue.
- The owner allows breaking changes until 1.0. The Pulumi Cloud-compatible API
  and the TFE-compatible API are the exception.
- Dusty runs inside the server. It is an agent loop on durable operations,
  and it acts as the user who asked it.

[RFC 6](../rfc/0006-runners-and-remote-runs.md) proposes runners and jobs.
[RFC 12](../rfc/0012-api-edits-and-write-back.md) proposes write-back and
the rule that each value has one home.
[RFC 13](../rfc/0013-policy-and-approvals.md) proposes policy and
approvals. [RFC 28](../rfc/0028-stored-plans.md) proposes stored plans.
[RFC 29](../rfc/0029-existing-ci.md) asks whether an existing CI is one
more kind of runner. ADR 101 decides the executors, ADR 102 the plans and
logs, and ADR 103 the gates, the actions and the audit entries. The history
model (ADR 151 and ADR 152) decides the item keys, the redaction and the
retention of plans and logs.

The owner approved the decisions of this ADR on 17 October 2026.

## Decision

1. **A domain package `pkg/job`.** The package is tool-neutral. It imports
   no tool package, no store adapter, no forge adapter and no transport. No
   tool package imports it. The internal wiring of the server connects
   them (ADR 101 decision 8). depguard holds `pkg/job` to the rules of the
   other domain packages.
2. **One job, one unit.** The unit of a job is an `identity.Object` unit
   reference with its stable unit ID set. A rename of the unit keeps its
   jobs. A change to many units is many jobs. The job core never parses the
   tool's own name.
3. **Four tool-neutral operations.** `plan`, `apply`, `destroy` and
   `refresh`. Each tool maps them to its own commands: a Pulumi `preview`,
   `up`, `destroy` and `refresh`, and an OpenTofu `plan`, `apply`,
   `apply -destroy` and `apply -refresh-only`. User-facing text keeps the
   words of the tool. Only `plan` cannot write a state version.
4. **A job is a list of phases.** A phase is the work that one executor does
   with one claim. There are three kinds:
   - `plan`: runner steps that change nothing, and the stored plans.
   - `apply`: runner steps that change resources or write state.
   - `deliver`: the commit of a write-back job.

   The phases come from the executor at start, and do not change after. The
   defaults are:

   | Executor | Operation | Phases |
   | --- | --- | --- |
   | runner | plan | plan |
   | runner | apply | plan, apply |
   | runner | destroy | plan, apply |
   | runner | refresh | apply |
   | write-back | apply | deliver |

   An apply and a destroy plan first, so that a person can read the plan,
   and a gate can bind approvals to it. Between two phases, no executor
   holds the job. So a wait for a person never keeps a runner busy.
5. **The server fixes the spec at start.** The server resolves the steps,
   the code location, the commit, the runner pool and the profile that
   supplies the secrets when the job starts. It stores them in the job. A
   later change of the unit settings, of a blueprint, of the profile choice
   or of the head of the branch does not change a job. Each phase of a job
   runs on one commit. The runner pool must belong to the namespace of the
   unit. Else the start fails with `ErrInvalid`.
6. **Eleven states.** The job states are `queued`, `awaiting-approval`,
   `dispatched`, `running`, `canceling`, `awaiting-state`, and the terminal
   states `succeeded`, `failed`, `canceled`, `rejected` and `expired`. A
   queued job has a wait reason when something holds it: `unit-busy` or
   `frozen`.
7. **One table of transitions.** `job.Next` is the only place that decides
   a transition. An event that the table does not allow fails with
   `ErrTransition`. Any event on a terminal state fails with `ErrFinished`.
   A table-driven test checks each pair of state and event against the
   table below, written out apart from the code.

   | From | Event | To |
   | --- | --- | --- |
   | queued | dispatch | dispatched |
   | queued | need-approval | awaiting-approval |
   | queued | replan | queued |
   | queued | cancel | canceled |
   | queued | expire | expired |
   | awaiting-approval | approved | queued |
   | awaiting-approval | rejected | rejected |
   | awaiting-approval | cancel | canceled |
   | awaiting-approval | expire | expired |
   | dispatched | claim | running |
   | dispatched | cancel | canceled |
   | dispatched | expire | expired |
   | dispatched | phase-failed | failed |
   | running | claim | running |
   | running | release | dispatched |
   | running | phase-done | queued |
   | running | last-phase-done | succeeded |
   | running | delivered | awaiting-state |
   | running | phase-failed | failed |
   | running | cancel | canceling |
   | canceling | release | canceled |
   | canceling | phase-failed | failed |
   | canceling | phase-done | canceled |
   | canceling | last-phase-done | succeeded |
   | canceling | delivered | awaiting-state |
   | canceling | cancel | canceling |
   | awaiting-state | merged | awaiting-state |
   | awaiting-state | state-observed | succeeded |
   | awaiting-state | rejected | rejected |
   | awaiting-state | cancel | canceled |
   | awaiting-state | expire | expired |

   These rules in the table need a reason:
   - An approval goes back to `queued`, not to `dispatched`. So the freeze,
     the one-change rule and the plan check apply again before the
     dispatch.
   - `running` + `claim` is a takeover: the same durable operation with a
     new claim token (decision 9).
   - `queued` + `replan` moves the job back to its plan phase when the plan
     is stale (ADR 102 decision 7).
   - The job never hides what happened. When the last phase ends before
     the executor sees a cancel, the job ends `succeeded`. A phase that
     fails during a cancel ends the job `failed`, because a step can have
     changed resources before it stopped. Only a release when no claim of
     the phase began a step that changes resources, or the end of a phase
     that is not the last, ends `canceled`. A release after a change began
     is `phase-failed` (decision 9), so `canceling` + `release` never ends
     `canceled` after a change, also when the change ended.
   - A push cannot be undone. A write-back job that delivers during a
     cancel waits for the state version, as without the cancel.
   - A review request that a person closes without a merge ends the job
     `rejected`, as a rejection in OpenDustpan does.
8. **Each phase is one durable operation.** The dispatch of a phase starts
   one operation of the `durable` port of ADR 72, with the start key
   `<job ID>/<phase index>`. The claim, the expiry, the fencing token, the
   takeover and the cancel of a phase are the rules of that port. A
   takeover is a new claim on the same operation, as the claim rule of ADR
   72 decision 2 says. It is never a new operation. `Phase.Claims` counts
   the claims as a record only. The kinds are:
   - `job.write-back` for a deliver phase. Each server instance claims it
     with a `durable.Runner`.
   - `job.runner.<pool ID>` for a runner phase. The server claims it on
     behalf of a runner of that pool, through the runner protocol.

   So a runner claims only the phases of its own pool, and the claim uses the
   `SKIP LOCKED` query of the durable adapter. The job store starts the
   operation in the transaction of the job change. The `durable.Store`
   contract already allows this.

   The end of a phase is two writes. First, the job write of the outcome
   carries the step fence of a last step, `finish`, with the rules of
   `RecordStep`. Then the executor completes the operation. A takeover
   between the two writes finds `finish` done, and only completes.
9. **A takeover never repeats a change.** Before a runner starts a step that
   changes resources, it records the start of the step through its claim.
   At a takeover, the job store reads the step records of every earlier
   claim of the phase:
   - When any earlier claim recorded the start of a step that changes
     resources, the phase fails. This is true whether or not that claim
     recorded the end of the step. A change that ended is still a change: a
     new run of the phase from its first step would make it again. The
     reason tells the operator to read the state of the unit. OpenDustpan
     never repeats a change, finished or not.
   - Else the job stays `running` under the new claim, and the phase runs
     again from its first step on the new runner, with a fresh checkout.
     The records of the earlier claim stay, with the token of that claim.

   `job.AfterLostClaim` holds the rule, and a table-driven test checks it.
   The same rule applies to a release, also during a cancel (decision 7). A
   failed step also fails the phase. A person starts a new job: OpenDustpan
   never applies again on its own. A deliver phase has no step that changes
   resources. The write-back executor makes its commit idempotent instead,
   with a lookup on every takeover (ADR 101 decision 3), so a takeover of a
   deliver phase does not fail.
10. **One change at a time on a unit.** A dispatch of a phase that changes
    resources, state or the code in git fails with `ErrUnitBusy` while
    another job of the unit is `dispatched`, `running` or `canceling` with
    such a phase. A SQL adapter holds this rule with a partial unique index
    on the unit ID of the jobs table, over the rows whose state holds the
    unit and whose current phase changes. The index fails the second
    dispatch in its own transaction. `Store.Create` with a dispatch checks
    the index and the freeze as any dispatch does, and on `ErrUnitBusy` it
    stores nothing. `Service.Start` then creates the job again with no
    dispatch, `queued` with the wait reason `unit-busy`, and returns it with
    no error. The reconcile schedule dispatches it when the unit is free. A
    plan phase never holds the unit. A job that waits for approval or for a
    state version does not hold the unit either. A hold of days would stop
    every other change.
11. **Cancel.** A cancel of a job that no executor holds ends it `canceled`
    at once, and cancels its operation in the same transaction. A cancel of
    a running job moves it to `canceling` and requests the cancel of the
    operation. The executor sees `ErrCanceled` at its next renewal or step
    start, stops, and ends the phase. A cancel of a job in `awaiting-state`
    with an open review request ends it `canceled`. A cancel of a job whose
    change is in git for good, a direct push or a merged review request,
    fails with `ErrDelivered`. The job stays and waits for its state
    version, so that the console shows what happened. `job.CheckCancel`
    holds the rule.
12. **Follow-up work is durable too.** The job store starts these
    operations in the transaction of the change that needs them, with the
    start key `<job ID>/<kind>`:
    - `job.abandon` after a cancel, a rejection or an expiry of a job that
      delivered. It calls `Executor.Abandon`: write-back closes its review
      request when no merge happened.
    - `job.review-watch` after the delivery of a review request. It is the
      polling fallback of the review watcher (decision 15).
    - `job.verify` after the close of a write-back job (decision 15).
13. **The job record is namespace data.** The job store checks the
    namespace fence and appends the audit entries in the transaction of each
    write, as the other stores do. An executor takes the fence at its claim,
    in `PhaseClaim`, and every write of `Claimer` and `Recorder` checks it.
    So an executor of a namespace that failed over writes nothing. The
    backup of a namespace holds its jobs. The durable operations are control
    tables, which a restore keeps (ADR 72 decision 5). After a restore, a
    job can name an operation that does not exist or that ended.
    `Store.Orphans` finds them, and the reconcile schedule applies the rule
    of decision 14 to each one, with the reason `restored` when no change
    began. A phase that changes resources never dispatches again because of
    a restore.
14. **One schedule, no ticker.** One schedule row of ADR 72 decision 3,
    `job.reconcile`, does the periodic work every minute. It dispatches
    queued jobs whose wait ended, expires jobs past their limits, and
    applies `job.AfterOperationEnded` to each job that `Store.Orphans`
    returns: a job that is `dispatched`, `running` or `canceling` while the
    operation of its current phase ended with no `Finish`, or does not
    exist. Such an operation comes from a restore, from a durable failure
    at the limit of expired claims, or from a claim that expires during a
    cancel: the durable store ends a canceled operation when its claim
    expires, so no takeover follows. The rule:
    - A `dispatched` job fails. No executor saw the phase.
    - A `running` or `canceling` job gets the rule of `job.AfterLostClaim`
      (decision 9) with a release. When any claim of the phase began a step
      that changes resources, the job fails. Else a `canceling` job ends
      `canceled`, and a `running` job fails, because its operation cannot
      run again.

    A table-driven test checks the rule, and the conformance suite has a
    case for a cancel whose claim expires. The retention of plans and logs
    is not a schedule of the job model: at the end of the retention period
    of an item, the retention expiry of the history model redacts it
    through the `history.Redactor` of the job model, with the cause
    `retention` (ADR 102 decision 10, ADR 152).
15. **A final state version closes a write-back job.** The internal wiring
    of the server reports each state version that a tool writes as a
    tool-neutral `job.StateVersion`. The store adapter applies the rule of
    `Store.ObserveStateVersion` in the transaction of that state write. A
    tool package never imports `job`. The rule is (`Job.ClosedBy`):
    - Only the final state version of a run closes a job: the release of
      the OpenTofu state lock, or the end of a Pulumi update. An
      intermediate write of a run never does.
    - A direct push must deliver at or before the state version. The
      delivery time is the time that the forge gives for the commit, also
      when a takeover finds the commit later by its trailer.
    - A review request must merge at or before the state version. With no
      known merge, the job waits. The **review watcher** records merges and
      closes: a forge webhook gives them, and the `job.review-watch`
      operation polls the forge as the fallback for a lost webhook. A
      merge applies `merged` with the merge time of the forge. A close
      without a merge applies `rejected`.
    - A preview or a plan is not a state version. A final state write of
      any origin closes the job, also a state write from a workstation.
    - **A late merge or a late delivery still closes.** The job store
      records the last final state version of each unit, also when no job
      waits. When `merged` or `delivered` applies, the store checks, in the
      same transaction, whether that version is at or after the merge time
      or the delivery time. If it is, the store also applies
      `state-observed` with that version. So a merge that the poll sees
      after a lost webhook, and a commit that a takeover finds after the
      CI wrote the state, close the job as soon as the job store learns of
      them. The conformance suite has a case for each.

    The close sets the verification of the job to `pending` and starts the
    `job.verify` operation. That operation asks the forge, outside the
    transaction of the state write, whether the commit of the state version
    contains the delivered commit. It records `verified` or `unverified`
    with `Store.SetVerification`. The console shows a job that closed
    unverified.
16. **Limits of open jobs.** A unit has at most 16 jobs that are not
    terminal, and a namespace 1,000. A start above a limit fails with
    `ErrLimit`. A job that stays queued for 7 days, or dispatched with no
    claim for 24 hours, expires. Installation settings can change these
    limits later.
17. **What a caller calls.** The REST API, the Catalog and Dusty start and
    drive jobs through one port, `job.Service`: `Start`, `Get`, `Cancel`,
    `Approve`, `Confirm` and `RedactLog`. Each method takes a `job.Actor`:
    the principal of the request, and the agent that acts for it, if any,
    with the mode of the agent (ADR 103 decision 12). Dusty runs inside the
    server as an agent loop on durable operations. It calls `job.Service`
    directly, and acts as the user who asked it, through an act-as edge of
    [ADR 70](0070-act-as-reach-idp-groups-and-break-glass.md). The actions
    of the user decide. The job, its approvals, the decision input and the
    audit entries record both the user and the agent (ADR 103). A caller
    gives a `job.StartRequest`: the unit, the operation, the executor, the
    code location, the pool, the edits, an idempotency key, a reason and an
    optional break-glass. A caller never gives steps (ADR 101 decision 8).
    Two starts with one key and one spec give one job. A key with another
    spec fails with `ErrKeyConflict`. No caller writes to the job store
    directly.
18. **Ports with conformance first.** `job.Store` and `job.FreezeStore` get
    the suite `pkg/job/jobtest` before any SQL adapter. A reference adapter
    in memory, in the tests of that package, runs the suite, so each case
    runs today. The same reference adapter implements `history.Redactor`
    for plans and log versions and runs `historytest.RunRedactor` with
    `historytest.JobArtifactSituations()` and
    `historytest.JobLogSituations()`. The suites of `job.Claimer`,
    `job.Recorder` and the reads of `job.ArtifactStore` come with their
    first implementation, because they need the blob store and the item
    keys of ADR 151.
19. **Terms.** The owner uses "executor" for the way a job runs: write-back
    or runners. This ADR supersedes the _Avoid_ entry "executor" of the term
    Runner in RFC 6. It also supersedes the definition of Job in RFC 6 as
    work for a runner only: a job is work that OpenDustpan asks an executor
    to do on one unit. The process that a runner starts for a step is the
    **launcher**, so that it does not clash with "executor".
20. **The job token and the job-ID claim.** At the claim of a runner phase,
    the server makes a **job token** for the steps of the phase. It is an
    access token of the principal that started the job, limited to the
    unit of the job, with the expiry of the claim. The runner gives it to
    the tool as ADR 101 decision 10 says, never in a variable. The job
    token puts one claim on the principal that it authenticates: the
    **job-ID claim**, the ID of the job. The `identity` package gets the
    field `Principal.JobID` for it, empty for every other credential. The
    claim does not add an action. A state write with the job token reports
    the claim as `StateVersion.Job`, so the job record and the history of
    the unit can show which state version the job wrote. A job token of a
    job that ended authenticates nothing.

## Consequences

- The Catalog, Dusty, the runner protocol and the write-back executor can
  start in parallel on `pkg/job`. Each of them changes no part of the model
  that the others use.
- No new queue table. A job adds one durable operation per phase, and one
  for each follow-up. The durable tables hold the claims, and the job
  tables hold what people read.
- The durable port gets one more use of its contract for other ports. A log
  append checks that the claim is live and accepts writes during a cancel.
  The durable contract today names only the rules of `RecordStep` and
  `Start`. The first `Recorder` implementation adds a conformance case for
  this rule to `durabletest`.
- A failed or lost apply is never repeated without a person. A takeover of
  an apply phase whose change began fails the job. Some teams may want an
  automatic retry of a plan. That is a later option per unit, not a
  default.
- A state write from a workstation can close a write-back job before the CI
  runs. The verification shows this. It does not prevent it.
- A cancel cannot stop a direct push. The console shows the job until its
  state version arrives or it expires.
- RFC 13 says that one edit has one place for approval. The owner decided
  that OpenDustpan approvals add to the approvals of the forge. ADR 103
  records this, and RFC 13 must follow it.
- RFC 29 asks whether an existing CI is a kind of runner. Under this ADR, it
  is not. Write-back is the path for a team that applies in its own CI, and
  OpenDustpan schedules no work in that CI.
- `GLOSSARY.md` needs the terms Job, Executor, Phase, Gate, Freeze,
  Write-back, Runner pool and Launcher.

## Open questions

- What is a batch? A blueprint rollout (RFC 14) starts one job per unit.
  The console may need a batch ID to show them as one rollout. Is that a
  field of the job, or a record of its own that the Catalog owns?
- How long does the server keep a job record? The content of plans and logs
  has the retention of the history model, 400 days by default (ADR 152).
  The record and its audit entries may need the retention of the audit log
  instead.
- Does a `refresh` close a write-back job? It writes a state version, but it
  applies no change from git. This ADR closes the job on any final state
  version.
- Is the default lease of a runner claim the 30 seconds of
  `durable.DefaultLease`? A runner on a slow network may need more. The
  runner protocol pull request decides it.
- Which events do outgoing webhooks send (RFC 13 "Approvals through the
  API")? The state changes of the table are a natural list.
