# 100. A shared job model on durable operations

Status: accepted · 2026-10-17

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

[RFC 6](../rfc/0006-runners-and-remote-runs.md) proposes runners and jobs.
[RFC 12](../rfc/0012-api-edits-and-write-back.md) proposes write-back and
the rule that each value has one home.
[RFC 13](../rfc/0013-policy-and-approvals.md) proposes policy and
approvals. [RFC 28](../rfc/0028-stored-plans.md) proposes stored plans.
[RFC 29](../rfc/0029-existing-ci.md) asks whether an existing CI is one
more kind of runner. ADR 101 decides the executors, ADR 102 the plans and
logs, and ADR 103 the gates, the actions and the audit entries.

This ADR records the model as a design for review. The owner decisions
above are the owner's. The other decisions take effect when the project
lead accepts the pull request that adds this ADR.

## Decision

1. **A domain package `pkg/job`.** The package is tool-neutral. It imports
   no tool package, no store adapter, no forge adapter and no transport.
   depguard holds it to the rules of the other domain packages.
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
   - `plan`: runner steps that change nothing, and a stored plan.
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
5. **The server fixes the spec at start.** The server resolves the steps, the code
   location, the commit and the runner pool when the job starts. It stores
   them in the job. A later change of the unit settings, of a blueprint or of
   the head of the branch does not change a job. Each phase of a job runs on
   one commit.
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
   | running | release | dispatched |
   | running | phase-done | queued |
   | running | last-phase-done | succeeded |
   | running | delivered | awaiting-state |
   | running | phase-failed | failed |
   | running | cancel | canceling |
   | canceling | release | canceled |
   | canceling | phase-failed | canceled |
   | canceling | phase-done | canceled |
   | canceling | last-phase-done | succeeded |
   | canceling | delivered | awaiting-state |
   | canceling | cancel | canceling |
   | awaiting-state | state-observed | succeeded |
   | awaiting-state | rejected | rejected |
   | awaiting-state | cancel | canceled |
   | awaiting-state | expire | expired |

   Four rules in the table need a reason:
   - An approval goes back to `queued`, not to `dispatched`. So the freeze
     and the one-change rule apply again before the dispatch.
   - When the last phase ends before the executor sees a cancel, the job
     ends `succeeded`. The change happened, and the job tells the truth.
   - A push cannot be undone. A write-back job that delivers during a
     cancel waits for the state version, as without the cancel.
   - A review request that a person closes without a merge ends the job
     `rejected`, as a rejection in OpenDustpan does.
8. **Each dispatch of a phase is one durable operation.** The dispatch
   starts an operation of the `durable` port of ADR 72. The claim, the
   expiry, the fencing token, the takeover and the cancel of a phase are the
   rules of that port. The start key is `<job ID>/<phase index>/<dispatch
   number>`. The kinds are:
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
   When a claim ends with a start record and no end record, the phase fails.
   Its reason tells the operator to read the state of the unit. Otherwise,
   the phase dispatches again. `job.AfterLostClaim` holds the rule, and a
   table-driven test checks it. A failed step also fails the phase. A
   person starts a new job: OpenDustpan never applies again on its own.
10. **One change at a time on a unit.** A dispatch of a phase that changes
    resources, state or the code in git fails with `ErrUnitBusy` while
    another job of the unit is `dispatched`, `running` or `canceling` with
    such a phase. The store checks this in the transaction of the dispatch.
    The job then stays `queued` with the wait reason `unit-busy`. A plan
    phase never holds the unit. A job that waits for approval or for a
    state version does not hold the unit either. A hold of days would stop
    every other change.
11. **Cancel.** A cancel of a job that no executor holds ends it `canceled`
    at once, and cancels its operation in the same transaction. A cancel of
    a running job moves it to `canceling` and requests the cancel of the
    operation. The executor sees `ErrCanceled` at its next renewal or step
    start, stops, and ends the phase. A cancel of a job in `awaiting-state`
    ends it, and calls `Executor.Abandon`: the write-back executor then
    closes its review request when no merge happened. A direct push stays.
