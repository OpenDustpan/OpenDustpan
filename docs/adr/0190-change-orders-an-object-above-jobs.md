# 190. Change orders: an object above jobs

Status: accepted · 2026-10-18 · ADRs 191 to 198 give the parts · supersedes parts of ADRs 100, 102, 103, 128 and 147. Decisions 6 and 7 superseded in part by ADR 225

Changed by:

- ADR 222 changes decisions 6 and 7: approval defaults replace MinApprovals.
- ADR 225 changes decisions 6 and 7: approval defaults replace MinApprovals.
- ADR 299 changes decisions 4, 9, 10, 12 and 14: authors, the apply after a
  merge, and review requests.
- ADR 300 changes decision 10: a merge queue entry is a request to apply, and the account that queued it is the requester.
- ADR 344 changes decision 14: an agent can draft a change order. A person must make the draft a change order.
- ADR 365 changes decision 10: a wording refinement, an apply job only for a
  unit whose plan has a change.

## Context

The job model (ADRs 100 to 103) puts approvals on the gates of one job, and
a job belongs to one unit. A change that a team reviews often covers more
than one unit. A change of a shared local can change many workspaces, and a
change of a shared component can change many stacks. A reviewer wants to
read one plan for the whole change, and to approve it one time.

ADR 128 decision 10 to decision 14 give the rules for the approvals of a
job: an agent records no verdict, a request-changes thread blocks approval,
and the author never resolves a thread. ADR 128 says that the same rules
apply to the approvals of a change order when that design lands. This ADR
is that design.

The project owner decided on 18 October 2026 (owner):

- A change order is an object above jobs. It holds a plan of one or more
  units, approvals, signatures and request-changes threads. Jobs run under
  it: a plan job and an apply job.
- "Request changes" is the main counterpart to Approve. The change order
  stays open. The signatures reset when a new plan is attached.
- "Reject and close" is a secondary action, with a required reason. Both
  Request changes and Reject and close need a reason.
- A request-changes thread blocks approval until the reviewer or another
  approver resolves it. The author of the change order never resolves it,
  also when the author is an approver. An agent never resolves it. The
  audit log records each dismissal with its reason. Agents never reject.
  They can only leave blocking comments, as ADR 128 says.
- There is one thread for each concern. A thread has an anchor: the whole
  plan, one resource, several resources, or a group. Threads queue as a
  draft review, and the reviewer submits them together. A thread follows
  its resource addresses into the next plan. When a resource is not in the
  next plan, the thread is outdated, and it still blocks until a person
  resolves it.
- A big plan shows the cause diff first: what the author changed, for
  example a shared local. The effect comes after it. Replacements and
  deletes are never in a group, and the review shows them as "needs
  attention". Identical in-place changes are in a group. A thread can have
  a group as its anchor. The review shows the blast radius and the policy
  results.

The project manager decided earlier, for the owner, that the approvals
across the gates of one phase come from distinct people (ADR 116 decision
6). This ADR applies the same rule to the approval requirements of a
change order (ADR 192).

The project lead gave the design work to an agent, with the decisions
above. This ADR and ADRs 191 to 198 record the decisions of the owner and
the design decisions that follow from them. Each decision of the owner has
the label "(owner)". This pull request holds design only: ADRs, ports and
interfaces, and no implementation.

### One-way doors

Some decisions are hard to undo after the first tag. Each of them has the
label **One-way door** in the text. This list collects them:

