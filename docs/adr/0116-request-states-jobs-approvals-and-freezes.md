# 116. Request states, jobs, approvals and freezes

Status: accepted · 2026-10-17. Decisions 5 and 6 superseded in part by ADR 225

## Context

ADR 110 decides that each submit of a Catalog request makes a revision, and
that a revision starts one job through `job.Service`. ADR 100 decision 6
gives the eleven job states. ADR 103 gives the gates, the freezes and
break-glass.

The owner decided: OpenDustpan approvals add to the approvals of the forge,
and never gate a team that runs only in its own CI. Change freezes and
break-glass come from the job model.

## Decision

1. **The operation of a revision.** The server compares the document of a
   revision with the last applied revision of the request
   (`catalog.OperationFor`):

   | Last applied | `ensure` | Operation | Runner job | Write-back job |
   | --- | --- | --- | --- | --- |
   | none | `present` | `create` | `apply` | `apply`, the file is new |
   | `present` | `present` | `update` | `apply` | `apply` |
   | `present` | `absent` | `destroy` | `destroy` | `apply`, the file says `absent` |
   | none | `absent` | refused with `catalog.ErrInvalid` | | |
   | `absent` | `absent` | no job (ADR 111 decision 6) | | |
   | `absent` | `present` | `create` | `apply` | `apply` |

   A write-back job always does `apply` (ADR 101 decision 5). In git, the
   team's CI reads `ensure` from the render and runs the destroy of its tool.
   A table-driven test covers the table.
2. **Revision states.** A revision has one of twelve states
   (`catalog.RevisionState`). Six of them are terminal:

   | State | Meaning |
   | --- | --- |
   | `accepted` | The submit passed its checks. The Catalog makes the unit and starts the job. |
   | `queued` | The job waits in the queue. |
   | `awaiting-approval` | The job waits at a gate. |
   | `running` | A phase of the job runs, or stops after a cancel. |
   | `awaiting-merge` | A write-back job delivered a review request, and the forge did not merge it yet. |
   | `awaiting-state` | The change is in git. The team's CI did not write the final state version yet. |
   | `applied` | Terminal. The job succeeded. |
   | `failed` | Terminal. The job failed, or the start failed. |
   | `rejected` | Terminal. A person, a review request or a recheck rejected it. |
   | `canceled` | Terminal. A person canceled it. |
   | `expired` | Terminal. The job expired, or the revision passed its deadline (decision 13). |
   | `superseded` | Terminal. A revision from git replaced it before its job delivered (decision 9). |

3. **One revision in flight.** A submit while a revision of the same
   request is not terminal fails with `catalog.ErrInFlight`. The requester
   cancels the revision in flight first. A render from git does not fail:
   it supersedes the revision in flight in the same store write, or fails
   with `catalog.ErrConflict` when the change of that revision is already in
   git (decision 9). So two revisions never race for one unit, and a
   reservation always belongs to one revision.
4. **Job states give revision states.** After `accepted`, the state of a
   revision comes from the state of its job. `catalog.StateOf` holds the
   map, and a table-driven test covers each job state:

   | Job state | Revision state |
   | --- | --- |
   | `queued` | `queued` |
   | `awaiting-approval` | `awaiting-approval` |
   | `dispatched`, `running`, `canceling` | `running` |
   | `awaiting-state`, delivered as a review request with no merge | `awaiting-merge` |
   | `awaiting-state`, a direct push or a merged review request | `awaiting-state` |
   | `succeeded` | `applied` |
   | `failed` | `failed` |
   | `rejected` | `rejected` |
   | `canceled` | `canceled` |
   | `expired` | `expired` |

   The Catalog stores the state of the revision with the version of the job
   that it read. A job change gives the new state through the job events
   (ADR 100, "Open questions" on outgoing events) or the next read. The job
   is the record. The revision state is a copy for lists and filters, and a
   read of one revision reads the job again.
