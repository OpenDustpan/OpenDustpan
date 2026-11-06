# 196. Change order ports, errors, fences and conformance suites

Status: accepted · 2026-10-18 · part of ADR 190. Decisions 1 and 5 superseded in part by ADR 230

Changed by:

- ADR 222 changes decisions 1 and 5: errors, status codes and audit entries of
  gates.
- ADR 230 changes decisions 1 and 5: the errors and status codes of gates.

## Context

CLAUDE.md asks for small ports in the package that consumes them, each
documented with the errors that it returns, and a conformance suite for
each port before any adapter. Each write inside a namespace carries a
`namespace.Fence`, and the store checks it in the same transaction. ADRs
190 to 195 decide the change order. This ADR gives its ports.

## Decision

1. **The errors.** `pkg/changeorder` holds these sentinels. Each port
   wraps them with `fmt.Errorf("...: %w", err)`, and an adapter translates
   each driver error into one of them.

   | Error | When |
   | --- | --- |
   | `ErrInvalid` | A request with an empty or bad field. A unit of another namespace, or a unit with no runner pool. A base commit that is not an ancestor of the head, or that is the head (an empty cause diff). An anchor that names no change of the plan. A second `approve` by the same principal on the same revision. |
   | `ErrNotFound` | An unknown change order, revision, thread or draft, or a caller without `changeorder:read` on each unit (decision 9). |
   | `ErrConflict` | A write on a stale read of the change order. A write with an access version of the actor or of a signer that changed (ADR 192 decision 9). |
   | `ErrTransition` | An event that `Next` does not allow in the state. |
   | `ErrFinished` | A write on a terminal change order. |
   | `ErrApplying` | An attach, a close or a reject while apply jobs run. |
   | `ErrLimit` | A write above a limit. The error names the limit. |
   | `ErrPlanChanged` | A digest that is not the digest of the current revision. |
   | `ErrPlanStale` | An apply with a stale unit plan. The error names each unit. |
   | `ErrNotReady` | An approve or an apply before the revision is `ready`, or before the store has its policy decisions. |
   | `ErrNotApproved` | An apply of a change order that is not `approved`. |
   | `ErrChangesRequested` | An approve or an apply while a blocking thread is open. The error names the threads. |
   | `ErrSelfApproval` | An approve, a request for changes or a reject by an author. |
   | `ErrNotAuthor` | A close by a principal that is not an author, also with `changeorder:author` on each unit (ADR 192 decision 8). |
   | `ErrNotEligible` | A verdict or a resolve by a principal that is not an eligible approver. This includes the principal that opened a blocking thread and lost `changeorder:approve` (ADR 193 decision 9). |
   | `ErrAuthorThread` | A blocking thread, or a reopen of one, by an author. |
   | `ErrSelfResolve` | A resolve of a blocking thread by an author. |
   | `ErrReasonRequired` | A request for changes, a reject, a close, a dismissal or a reopen with no reason. |
   | `ErrAgentDenied` | A write of an agent that is not a reply, a write of its own draft, or a review of the verdict `comment`. A read or a submit of the draft of its person by an agent. That review can hold comment threads, replies and blocking threads (ADR 190 decision 14). |
   | `ErrPolicyDenied` | An approve of a revision that a policy denied (ADR 192 decision 4, ADR 194 decision 8). |
   | `ErrPolicyFailed` | An approve while the policy of the revision failed and no retry succeeded, also for a decision with a requirement that is not valid. For `invalid-requirement`, a retry succeeds only after the policy changes. |
   | `ErrPolicyUnreadable` | An approve of a revision with a resource policy report that the `PlanReader` cannot read. The error names the unit and the parse error (ADR 194 decision 8). |
   | `ErrSummaryUnreadable` | An approve of a revision with a unit plan whose summary is `unreadable`, also with no policy. The error names the unit and the error of the `PlanReader` (ADR 192 decision 4, ADR 194 decision 6). |
   | `ErrToolUnsupported` | A `PlanReader` that does not know the tool of a unit. |
   | `ErrPlanFormat` | A readable plan that the `PlanReader` cannot parse. |
   | `ErrForgeUnavailable` | A forge that does not answer for a cause diff. |

   The errors of the `job` package do not cross into this package. The
   wiring maps them, and a test of the wiring checks each row:

   | `job` error at a plan or apply start | `changeorder` result |
   | --- | --- |
   | `job.ErrPolicyDenied`, `job.ErrFrozen` with no break-glass, `job.ErrLimit`, `job.ErrInvalid` | The unit plan or the apply of that unit is `failed`, with the reason. |
   | `job.ErrUnitBusy` | The start returns a queued job. Nothing changes (ADR 100 decision 10). |
   | `job.ErrPlanStale` at the dispatch of an apply job | The apply of that unit is `failed` with the reason `plan-stale` (ADR 190 decision 9). |
   | `identity.ErrDenied` | The unit plan or the apply of that unit is `failed` with the reason `denied`, and the record names the missing action. |
   | another error | The durable operation retries with its backoff for at most 1 hour. Then the unit plan or the apply of that unit is `failed` with the reason `start-failed`. |

   A failed apply start of a unit counts as an apply of that unit that
   ended (ADR 190 decision 10). So each start failure moves the change
   order out of `applying` when each other apply ended, and no change order
   stays in `applying`. A test of the wiring checks each row.