| Decision | Why it is hard to undo |
| --- | --- |
| A change order owns the approvals of its jobs (decision 6) | Stored jobs and change orders depend on where an approval lives. |
| All units of a change order are in one namespace (decision 3) | The fence, the backup and the audit scope are per namespace. |
| The digest encoding `cho-digest/1` (ADR 191 decision 4) | Signatures in the audit chain hold the digest. A new encoding needs a new version name. |
| The ID prefixes `cho-`, `cor-`, `cot-` and `coc-` (decision 2, ADRs 192 and 193) | IDs are in audit entries, links and backups. |
| The encoding of the content key of a change group: the prefix `cog-` and the HMAC-SHA-256 with the key of the namespace (ADR 194 decision 5) | Thread anchors and backups hold the key. It is a content key, not an ID. A new encoding stops each thread that follows a change group, and needs a new prefix. |
| Shared code and the tool-neutral API say `plan` (ADR 195 decision 1) | API fields and audit entries hold the word. |
| The audit entry shapes of ADR 197 decision 6 | The audit chain hashes each entry. |
| Comment text is a history item with its own item key (ADR 198 decision 3) | Backups hold the sealed text. A later change needs a migration of each key. The summary of a review is a comment too (ADR 192 decision 1). |

## Decision

1. **A domain package `pkg/changeorder`.** The package is tool-neutral. It
   imports no tool package, no store adapter, no forge adapter and no
   transport. It does not import `pkg/job`, and `pkg/job` does not import
   it. The internal wiring of the server connects them, as ADR 128 decision
   2 does for `pkg/agent`. depguard holds `pkg/changeorder` to the rules of
   the other domain packages.
2. **A change order and its ID.** A change order (`changeorder.Order`) has
   an ID with the prefix `cho-`, a namespace, a title (1 to 200
   characters), a description (at most 16 KiB of Markdown), the principal
   that created it, its plan revisions (ADR 191), its reviews (ADR 192),
   its threads (ADR 193), a state (decision 7) and a version for writes on
   a stale read (`changeorder.ErrConflict`). **One-way door.**
3. **One namespace, many units.** A change order belongs to one namespace.
   Each unit of the change order is an `identity.Object` unit reference
   with its stable unit ID set: tool, namespace and the tool's own name
   (ADR 13). The core never parses the tool's own name. All units are in
   the namespace of the change order. A unit of another namespace fails
   with `changeorder.ErrInvalid`. Each plan revision holds 1 to 100 units,
   and each unit one time. The units of a change order are the units of
   its current revision. **One-way door:** a change across namespaces is two
   change orders.
4. **The authors.** The authors of a change order are the principal that
   created it and each principal that attached a plan revision to it (ADR
   191 decision 2). A principal that attached a plan stays an author, also
   after a later revision. "The author" in the decisions of the owner means
   each of these principals (owner). An agent never becomes an author:
   ADR 196 decision 10 refuses each write of an agent other than a comment.
5. **Jobs run under a change order.** A change order starts two kinds of
   jobs (owner), through `job.Service` and the wiring:
   - A **plan job** for each unit of each plan revision: a runner job with
     the operation `plan`. Its one phase of the kind `plan` stores one
     stored plan (ADR 102).
   - An **apply job** for each unit of the approved revision: a runner job
     with the operation `apply` or `destroy`, which applies the stored plan
     of the plan job of the same unit (decision 9).

   The spec of each job holds the origin `changeorder.Origin`: the change
   order ID and the revision number. The job keeps it, so a reader of a job
   finds its change order. Decision 15 gives the rules that protect the
   origin and `job.Spec.Plan`. Version 1 runs runner jobs only. A write-back
   job applies in the team's CI, so the server cannot bind its apply to a
   stored plan. A write-back change keeps the gates of the job model (ADR
   103).