5. **The start of a revision.** A submit that passes its checks stores the
   revision `accepted`, writes its reservation and its count in the windows
   of the `requests` rules through the `quota.Tx` of the same transaction
   (ADR 115 decision 5), and starts a durable operation `catalog.start` with
   the start key `<request ID>/<revision>`, all in one transaction. A
   submit that fails keeps none of them. The operation makes the unit (ADR 110 decision 6), then
   calls `job.Service.Start`. The API runs the operation at once and waits
   for it up to 10 seconds, so a submit with no problem answers with the job.
   Each step is idempotent, so a retry after a crash gives one unit and one
   job. The operation first reads the revision, and stops when the revision
   is terminal. A job that the operation started may exist while
   `catalog.Revision.Job` is empty, so for an API revision with no job,
   `catalog.ExpiryOf` first looks up the job by the idempotency key
   `catalog.Revision.Key()`. A start error of the job model ends the revision:

   | `job.Service.Start` error | Revision |
   | --- | --- |
   | `job.ErrPolicyDenied` | `rejected` |
   | `job.ErrFrozen` | `rejected`, with the freeze |
   | `job.ErrAgentDenied` | `rejected` |
   | `job.ErrLimit` | `failed` |
   | `job.ErrInvalid`, `job.ErrUnsupported` | `failed` |
   | another error | the operation retries with the backoff of the durable runner |

   Before it stores anything, the submit asks the freeze store whether a
   freeze of the namespace is active. A submit during a freeze with no
   break-glass fails with `job.ErrFrozen` and stores no revision.
6. **The Catalog decision gives the gates.** The combined decision of the
   `submit` point (ADR 114 decision 4) is in the revision. The job decision
   of the job of the revision joins it (ADR 114 decision 10): the
   `job.Decider` adapter finds the revision through the Catalog origin of
   the job spec (ADR 110, "Changes requested in the job model", item 1).
   - `deny` cannot reach the job: the submit already failed.
   - `approve` gives one gate of type `job.GateApproval` for each
     requirement of the combined decision (ADR 114 decision 4), all on the
     first phase that changes: `apply` for a runner job, `deliver` for a
     write-back job. The job model needs each gate of a phase, so the change
     needs each requirement: an `approve` by `security` and an `approve` by
     `dba` give two gates. The decision of a phase needs several gates
     (ADR 110, "Changes requested in the job model", item 5). Each gate binds its approvals to the plan when a
     plan phase comes before. Each gate sets `AllowSelf` to false
     (`catalog.GateFor`), so the requester never counts as an approver of
     its own request, also when it is a member of a group of the gate.
     The approvals across the gates of one phase come from distinct people.
     One approver who is in two required groups passes one gate, not both.
   - `allow` gives no gate.

   An approver approves through `job.Service.Approve`, as for any job, and
   needs `job:approve` on the unit. The Catalog has no approval route of its
   own.
7. **Rechecks run through the job.** At `approval` and at `apply`
   (ADR 113 decision 8), the job model calls the Catalog through the
   precondition port (ADR 110, "Changes requested in the job model", item
   2). A failed recheck rejects the job. The revision then ends `rejected`,
   with each failure, and the Catalog releases its reservation. A recheck
   that cannot run, for example a policy rule that fails to run, records no
   approval and dispatches nothing. The job waits, and the timeout of its
   gate still ends it.
8. **Freezes and break-glass.** A submit can carry a break-glass with a
   reason. The Catalog passes it to `job.StartRequest.BreakGlass`, and the
   job model applies ADR 103 decision 6: the action `namespace:break-freeze`
   on the namespace, a person and not an agent, and an audit entry of the
   kind `override`. The revision records the break-glass. A freeze does not
   stop a submit of a request in git whose job only waits for a review: the
   deliver phase changes the code in git, so the freeze holds it, as for any
   write-back job.