2. **The store port.** `changeorder.Store` persists change orders. Each
   method that writes takes a `namespace.Fence` and the audit entries of
   the write, and appends them in the transaction of the write. Each one
   returns `namespace.ErrFenced` for a stale fence, and changes nothing on
   an error.

   Each method that writes also returns `ErrNotFound` for an unknown change
   order or a fence of another namespace (decision 6), `ErrFinished` for a
   terminal change order where the method says so, and
   `namespace.ErrFenced`. Each method that takes a version returns
   `ErrConflict` for a stale version. The comments name the other errors.

   ```go
   type Store interface {
       // Create stores o in planning with revision 1, and starts the
       // operation changeorder.plan. ErrInvalid for a bad field, a unit of
       // another namespace or a fence of another namespace. ErrLimit.
       Create(ctx context.Context, o Order, f namespace.Fence, e []audit.Entry) (Order, error)
       // Get reads one change order. ErrNotFound.
       Get(ctx context.Context, id ID) (Order, error)
       // List reads one page. ErrInvalid for a bad filter or cursor.
       List(ctx context.Context, q Filter) (Page, error)
       // Attach adds revision r, voids the signatures of each earlier
       // revision, and starts changeorder.plan. ErrInvalid,
       // ErrTransition, ErrApplying, ErrFinished, ErrConflict, ErrLimit.
       Attach(ctx context.Context, id ID, r Revision, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       // ObserveJob applies the state of one job of the change order, or
       // a start that failed. A job version that the store saw changes
       // nothing. ErrInvalid for a job of another revision or unit.
       // ErrFinished for a terminal change order.
       ObserveJob(ctx context.Context, j JobState, f namespace.Fence) (Order, error)
       // SetAnalysis stores the metadata of the summary of one unit
       // plan, and marks the revision ready when each analysis ended.
       // ErrInvalid for a unit that is not in rev. ErrTransition for a
       // revision that is superseded. ErrFinished.
       SetAnalysis(ctx context.Context, id ID, rev int, s SummaryMeta, f namespace.Fence) (Order, error)
       // SetRequirements stores the policy decisions of a ready revision.
       // ErrNotReady for a revision that is not ready. ErrTransition for
       // a revision that is superseded. ErrFinished.
       SetRequirements(ctx context.Context, id ID, rev int, d Decisions, f namespace.Fence, e []audit.Entry) (Order, error)
       // Submit writes review r with its threads, its signature and the
       // change of the state, and deletes the draft. It checks the rules
       // of Eligible, Match, Follow, Approved and Next in its
       // transaction, with el (ADR 192 decision 9). ErrInvalid,
       // ErrPlanChanged, ErrNotReady, ErrChangesRequested,
       // ErrSelfApproval, ErrNotEligible, ErrAuthorThread,
       // ErrReasonRequired, ErrAgentDenied, ErrPolicyDenied,
       // ErrPolicyFailed, ErrPolicyUnreadable, ErrSummaryUnreadable,
       // ErrApplying for a reject-and-close, ErrTransition, ErrFinished,
       // ErrConflict, ErrLimit. ErrInvalid for a second approve by the
       // same principal on the same revision.
       Submit(ctx context.Context, id ID, r Review, el Eligibility, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       // Reply adds comment c to thread t. ErrNotFound for an unknown
       // thread. ErrInvalid, ErrFinished, ErrLimit.
       Reply(ctx context.Context, id ID, t ThreadID, c Comment, f namespace.Fence, e []audit.Entry) (Order, error)
       // Resolve closes thread t, with el. ErrNotFound for an unknown
       // thread. ErrSelfResolve, ErrNotEligible, ErrAgentDenied,
       // ErrReasonRequired, ErrTransition for a thread that is resolved,
       // ErrFinished, ErrConflict.
       Resolve(ctx context.Context, id ID, t ThreadID, r Resolution, el Eligibility, f namespace.Fence, e []audit.Entry) (Order, error)
       // Reopen opens thread t again, with el. ErrNotFound for an unknown
       // thread. ErrAuthorThread, ErrNotEligible, ErrAgentDenied,
       // ErrReasonRequired, ErrTransition for a thread that is open,
       // ErrFinished, ErrConflict.
       Reopen(ctx context.Context, id ID, t ThreadID, r Resolution, el Eligibility, f namespace.Fence, e []audit.Entry) (Order, error)
       // StartApply moves an approved change order to applying and
       // starts changeorder.apply, with el. ErrNotApproved,
       // ErrPlanChanged, ErrChangesRequested, ErrNotReady,
       // ErrAgentDenied, ErrFinished, ErrConflict.
       StartApply(ctx context.Context, id ID, a ApplyRequest, el Eligibility, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       // Close ends the change order closed. ErrReasonRequired,
       // ErrNotAuthor, ErrApplying, ErrAgentDenied, ErrFinished, ErrConflict.
       Close(ctx context.Context, id ID, c Closure, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       // Expire ends an idle change order. ErrTransition when it saw a
       // write after now minus DefaultIdleTimeout, or when it is applying.
       // ErrFinished.
       Expire(ctx context.Context, id ID, now time.Time, f namespace.Fence, e []audit.Entry) (Order, error)
       // PutDraft stores the draft of d.Key. A draft write appends no
       // audit entry. ErrInvalid, ErrLimit above 100 items, ErrFinished.
       PutDraft(ctx context.Context, id ID, d Draft, f namespace.Fence) error
       // GetDraft reads the draft of k: the subject and the agent
       // reference, empty for a person. ErrNotFound when it has none.
       GetDraft(ctx context.Context, id ID, k DraftKey) (Draft, error)
       // DeleteDraft deletes the draft of k. ErrNotFound when it has
       // none.
       DeleteDraft(ctx context.Context, id ID, k DraftKey, f namespace.Fence) error
   }
   ```

   `Submit`, `Resolve`, `Reopen` and `StartApply` read the threads, the
   signatures, the requirements and the access versions of the actor and of
   each signer in the same transaction as their write. So two reviewers that
   write at the same time never make an approved change order with an open
   blocking thread, and a revoke between the check and the commit fails the
   write with `ErrConflict` (ADR 192 decision 9).