6. **The change order owns the approvals of its jobs.** The approvals,
   the signatures and the threads are on the change order, never on a gate
   of its jobs (owner). The `job.Decider` adapter gives no gate of the type
   `job.GateApproval` to a job with a change order origin. The policy of
   the decision point `job.start` still applies, in two places:
   - The operation `changeorder.decide` (ADR 192 decision 4) also asks
     `job.start` for each unit of the revision, with the operation of that
     unit (`apply` or `destroy`). Each `approve` requirement that it
     returns joins the set of requirements that `changeorder.Match` fills.
     A `deny` blocks the approval of the revision, as a `deny` of
     `changeorder.review` does.
   - At the start of an apply job, the `job.Decider` adapter still applies
     a `deny` of `job.start` (ADR 114 decision 10). When `job.start` gives
     `approve`, and the stored decisions of the revision do not hold each
     requirement that it gives, the adapter refuses the start with
     `job.ErrPolicyDenied`. An error of the port `job.ChangeOrders` also
     refuses the start: the check fails closed. So a requirement of
     `job.start` that the change order did not meet stops its apply job.

   `jobtest` gets these cases: an apply job start when `job.start` gives
   `approve` and the stored decisions hold its requirement (the start
   passes), the same start when the stored decisions do not hold it, a
   `deny` of `job.start`, and an error of the port. The job model still applies its other rules to these jobs: the freeze, the
   one change at a time on a unit, and the plan check at the dispatch.
   **One-way door.**
7. **States.** A change order has one of nine states
   (`changeorder.State`). Three of them are terminal:

   | State | Meaning |
   | --- | --- |
   | `planning` | The plan jobs of the current revision run. |
   | `plan-failed` | A plan job of the current revision failed, or ended with no stored plan. |
   | `in-review` | Each unit plan of the current revision is ready. The change order waits for its approvals. |
   | `approved` | The signatures on the current revision meet each requirement, and no request-changes thread is open. |
   | `applying` | The apply jobs of the current revision run. |
   | `partially-applied` | Each apply job ended. At least one succeeded, and at least one did not. |
   | `applied` | Terminal. Each apply job succeeded. |
   | `closed` | Terminal. A reviewer rejected and closed it, or an author closed it, each with a reason. |
   | `expired` | Terminal. It saw no write for the idle timeout (decision 12). |

   `approved` is a state that the store computes in the transaction of
   each write, with the function `changeorder.Approved`. It gives
   `approved` only when all of these are true for the current revision:
   - the revision is `ready`,
   - the store holds the policy decisions of the revision (ADR 192
     decision 4). A revision whose operation `changeorder.decide` did not
     end has none,
   - the combined effect of the decisions is not `deny`, and the policy
     did not fail,
   - no resource policy report of a unit plan has a `mandatory` result with
     the outcome `fail`, and no report is unreadable (ADR 194 decision 8),
   - no unit plan has a summary that is `unreadable` (ADR 194 decision 6).
     This check applies also with no policy,
   - the set of requirements is not empty, and each requirement needs at
     least 1 approval,
   - the matching of ADR 192 decision 6 fills each slot of each
     requirement,
   - at least `changeorder.MinApprovals` (1) signatures count, each by a
     principal that is not an author. This check applies also when a
     policy gives the requirements (ADR 192 decision 4),
   - no blocking thread is open.

   A recompute with no stored decisions, or with no requirement, never
   gives `approved`. A table-driven test of `Approved` has a row for each
   condition. A thread that opens on an approved change order moves it back
   to `in-review`. The signatures stay and count again when no thread is
   open (ADR 128 decision 12).