12. **The job record is namespace data.** The job store checks the
    namespace fence and appends the audit entries in the transaction of each
    write, as the other stores do. The backup of a namespace holds its jobs.
    The durable operations are control tables, which a restore keeps (ADR 72
    decision 5). After a restore, a job can name an operation that does not
    exist or that ended. The reconcile schedule then fails the job, with the
    reason `restored`. A phase that changes resources never dispatches
    again because of a restore.
13. **Two schedules, no ticker.** Two schedule rows of ADR 72 decision 3 do
    the periodic work:
    - `job.reconcile`, every minute. It dispatches queued jobs whose wait
      ended, expires jobs past their limits, and moves a job whose operation
      ended with no job change. The last case is a durable failure at the
      expiry limit, for example.
    - `job.retention`, every day. It shreds expired plans and logs (ADR 102).
14. **A state version closes a write-back job.** A tool package reports each
    state version that it writes as a tool-neutral `job.StateVersion`. The
    store adapter applies the rule of `Store.ObserveStateVersion` in the
    transaction of that state write. A tool package never imports `job`.
    The rule closes each write-back job of the unit in `awaiting-state` that
    delivered before the state version. For a review request with a known
    merge, the merge must come before the state version. A preview or a plan
    is not a state version. A state write of any origin closes the job, also
    a state write from a workstation. `StateVersion.Verified` is true only
    when the tool reported a commit that contains the delivered commit. The
    console shows a job that closed unverified.
15. **What a caller calls.** The REST API, the Catalog and Dusty start and
    drive jobs through one port, `job.Service`: `Start`, `Get`, `Cancel` and
    `Approve`. A caller gives a `job.StartRequest`: the unit, the operation,
    the executor, the code location, the steps or edits, an idempotency key,
    a reason and an optional break-glass. Two starts with one key and one
    spec give one job. A key with another spec fails with `ErrKeyConflict`.
    No caller writes to the job store directly.
16. **Ports with conformance first.** `job.Store` and `job.FreezeStore` get
    the suite `pkg/job/jobtest` before any adapter. The suites of
    `job.Claimer`, `job.Recorder` and `job.ArtifactStore` come with their
    first implementation, because they need the blob store and the data
    keys.

## Consequences

- The Catalog, Dusty, the runner protocol and the write-back executor can
  start in parallel on `pkg/job`. Each of them changes no part of the model
  that the others use.
- No new queue table. A job adds one durable operation per dispatch. The
  durable tables hold the claims, and the job tables hold what people read.
- The durable port gets one more use of its contract for other ports. A log
  append checks that the claim is live and accepts writes during a cancel.
  The durable contract today names only the rules of `RecordStep` and
  `Start`. The first `Recorder` implementation adds a conformance case for
  this rule to `durabletest`.
- A failed or lost apply is never repeated without a person. Some teams may
  want an automatic retry of a plan. That is a later option per unit, not a
  default.
- A state write from a workstation can close a write-back job before the CI
  runs. The verified flag shows this. It does not prevent it.
- RFC 13 says that one edit has one place for approval. The owner decided
  that OpenDustpan approvals add to the approvals of the forge. ADR 103
  records this, and RFC 13 must follow it.
- RFC 29 asks whether an existing CI is a kind of runner. Under this ADR, it
  is not. Write-back is the path for a team that applies in its own CI, and
  OpenDustpan schedules no work in that CI.

## Open questions

- What is a batch? A blueprint rollout (RFC 14) starts one job per unit.
  The console may need a batch ID to show them as one rollout. Is that a
  field of the job, or a record of its own that the Catalog owns?
- How long does the server keep a job record? The content of plans and logs
  has a retention of 400 days (ADR 102). The record and its audit entries
  may need the retention of the audit log instead.
- Does a `refresh` close a write-back job? It writes a state version, but it
  applies no change from git. This ADR closes the job on any state version.
- Is the default lease of a runner claim the 30 seconds of
  `durable.DefaultLease`? A runner on a slow network may need more. The
  runner protocol pull request decides it.
- Which events do outgoing webhooks send (RFC 13 "Approvals through the
  API")? The state changes of the table are a natural list.
- What is Dusty? This ADR assumes that Dusty is a client of the REST API
  with its own principal, as an internal developer portal is. If Dusty runs
  in the server, it calls `job.Service` directly with the same rules.