3. **The cause diff port.** `changeorder.CauseDiffs` reads a diff from the
   forge. A forge adapter implements it in the wiring.

   ```go
   type CauseDiffs interface {
       // Diff answers the file diff of loc from base to head, up to
       // limit bytes. ErrNotFound for an unknown repository or commit,
       // ErrForgeUnavailable for a forge that does not answer in 10
       // seconds. A diff above limit answers its first part with
       // Truncated set.
       Diff(ctx context.Context, loc Location, base, head string, limit int64) (Diff, error)
       // IsAncestor answers whether base is an ancestor of head in the
       // repository of loc. ErrNotFound for an unknown repository or
       // commit, ErrForgeUnavailable as for Diff.
       IsAncestor(ctx context.Context, loc Location, base, head string) (bool, error)
   }
   ```

4. **The plan reader port.** `changeorder.PlanReader` turns the readable
   plan of one tool into the tool-neutral summary (ADR 194 decision 3).
   Each tool package implements it. The wiring picks the reader by the
   tool of the unit.

   ```go
   type PlanReader interface {
       // Tool names the tool of the reader: "pulumi" or "opentofu".
       Tool() string
       // Summarize reads the part plan-display, masked, and the optional
       // part policy-report. ErrPlanFormat for a plan that it cannot
       // parse. It never answers an unmasked value.
       Summarize(ctx context.Context, display io.Reader, report io.Reader) (UnitSummary, error)
   }
   ```

   A reader reads at most the size of the part (`job.MaxPlanPart`), and a
   summary holds at most 100,000 resource changes. A larger plan fails
   with `ErrPlanFormat` and the limit.