8. **One table of transitions.** `changeorder.Next` is the only place that
   decides a transition. An event that the table does not allow fails with
   `changeorder.ErrTransition`. Any event on a terminal state fails with
   `changeorder.ErrFinished`. A table-driven test checks each pair of state
   and event against this table, which is written out apart from the code:

   | From | Event | To |
   | --- | --- | --- |
   | planning | plans-ready | in-review |
   | planning | plan-failed | plan-failed |
   | planning | attach | planning |
   | plan-failed | attach | planning |
   | in-review | attach | planning |
   | in-review | requirements-met | approved |
   | approved | thread-opened | in-review |
   | approved | signature-void | in-review |
   | planning, plan-failed, in-review, applying, partially-applied | thread-opened | the same state |
   | in-review | signature-void | in-review |
   | approved | attach | planning |
   | approved | apply | applying |
   | applying | jobs-ended-all-succeeded | applied |
   | applying | jobs-ended-some-failed | partially-applied |
   | partially-applied | attach | planning |
   | planning, plan-failed, in-review, approved, partially-applied | reject-and-close (with no digest in planning and plan-failed) | closed |
   | planning, plan-failed, in-review, approved, partially-applied | close | closed |
   | planning, plan-failed, in-review, approved, partially-applied | expire | expired |

   These writes are not events, and do not call `Next`: a reply, a comment
   thread, a resolve, a draft write, the analysis of a unit plan and the
   store of the policy decisions. Each one is possible in each state that is
   not terminal, also in `applying` and `partially-applied`. A reopen of a
   blocking thread is the event `thread-opened`. After each write, the
   store computes `Approved` again. In `in-review`, a result of `approved`
   is the event `requirements-met`. In `approved`, a result that is not
   `approved` is the event `signature-void`, or `thread-opened` for a new
   or reopened blocking thread. In `applying`, a blocking thread stops no
   apply job. It blocks the approval of a later revision.

   These rules in the table need a reason:
   - An attach is possible in each state that is not terminal, except
     `applying`. A new plan while apply jobs run would hide what the jobs
     change. An attach in `applying` fails with `changeorder.ErrApplying`.
   - `reject-and-close` is possible in `planning` and `plan-failed`, where
     the current revision has no digest. There the submit names no digest:
     it closes the change order and signs nothing (ADR 193 decision 6).
   - `reject-and-close` and `close` are not possible in `applying`. A
     person cancels the apply jobs through the job model first (ADR 100
     decision 11). The change order then moves to `partially-applied` or
     `applied`, from the end of its jobs.
   - `partially-applied` is not terminal. An author attaches a new plan for
     the units that did not apply, or closes the change order. The new plan
     needs new signatures (ADR 191 decision 6).
   - `signature-void` applies when the result of `Approved` changes to
     false with no new thread, for example when the approver loses
     `changeorder:approve` on a unit (ADR 192 decision 9).
9. **An apply job applies the plan of its plan job.** The spec of an apply
   job names the stored plan (`pln-`) of the plan job of the same unit in
   the approved revision, in the new field `job.Spec.Plan`. Such a job has
   one phase, of the kind `apply`. The job model checks that plan at the
   dispatch with `job.CheckPlan` (ADR 102 decision 7): the state version
   that it read, its age and its redaction. A stale plan fails the dispatch
   with `job.ErrPlanStale`. The apply job has no plan phase to go back to,
   so it ends `failed` with the reason `plan-stale`, and it changed
   nothing. The dispatch also applies the origin check of decision 15. The
   runner of that apply job reads the part `plan-apply` of the named plan,
   and refuses a part with another hash.
10. **Apply is a request of a person.** A change order in `approved` does
    not apply on its own. A principal asks for the apply (ADR 197). Before
    the transaction, `changeorder.Service` checks for each unit of the
    revision:
    - that the principal holds `unit:run-apply`, or `unit:run-destroy` for
      a `destroy` unit (ADR 103 decision 8), else `identity.ErrDenied`
      names the unit and the action,
    - that the unit has a runner pool that the job model can use (ADR 130),
      else `changeorder.ErrInvalid` names the unit,
    - that the unit plan is not stale (ADR 191 decision 7).

    The store then checks in one transaction that the change order is
    `approved` and that the request names the digest of the current
    revision. It moves the change order to `applying` and starts the
    durable operation `changeorder.apply` with the start key `<change order
    ID>/<revision>`. That operation starts one apply job for each unit
    whose plan has a change (ADR 213 decision 3), with the idempotency key
    `<change order ID>/<revision>/<unit ID>/apply`.
    So a retry after a crash gives one apply job for each unit. The apply
    jobs of one change order run with no order between them. Each unit
    keeps the one change at a time rule of the job model.

    A check before the transaction can pass and the start can still fail,
    for example when a grant changes. Each start failure of the apply of a
    unit makes the apply of that unit `failed`, with the reason, and the
    store records it with `ObserveJob` as an apply that ended (ADR 196
    decision 1). An apply of a unit ends when its job ends or when its
    start failed. When the apply of each unit ended, the store applies
    `jobs-ended-all-succeeded` or `jobs-ended-some-failed`. So no change
    order stays in `applying` because a start failed.
