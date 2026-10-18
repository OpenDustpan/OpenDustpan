# 196. Change order ports, errors, fences and conformance suites

Status: accepted · 2026-10-18 · part of ADR 190

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
   | `ErrInvalid` | A request with an empty or bad field, a unit of another namespace, an anchor that names no change of the plan. |
   | `ErrNotFound` | An unknown change order, revision, thread or draft, or a caller without `changeorder:read` on each unit (decision 9). |
   | `ErrConflict` | A write on a stale read of the change order. |
   | `ErrTransition` | An event that `Next` does not allow in the state. |
   | `ErrFinished` | A write on a terminal change order. |
   | `ErrApplying` | An attach, a close or a reject while apply jobs run. |
   | `ErrLimit` | A write above a limit. The error names the limit. |
   | `ErrPlanChanged` | A digest that is not the digest of the current revision. |
   | `ErrPlanStale` | An apply with a stale unit plan. The error names each unit. |
   | `ErrNotReady` | An approve or an apply before the revision is `ready`. |
   | `ErrNotApproved` | An apply of a change order that is not `approved`. |
   | `ErrChangesRequested` | An approve or an apply while a blocking thread is open. The error names the threads. |
   | `ErrSelfApproval` | An approve, a request for changes or a reject by an author. |
   | `ErrNotEligible` | A verdict by a principal that is not an eligible approver. |
   | `ErrAuthorThread` | A blocking thread, or a reopen of one, by an author. |
   | `ErrSelfResolve` | A resolve of a blocking thread by an author. |
   | `ErrReasonRequired` | A request for changes, a reject, a close, a dismissal or a reopen with no reason. |
   | `ErrAgentDenied` | A write of an agent other than a comment, a reply or a blocking thread (ADR 190 decision 14). |
   | `ErrPolicyDenied` | An approve of a revision that a policy denied (ADR 192 decision 4, ADR 194 decision 8). |
   | `ErrPolicyFailed` | An approve while the policy of the revision failed and no retry succeeded. |
   | `ErrToolUnsupported` | A `PlanReader` that does not know the tool of a unit. |
   | `ErrPlanFormat` | A readable plan that the `PlanReader` cannot parse. |
   | `ErrForgeUnavailable` | A forge that does not answer for a cause diff. |

   The errors of the `job` package do not cross into this package. The
   wiring maps them, and a test of the wiring checks each row:

   | `job` error at a plan or apply start | `changeorder` result |
   | --- | --- |
   | `job.ErrPolicyDenied`, `job.ErrFrozen` with no break-glass, `job.ErrLimit`, `job.ErrInvalid` | The unit plan or the apply of that unit is `failed`, with the reason. |
   | `job.ErrUnitBusy` | None. The job is queued, as the job model says (ADR 100 decision 10). |
   | `job.ErrPlanStale` at the dispatch of an apply job | The apply of that unit is `failed` with the reason `plan-stale` (ADR 190 decision 9). |
   | `identity.ErrDenied` | The operation fails, and the change order records the missing action. |
   | another error | The durable operation retries with its backoff. |

2. **The store port.** `changeorder.Store` persists change orders. Each
   method that writes takes a `namespace.Fence` and the audit entries of
   the write, and appends them in the transaction of the write. Each one
   returns `namespace.ErrFenced` for a stale fence, and changes nothing on
   an error.

   ```go
   type Store interface {
       // Create stores o in planning with revision 1, and starts the
       // operation changeorder.plan. ErrLimit, ErrInvalid.
       Create(ctx context.Context, o Order, f namespace.Fence, e []audit.Entry) (Order, error)
       Get(ctx context.Context, id ID) (Order, error) // ErrNotFound
       List(ctx context.Context, q Filter) (Page, error)
       // Attach adds revision r, voids the signatures of each earlier
       // revision, and starts changeorder.plan. ErrTransition,
       // ErrApplying, ErrFinished, ErrConflict, ErrLimit.
       Attach(ctx context.Context, id ID, r Revision, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       // ObserveJob applies the state of one job of the change order.
       // A job version that the store saw changes nothing.
       ObserveJob(ctx context.Context, j JobState, f namespace.Fence) (Order, error)
       // SetAnalysis stores the metadata of the summary of one unit
       // plan, and marks the revision ready when each analysis ended.
       SetAnalysis(ctx context.Context, id ID, rev int, s SummaryMeta, f namespace.Fence) (Order, error)
       // SetRequirements stores the policy decisions of a ready revision.
       SetRequirements(ctx context.Context, id ID, rev int, d Decisions, f namespace.Fence, e []audit.Entry) (Order, error)
       // Submit writes review r with its threads, its signature and the
       // change of the state, and deletes the draft. It checks the rules
       // of Eligible, Match, Follow and Next in its transaction.
       Submit(ctx context.Context, id ID, r Review, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       Reply(ctx context.Context, id ID, t ThreadID, c Comment, f namespace.Fence, e []audit.Entry) (Order, error)
       Resolve(ctx context.Context, id ID, t ThreadID, r Resolution, f namespace.Fence, e []audit.Entry) (Order, error)
       Reopen(ctx context.Context, id ID, t ThreadID, r Resolution, f namespace.Fence, e []audit.Entry) (Order, error)
       // StartApply moves an approved change order to applying and
       // starts changeorder.apply. ErrNotApproved, ErrPlanChanged,
       // ErrChangesRequested.
       StartApply(ctx context.Context, id ID, a ApplyRequest, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       Close(ctx context.Context, id ID, c Closure, version int64, f namespace.Fence, e []audit.Entry) (Order, error)
       // Expire ends an idle change order. ErrTransition when it saw a
       // write after now minus DefaultIdleTimeout.
       Expire(ctx context.Context, id ID, now time.Time, f namespace.Fence, e []audit.Entry) (Order, error)
       // Drafts. A draft write appends no audit entry.
       PutDraft(ctx context.Context, id ID, d Draft, f namespace.Fence) error
       GetDraft(ctx context.Context, id ID, reviewer string) (Draft, error) // ErrNotFound
       DeleteDraft(ctx context.Context, id ID, reviewer string, f namespace.Fence) error
   }
   ```

   `Submit`, `Resolve`, `Reopen` and `StartApply` read the threads, the
   signatures and the requirements in the same transaction as their write.
   So two reviewers that write at the same time never make an approved
   change order with an open blocking thread.
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
   durable operations of decision 7 are control tables (ADR 72 decision
   5).
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
    write a comment thread, a reply and a blocking thread (as an agent of
    an eligible approver), and nothing else. A table-driven test checks
    each method.
11. **Conformance suites first.**
    - `pkg/changeorder/changeordertest.RunStore` gets a reference adapter
      in memory in the tests of the package, so each case runs before any
      SQL adapter. The cases cover each row of the table of `Next`, each
      error of decision 1, the three fences of decision 6, the reset of
      signatures at an attach, the matching of ADR 192 decision 6, the
      follow and outdated rules of ADR 193, the draft rules, two submits
      at the same time, a resolve by an author, a resolve by an agent, a
      dismissal with no reason, and an `ObserveJob` call with an old job version.
      Each SQL adapter runs it.
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
      and the start of `changeorder.sync`.

## Consequences

- The change order package has five ports and no driver code. The SQL
  adapters, the forge adapter and the plan readers come later, each with
  its suite.
- A tool package gets one more port to implement, `PlanReader`. It is the
  only place that reads the plan format of the tool.
- `pkg/changeorder` does not import `pkg/job`. The wiring maps the errors
  and the types, and its tests check each map.