5. **The jobs port.** `changeorder.Jobs` is what the change order needs of
   the job model. The wiring implements it on `job.Service`, and converts
   the types field by field (ADR 128 decision 2).

   ```go
   type Jobs interface {
       StartPlan(ctx context.Context, a Actor, u UnitPlan, key string) (JobRef, error)
       StartApply(ctx context.Context, a Actor, u UnitPlan, key string, bg *BreakGlass) (JobRef, error)
       Cancel(ctx context.Context, a Actor, j JobRef, reason string) error
       // PlanFresh answers ErrPlanStale with the cause when the stored
       // plan of u is stale (job.CheckPlan).
       PlanFresh(ctx context.Context, u UnitPlan) error
   }
   ```

   The policy decision uses the port `policy.Engine` (ADR 114), and the
   blast radius uses `graph.Walk` (ADR 162), each through the wiring.
6. **Fences.** Each write carries the fence of the namespace of the change
   order. A change order is never in the server scope, so the store
   refuses a zero fence. A create with the fence of another namespace
   fails with `ErrInvalid`. A write on a stored change order with such a
   fence fails with `ErrNotFound`, so the answer does not tell whether the
   change order exists in another namespace (ADR 147 decision 16). A stale
   fence fails with `namespace.ErrFenced`. The store also refuses a write
   whose audit entry names another namespace. The tables of the change
   order are data tables, so the backup of the namespace holds them. The
   table of drafts is the one exception: the backup leaves it out (ADR 198
   decision 4). The durable operations of decision 7 are control tables
   (ADR 72 decision 5).
7. **Durable operations.** The change order starts these operations of ADR
   72, each in the transaction of the write that needs it:

   | Kind | Start key | Work |
   | --- | --- | --- |
   | `changeorder.plan` | `<change order ID>/<revision>` | Start the plan jobs, then cancel the plan jobs of the revision before. |
   | `changeorder.analyze` | `<change order ID>/<revision>/<unit ID>` | Summarize one unit plan (ADR 194 decision 6). |
   | `changeorder.decide` | `<change order ID>/<revision>` | Ask the policy at `changeorder.review` and store the decisions. |
   | `changeorder.apply` | `<change order ID>/<revision>` | Start the apply jobs. |
   | `changeorder.sync` | `<change order ID>/<job ID>/<job version>` | Apply `ObserveJob` (ADR 190 decision 11). The job store starts it. |

   The schedule `changeorder.reconcile` runs each hour (ADR 190 decision
   12).
8. **The service.** `changeorder.Service` is what the REST API calls. It
   takes an actor with the principal, the agent reference and the
   credential (ADR 128 decisions 1 and 6), checks the actions of ADR 197,
   writes the audit entries, and calls the store. It has a method for each
   route of ADR 197.