11. **The change order follows its jobs.** The job store starts the
    durable operation `changeorder.sync` in the transaction of each change
    of a job with a change order origin, with the start key `<change order
    ID>/<job ID>/<job version>`. The operation reads the job and applies
    `changeorder.Store.ObserveJob`. So the end of a plan job records its
    stored plan, and the end of the last apply job ends the change order.
    A lost operation is safe to run again: `ObserveJob` with a job version
    that the store already saw changes nothing.
12. **Limits and expiry.** A namespace has at most 1,000 change orders that
    are not terminal. A unit is in at most 16 of them. A change order has
    at most 100 plan revisions. A change order that is not terminal and
    that sees no write for 30 days ends `expired`
    (`changeorder.DefaultIdleTimeout`). The schedule `changeorder.reconcile`
    runs each hour. It expires idle change orders and applies `ObserveJob`
    to each job of a change order whose sync operation is lost. A start
    above a limit fails with `changeorder.ErrLimit`. Installation settings
    can change these limits later.
13. **The change order is namespace data.** The store checks the namespace
    fence and appends the audit entries in the transaction of each write.
    The backup of a namespace holds its change orders (ADR 196 decision 6).
14. **Agents.** An agent reads a change order and leaves comments, when a
    later ADR gives it a tool. It leaves comments and blocking comments only
    (owner, ADR 128 decision 10). An agent records no verdict: it submits
    its blocking threads in a review of the verdict `comment` (ADR 192
    decision 2). It can write its own draft review, because its threads
    queue in a draft before the submit (ADR 196 decision 10). An agent never
    creates a change order, attaches a plan, approves, requests changes,
    rejects, closes, resolves or reopens a thread, or asks for an apply.
    Each of these fails with `changeorder.ErrAgentDenied`. The agent tool
    set of ADR 122 has no tool for change orders. A new agent tool needs a
    later ADR.
15. **Only the wiring sets the origin and the plan of a job.** The fields
    `job.Spec.ChangeOrder` and `job.Spec.Plan` give a job the rules of
    decisions 6 and 9: no approval gate, and an apply of a named stored
    plan. So no caller other than the wiring of the change order sets them:
    - The job start route and `job.Service.Start` refuse a spec with
      `ChangeOrder` or `Plan` set, with `job.ErrInvalid`. The request body
      of the route has no field for them.
    - The wiring starts the jobs of a change order with the method
      `StartForChangeOrder` of the new type `job.ChangeOrderStarter`. That
      type is apart from `job.Service`, and only the wiring constructs it.
      No handler gets it: the HTTP adapters get `job.Service` only. A
      depguard or forbidigo rule for `internal/api/...` refuses each use of
      `job.ChangeOrderStarter` and of its constructor. That rule is the
      structural check. The route test also checks that no handler of the
      job routes calls it.
    - As a second layer, `StartForChangeOrder` and the `job.Decider` adapter
      check, through the new port `job.ChangeOrders` that the wiring
      implements on `changeorder.Store`, that the origin names a change
      order of the namespace of the job, and a revision of that change order
      that holds the unit of the job. For a plan job, the revision is the
      current revision. For an apply job, the change order is `applying` on
      that revision, and `Spec.Plan` is the plan ID of the unit plan of the
      same unit in that revision. A check that fails refuses the start with
      `job.ErrInvalid` and creates no job. An error of the port fails the
      start: the check fails closed.
    - The dispatch check of the job store applies the same check to an
      apply job, in the transaction of the dispatch. A check that fails
      ends the job `failed` with the reason `origin-mismatch`, before any
      change.

    `jobtest` gets these cases: `Start` with `ChangeOrder` set, `Start`
    with `Plan` set, an apply job whose change order is not `applying`, an
    origin with another revision, a `Spec.Plan` of another unit, a
    `Spec.Plan` that is not the plan of the unit, a change order of another
    namespace, an error of the port, a dispatch after the check stops to
    pass, and a start that passes each check.