9. **A change in git with no API.** A person can change a request file in
   git with no submit, and the team's CI then asks for a render of it:
   - **The server reads the file.** The render takes the repository, the
     commit and the path, and reads `request.json` at that commit from the
     forge itself (`catalog.RequestFiles`). It never takes a document from
     the caller. It checks that the path and the document agree (ADR 111
     decision 7).
   - **The render has the actions of its operation.** It needs
     `catalog:render` and the action of the operation that the document
     gives: `catalog:request` on the blueprint for a create,
     `unit:edit-inputs` on the unit for an update, and
     `unit:request-destroy` on the unit for a destroy (ADR 117 decision 2).
   - The render checks the document at `apply` (ADR 113 decision 8). A
     failure refuses the render. The policy effect `approve` does not gate
     the render: the forge approvals of the team are the approvals of a
     change in git. The render records the effect in the revision.
   - When the document differs from the last revision, the render stores a
     revision with the origin `git`, the commit, and the state
     `awaiting-state`. It has no job. A `create` from git reserves its unit,
     as a submit does, and the store makes the empty unit in the same
     transaction (`catalog.Submission.Provision`) through `catalog.UnitTx`,
     the provisioner on the open transaction of the store (ADR 110
     decision 6). So the revision, its reservation and its unit commit
     together or not at all.
   - The next final state version of the unit at exactly that commit, or
     after the render when the tool reports no commit, makes the revision
     `applied` (`catalog.Store.ObserveStateVersion`). A state version at
     another commit applies no revision, also at a commit that descends from
     the commit of the revision. The Catalog applies this rule in the
     transaction of the state write, through the same tool-neutral report
     as the job model (`job.StateVersion`, ADR 100 decision 15).
   - **The team's CI renders each commit.** A CI that applies a commit with
     a request file asks for the render of that commit, also when an older
     commit is not applied yet. The render of a newer commit supersedes the
     older revision (below), so the state version of the newer commit
     applies the newer revision. A CI that applies a commit with no render
     leaves the revision `awaiting-state` until its deadline.
   - **Supersede.** When a revision of the request is in flight, the render
     supersedes it in the same store write (`catalog.Submission.Supersedes`):
     - When the job of that revision did not deliver its change for good,
       the revision ends `superseded`, and the Catalog cancels its job
       after the store write. A canceled job no longer changes the
       revision: the revision follows its job only until it ends.
     - When the job delivered its change for good, a direct push or a
       merged review request, the change is in git and waits for its state
       version. The render then fails with `catalog.ErrConflict`, which says
       that the change of the revision is already in git and names the job
       (`catalog.CheckSupersede`). The CI renders again after that state
       version.
     - When both revisions are creates, the new revision takes over the
       unit and the reservation of the old one. The unit stays, and the
       Catalog reserves nothing twice. The store moves the end time of the reservation
       to the deadline of the new revision (ADR 115 decision 5), and the old
       revision starts no `catalog.quota` operation.
     - The Catalog reads the job before the store write. A job that
       delivers between that read and the cancel keeps its own rules: its
       change is in git, and its state version closes the job, not the new
       revision.
10. **Cancel.** A cancel of a revision cancels its job through
    `job.Service.Cancel`, and needs `job:cancel` on the unit. The error of
    the job model is the error of the cancel, for example
    `job.ErrDelivered` for a change that is in git for good. A cancel of an
    `accepted` revision with no job ends it `canceled` at once. A revision
    from git has no job: a person changes the file in git instead.
11. **The empty unit of a failed create.** When a create revision ends in a
    terminal state other than `applied`, and its unit has no state version,
    the Catalog deletes the unit through `catalog.UnitProvisioner` in the
    durable operation `catalog.quota`, and releases the reservation. A unit
    with a state version stays. Its request keeps the revision as the last
    one, and the settled count counts the unit.