9. **What a caller sees.** Each method that takes a change order ID returns
   `ErrNotFound` when the caller lacks `changeorder:read` on a unit of the
   current revision. So a caller cannot learn that the change order
   exists. With `changeorder:read` on each unit, a missing action returns
   an error that wraps `identity.ErrDenied`.
10. **The rules of agents in one place.** `changeorder.CheckAgent` holds
    the rule of ADR 190 decision 14: an actor with an agent reference can
    write its own draft (`PutDraft`, `GetDraft` and `DeleteDraft`), submit
    a review of the verdict `comment` with comment threads, replies and
    blocking threads (as an agent of an eligible approver), and write a
    reply, and nothing else. The draft holds the threads before the submit,
    so the agent needs the draft writes to submit blocking threads. An agent
    still records no verdict. A table-driven test checks each method and
    each verdict:

    | Method | Agent |
    | --- | --- |
    | `PutDraft`, `GetDraft`, `DeleteDraft` on its own draft | allowed |
    | `GetDraft` or `Submit` of the draft of its person (a draft key with an empty agent reference) | `ErrAgentDenied` |
    | `Submit` with the verdict `comment` | allowed, also with blocking threads for an agent of an eligible approver |
    | `Submit` with `approve`, `request-changes` or `reject-and-close` | `ErrAgentDenied` |
    | `Reply` | allowed |
    | `Create`, `Attach`, `Resolve`, `Reopen`, `StartApply`, `Close` | `ErrAgentDenied` |
11. **Conformance suites first.**
    - `pkg/changeorder/changeordertest.RunStore` gets a reference adapter in
      memory in the tests of the package, so each case runs before any SQL
      adapter. The cases cover each row of the table of `Next`, each error
      of decision 1, the three fences of decision 6, the reset of signatures
      at an attach, the matching of ADR 192 decision 6, the follow and
      outdated rules of ADR 193, the draft rules, a draft write and a
      `comment` submit with blocking threads by an agent, two submits at the
      same time, a second `approve` by the same principal on the same
      revision, an attach with a base that is the head, a change of the
      access version of one signer during a submit, a reopen of a blocking
      thread by an author, by an agent and by the principal that opened it
      and lost `changeorder:approve`, an approve with an unreadable summary
      and no policy, a resolve by an author, a resolve by an agent, a
      resolve by the principal that opened a thread and lost
      `changeorder:approve`, a dismissal with no reason, an `ObserveJob`
      call with an old job version, an apply start that failed for one unit,
      a revoke between the check and the commit, the cases of `Approved`
      (ADR 192 decision 4), and a backup that holds no draft. Each SQL
      adapter runs it.
    - `changeordertest.RunPlanReader` takes a reader and a set of fixture
      plans with their expected summaries. Each tool package runs it with
      fixtures of each supported version of its CLI. The fixtures include
      a secret value, a replace, a delete, two identical updates and a
      plan that the reader cannot parse.
    - `changeordertest.RunCauseDiffs` takes an adapter and a fake forge,
      and covers a truncated diff, an unknown commit and a timeout.
    - The reference adapter implements `history.Redactor` for comments and
      runs `historytest.RunRedactor` with the new table
      `historytest.CommentSituations()` (ADR 198 decision 3).
    - `pkg/job/jobtest` gets the cases of ADR 190: an apply job with
      `job.Spec.Plan`, its stale dispatch, the reader rule of the part
      `plan-apply`, no approval gate for a job with a change order origin,
      the start of `changeorder.sync`, the `job.start` cases of ADR 190
      decision 6, the origin cases of ADR 190 decision 15, and the refusal of a comment and a resolve on a job with
      a change order origin (ADR 190 decision 16).

## Consequences

- The change order package has five ports and no driver code. The SQL
  adapters, the forge adapter and the plan readers come later, each with
  its suite.
- A tool package gets one more port to implement, `PlanReader`. It is the
  only place that reads the plan format of the tool.
- `pkg/changeorder` does not import `pkg/job`. The wiring maps the errors
  and the types, and its tests check each map.