16. **No job thread on a job of a change order.** The threads of the
    change order hold the review of its jobs (decision 6). So
    `job.Service.Comment` and `job.Service.ResolveThread` (ADR 128
    decisions 11 to 13) refuse a job with a change order origin, with
    `job.ErrInvalid`. The error names the change order and says to comment
    on its threads. `jobtest` gets a case for each method.

## What this ADR replaces

ADR 1 forbids edits to the text of an accepted ADR. So this ADR records
what it replaces, and the status line of each ADR that it replaces names
it.

- **ADR 100, decision 4:** the table of the default phases. It gets one
  row: an apply job with `job.Spec.Plan` set has one phase, of the kind
  `apply` (decision 9).
- **ADR 102, decision 2 and decision 8:** "Only the runner of the apply
  phase of the same job reads it." The runner of an apply job that names
  the plan in `job.Spec.Plan` also reads it (decision 9).
- **ADR 102, decision 7:** a stale plan sends the job back to its plan
  phase. An apply job with `job.Spec.Plan` set has no plan phase, so a
  stale plan ends it `failed` (decision 9).
- **ADR 103, decision 1 and decision 2:** the policy decision gives the
  approval gates of each job. A job with a change order origin gets no
  approval gate. Its change order holds the approvals (decision 6).
- **ADR 128, decisions 11 to 13:** a blocking comment opens a
  request-changes thread on a job, which blocks approval until a person
  resolves it. A job with a change order origin gets no comment and no
  thread. The threads of its change order apply (decision 16).
- **ADR 147, decision 9:** "A job is the change record. No new object is
  necessary." For a change under a change order, the change order and its
  jobs are the change record (ADR 198 decision 6).

## Consequences

- One review covers a change of many units. A reviewer approves one plan
  revision, not one job for each unit.
- A change order needs a runner pool for each unit. A team that applies
  only in its own CI uses write-back, and write-back jobs keep their own
  gates.
- The maximum plan age of the job model (24 hours by default) also limits
  the time from the plan to the apply. A review that takes longer needs a
  new plan revision, and so new signatures.
- The apply jobs of one change order have no order. A unit that reads the
  outputs of another unit of the same change order can apply before that
  unit. The blast radius in the review shows such links (ADR 194).
- `pkg/job` changes: `job.Spec.Plan`, `job.Spec.ChangeOrder` (the origin),
  the phase row of decision 9, the reader rule of the part `plan-apply`,
  the decider rule of decision 6, the sync operation of decision 11, the
  type `job.ChangeOrderStarter` and the port `job.ChangeOrders` of decision
  15, and the refusal of decision 16. `jobtest` gets a case for each. The
  lint configuration gets the rule of decision 15 for `internal/api/...`.
- `GLOSSARY.md` gets the terms of ADRs 190 to 198.

## Open questions

- Does an apply start on its own at approval, as a setting of the change
  order or of the namespace? Version 1 asks a person for each apply.
- Do the apply jobs of one change order follow the order of the dependency
  edges between their units (ADR 153)?
- Can a change order hold write-back units, with a plan job on a runner
  for the review and a deliver job after the approval?
- Does a later ADR give agents a tool that creates change orders, with a
  confirmation by the person who asked?

## Changelog

- 2026-11-13, wording refinement (ADR 365): decision 10 said "one apply job
  for each unit". It now says "for each unit whose plan has a change". ADR 213
  decision 3 already said that a unit plan with no change starts no apply job.