12. **The record of a request.** `catalog.Request` holds the request ID with
    the prefix `req-`, the namespace, the blueprint, the name, the home, the
    unit reference with its unit ID, the quota keys of the unit (ADR 115
    decision 4), the number of the last revision, the number of the last
    applied revision, and a version for writes on a stale read
    (`catalog.ErrConflict`). A request record is namespace data:
    the store checks the fence and appends the audit entries in the
    transaction of each write, and the backup of the namespace holds it.

13. **Each revision has a deadline.** The store takes each revision with a
    deadline (`catalog.Revision.Deadline`, `catalog.RevisionDeadline`),
    which the Catalog knows at the submit or at the render:
    - A revision from git: the render, plus 14 days, the state wait of a
      write-back job (`job.DefaultStateTimeout`), plus a grace of 1 day.
    - A revision with a job: the submit, plus 30 days, the sum of the time
      limits of the job model for a Catalog job (the queue, one approval
      gate, the dispatch of a plan and of an apply, and the state wait),
      plus a grace of 1 day.

    A job can live longer than this deadline. It goes back to the queue
    after each phase and after each approval, with a new time limit each
    time, a confirmation gate can wait on each phase that changes, the job
    decision can add a gate to each phase, and the steps of a job can run
    for many hours. So the deadline ends a revision only when its job
    ended. The schedule `catalog.expire` runs at least once each hour. It
    applies `catalog.ExpiryOf` to each revision in flight, from
    `catalog.ExtendLead` (half of the grace, 12 hours) before the deadline:
    - A revision with no job, for example a revision from git that waits
      for its state version, ends `expired` (`catalog.Store.Change`). For a
      create, the change starts the `catalog.quota` operation.
    - A revision whose job ended takes the state of its job
      (`catalog.StateOf`). For a create, the change starts the
      `catalog.quota` operation.
    - A revision whose job has not ended keeps its state. The schedule
      cancels the job when the job model allows it, and, from
      `catalog.ExtendLead` before the deadline, moves the deadline of the
      revision and the end time of its reservation by the grace of 1 day
      (`quota.ReservationGrace`), in one transaction
      (`catalog.Store.Extend`, which calls `quota.Tx.SetEnd`). The schedule
      checks the revision again on each run, until its job ends.

    So `catalog.quota`, which releases the reservation and deletes the
    empty unit through `catalog.UnitProvisioner.Remove`, runs only after the
    job of the revision ended. The reservation of a create ends at the
    deadline (ADR 115 decision 7), and the deadline moves while the job
    has not ended. So the reservation of a revision whose job has not ended
    stays open, and its unit stays, with no gap: the counter counts a
    reservation only while its end time is after the time of the count, so
    the schedule moves the end time before the deadline, not at it. A lost
    release can hold a slot for up to the grace plus one schedule interval
    after the end of the job. The store refuses a revision with no
    deadline, and a reservation that does not end at the deadline of its
    revision, with `catalog.ErrInvalid`.

## Consequences

- A requester sees one list of revisions for each request, with the job of
  each. A person who reads a job sees the request through the Catalog
  origin of the job.
- A request in git with a review request is `awaiting-merge` for as long as
  the forge review takes. The console shows the review request.
- A team that changes request files in git by hand gets every check at the
  render, and no OpenDustpan approval.
- A failed create leaves no empty unit, unless the tool wrote state.
- A request in git whose CI never applies its change ends `expired` 15
  days after the render, and frees its reservation.

## Open questions

- Does a new submit cancel the revision in flight, instead of the
  `catalog.ErrInFlight` failure? A cancel of a write-back job after its
  delivery fails, so the rule would need an exception.
- Can a queued Catalog job get break-glass after its start (ADR 103 "Open
  questions")? This ADR follows the job model.
- Does a state version at a commit that descends from the commit of a
  revision from git apply the revision? Version 1 matches only the exact
  commit, and asks the CI to render each commit. A match on a descendant
  needs the commit graph of the forge in the transaction of the state
  write.
- When the team's CI never calls the render for a change in git, the
  Catalog never sees the change. Does a forge webhook on a push to a request
  file need to store a revision too?
