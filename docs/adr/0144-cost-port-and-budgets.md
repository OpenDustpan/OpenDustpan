# 144. A pluggable cost port: estimates, actual spend and budgets

Status: accepted · 2026-10-18 · answers part of RFC 45 and RFC 67

## Context

[RFC 45](../rfc/0045-cost-estimation.md) asks which estimator gives a cost
estimate on each plan, and whether it is a built-in or a recommended step.
[RFC 67](../rfc/0067-spend-and-cost-tool-integration.md) asks how
OpenDustpan works with spend and cost tools, and lists four options: no
integration, a recommended job step, a built-in estimator, or generic
extension points.

On 17 October 2026 the project owner decided:

- Cost operations are pluggable: a port for the cost estimates of plans,
  for each resource and for each change, and a port for actual spend.
  Adapters come later.
- The work order and the Catalog show the estimate, for example "+EUR 31.20
  a month".
- Budgets exist for each namespace, with warnings. A budget never blocks
  silently. Only a policy can block.

A plan of the job model (ADR 102) is a stored, masked, machine-readable
document. A policy decides at the start of a job and can gate a phase (ADR
103 decision 1).

## Decision

### Money

1. **An amount in one currency.** `cost.Money` holds an ISO 4217 currency
   code and an amount in millionths of one unit. A price for each hour of a
   small resource keeps its digits. The server never converts between
   currencies. A sum of two currencies fails with `ErrCurrencyMismatch`.
   A cost, a spend and a budget amount is 0 or more, and at most
   `cost.MaxMicros`, 10^15 micros (one billion units). `Money.ValidateCost`
   refuses other amounts with `ErrInvalid`. `Add` and `Sub` refuse a result
   outside the int64 range with `ErrInvalid`, so no sum and no delta wraps
   around. Only a delta can be below 0.
2. **One period: a month.** Each estimate and each budget is a cost for one
   month. Each period is a calendar month in UTC (`cost.Period`). Plain text
   shows "+EUR 31.20 a month". `Money.String` rounds to the minor unit of
   the currency in ISO 4217 (`cost.MinorUnits`): "JPY 3100", "BHD 1.250",
   and 2 decimals for a currency that its table does not name. The console
   formats the amount for the locale of the person.

### Estimates

3. **The estimate of a plan.** `cost.Estimate` has a subject, a source, a
   currency, the cost of each resource before and after the change, and the
   sums `Before` and `After` of the priced resources. `Delta` is `After`
   minus `Before`, and returns an error instead of a value that wraps
   around. A resource that the estimator cannot price is in the
   estimate as unpriced, with a note. The work order shows the count of
   unpriced resources next to the delta, because the delta does not count
   them. `cost.Summed` computes the sums, and `Validate` refuses an
   estimate whose sums do not match its resources, or whose sum `Before`
   or `After` is above `cost.MaxMicros`. So a delta is from minus to plus
   `cost.MaxMicros`. `Estimate.Namespace` is
   the namespace of the row: the namespace of the unit of a plan, or of
   the blueprint. A blueprint of the installation has its estimate in the
   server scope `@server`.
4. **Two subjects.** The subject is the plan of a phase of a job, or a
   version of a blueprint in the Catalog. A unit made from a blueprint gets
   the estimate of its own plan. The Catalog shows the estimate of the last
   plan of the blueprint with its default inputs, with the date of that
   plan.
5. **Two ways in, one store.**
   - **Pull.** The `cost.Estimator` port reads the masked plan of a phase
     and answers the cost of each resource. The server calls it in a
     durable operation after the plan phase stores its plan. A slow price
     source never holds a job. The input is the masked plan, the same
     document that a person with `job:read-plans` reads, so an estimator
     never gets a secret value.
   - **Push.** A team that runs an estimator in a custom step of the role
     `tool` in a runner job sends the result through
     `PUT /api/jobs/{id}/phases/{n}/cost-estimate`, with the source `api`.
     The step authenticates with the job token, which only a step of the
     role `tool` gets (ADR 133 decision 12). A hook step and a pipeline
     outside the runners cannot push. Their way in is an adapter of the
     `Estimator` port. The route accepts a push only for the phase `n`
     that runs now. A push for a phase that waits or that ended returns
     409, so a step cannot change the estimate of a plan that a person
     already read or approved.

   Both write through `cost.EstimateStore.PutEstimate`, with the fence of
   the namespace of the estimate. A subject keeps one estimate of each
   origin (`cost.Origin`): the pushed estimate and the estimate of the
   `Estimator` port. An estimate replaces only the estimate of its own
   origin, so a push never replaces the estimate of the port silently, and
   the port never replaces a push. A second push writes a second audit
   entry. The work order shows both estimates when both exist, each with
   its source. A budget counts the change once, with the larger delta
   (decision 11). This answers the option "generic extension points" of
   RFC 67 for estimates: the server keeps the shape, and any tool can fill
   it.
6. **No estimator in the binary now.** The binary has no built-in
   estimator. An adapter of the `Estimator` port is a separate package, and
   a dependency needs the approval of the owner. RFC 45 keeps the choice of
   the first adapter open.

### Actual spend

7. **The spend port.** `cost.SpendSource` answers the spend of the units of
   a namespace for a month: the actual spend so far and, when the tool has
   one, its forecast for the month. Each line names a unit, or no unit for
   spend that the tool gives to the namespace but to no unit.
8. **How spend finds a unit.** Each unit has a stable ID (ADR 19). The query
   gives the reference and the ID of each unit. An adapter finds the
   resources of a unit by a tag that holds the ID, or by its own mapping.
   The server writes no tag on any resource. A team adds the tag in its
   code, and the docs give the tag key.
9. **A durable schedule reads spend.** Once an hour for each namespace with
   a budget, the server reads the spend, evaluates each budget, and saves
   the status with the fence of the namespace. A tool that does not answer
   keeps the last status, with its time.

### Budgets

10. **A budget is a limit for each month.** `cost.Budget` belongs to a
    namespace, or to one collection of it. It has an amount for each month
    and 1 to 5 thresholds in percent, 80 and 100 by default.
11. **The status of a budget.** `cost.Evaluate` gives the status for a
    month:
    - `Actual`: the spend so far,
    - `Forecast`: the larger of `Actual` and the forecasts of the tool,
    - `Pending`: the sum of the deltas of the plans of the jobs that wait
      or run: for each job, the estimate of its highest phase with an
      estimate, and of two estimates of that phase the larger delta,
    - `Projected`: `Forecast` plus `Pending`, and its percent of the
      amount.

    The level is `ok`, `warning` at or above a threshold, or `exceeded` at
    or above 100 percent. A pending delta is the cost of a whole month, so a
    warning comes early, never late.

    A spend line or a pending estimate in another currency than the budget
    does not count, and `Status.Skipped` counts it. A spend line with a
    negative spend or forecast, for example a credit, does not count
    either. `Status.Skipped` counts it, and `Status.SkippedNegative` counts
    it as its own reason. The console shows each count next to the status.
    So one estimate in another currency or one credit never stops the
    warnings of a budget. A spend amount above the cap fails the evaluation
    with `ErrInvalid`, because it is an error of the adapter. A percent that
    does not fit an int64 stays at the int64 maximum.
12. **A warning, once.** The status that the store keeps holds the
    thresholds that warned in its month (`Status.Warned`).
    `cost.MarkWarned` gives the thresholds that the new status crossed and
    that no status of the same month warned about, and adds them to
    `Warned`. The server first appends the notification `budget.threshold`
    for each of them to the owners of the namespace (ADR 145), with the
    dedupe key `cost.ThresholdKey`: the budget, the month and the
    threshold. Then it saves the status with `SaveStatus`. When the save
    fails, or the server stops between the two writes, the next evaluation
    appends the same notification again, and the outbox skips it because
    the key is the same. The outbox keeps a notification for 32 days, more
    than one month, so the key dedupes for the whole month. So the server
    loses no warning and sends none twice. Each threshold warns once a month,
    also when the projection goes from 85 to 70 and to 85 percent again. A
    new month starts with no warned threshold.
13. **A budget never blocks.** No budget refuses a start, a dispatch or a
    state write. The work order shows the impact of the change:
    `cost.ImpactOf` gives the percent before and after the delta, "budget
    after this change: 92% of EUR 4,000.00".
14. **Only a policy can block.** A block on cost is a rule of a policy, so
    a block is never silent: the denial or the gate names the policy and
    its reason. The decision at the start of a job (ADR 103 decision 1)
    comes before the plan, so it cannot read the estimate of that plan.
    This ADR does not decide how a policy gates a later phase on the
    estimate. It asks the job model for it, under "Requests to other
    designs".

### API, actions and audit

15. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/jobs/{id}/phases/{n}/cost-estimate` | `job:read` | unit |
    | `GET /api/jobs/{id}/phases/{n}/cost-estimate/resources` | `job:read-plans` | unit |
    | `PUT /api/jobs/{id}/phases/{n}/cost-estimate` | `job:write-cost` | unit |
    | `GET /api/catalog/{blueprint}/versions/{v}/cost-estimate` | the read action of the Catalog | blueprint |
    | `GET /api/namespaces/{ns}/budgets[/{id}]` | `namespace:read-cost` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/budgets[/{id}]` | `namespace:manage-budgets` | namespace |
    | `GET /api/namespaces/{ns}/spend?month=` | `namespace:read-cost` | namespace |

    A by-ID route answers 404 when the namespace of the row is not
    `{ns}`: a budget, and its status, of another namespace is not found,
    also for a person who holds the action in both namespaces. Each read
    by ID of the store takes the scope of the row: `GetEstimate` takes the
    namespace of the unit of the job, or the server scope for a blueprint
    of the installation, and `LastStatus` takes the namespace of the
    budget. Each returns `ErrNotFound` for a row of another scope.

    The first route answers the sums, the delta, the currency, the source
    and the unpriced count, which are metadata of the plan, as `job:read`
    allows (ADR 102). It answers one estimate of each origin that exists. The cost of each resource names the keys and the
    types of the resources of the plan. That is content of the plan, so
    the second route needs `job:read-plans`.

    Spend can be sensitive for a business, so it has its own read action.
    The built-in roles `viewer` and above get `namespace:read-cost`. The role
    `admin` gets `namespace:manage-budgets`.

    Only the job token (ADR 100 decision 20) holds `job:write-cost`, on the
    unit of its job, and only for the phases of its own job: a push for
    another job ID than the job-ID claim fails with `identity.ErrDenied`. No
    built-in role gives `job:write-cost`, so a session, a user token and an
    agent that acts for a person never hold it.
16. **Audit entries.** A pushed estimate, and a create, a change and a
    delete of a budget write one entry each, of the kind `change`, in the
    scope of the namespace. An estimate of the `Estimator` port, a spend
    read and a budget evaluation write no entry: they are not actions of a
    person.
17. **Fences.** Each write of `cost.EstimateStore` and `cost.BudgetStore`
    carries the fence of the namespace of its row, also `SaveStatus`. The
    store checks it in the transaction. A zero fence is valid only for an
    estimate in the server scope, and a fence for such an estimate is
    refused. A create or an estimate with a fence of another namespace
    returns `ErrInvalid`. A write of a stored budget or of its status with
    a fence of another namespace returns `ErrNotFound`, so a caller cannot
    learn of a budget in a namespace that it does not hold. A stale fence
    returns `namespace.ErrFenced`. A write with an audit entry whose
    namespace is not the namespace of its row returns `ErrInvalid` and
    changes nothing, so a write never appends to the audit log of another
    namespace.
18. **Ports with conformance first.** `cost.EstimateStore` and
    `cost.BudgetStore` get the suite `pkg/cost/costtest`, with a reference
    adapter in memory. The suite runs a zero, a cross-namespace and a stale
    fence on each write. `cost.Estimator` and `cost.SpendSource` get their
    suites with their first adapters.

## Requests to other designs

- **Job model (ADR 103).** A policy can block on cost only when the job
  model lets it read the estimate. This ADR asks the job model to decide:
  - whether the decision input at start gets the status of each budget of
    the namespace (the percent before the change).
  - whether the job model evaluates the policy again for the gate of the
    next phase when the estimate of a plan arrives, with the delta, the
    unpriced count, the currency and the percent of each budget after the
    change in the input. ADR 103 now decides the gates once, at start.
  - what a gate on cost does when no estimate arrives. A proposal: after
    15 minutes the gate stays closed with the reason "no estimate", and a
    person with `job:approve` can approve it.
- **Job token (ADR 100 decision 20).** Decision 20 says that the job-ID
  claim adds no action. Decision 15 of this ADR needs the job token to hold
  `job:write-cost` on the unit of its job, and the route to compare the
  job ID with the claim.
- **Notifications (ADR 145).** `BudgetStore.SaveStatus` takes no
  notification, so that the cost package does not depend on the
  notification package. Decision 12 appends the notifications of
  `budget.threshold` before the save, with a dedupe key, so the two writes
  need no shared transaction. This needs the outbox to keep a dedupe key
  for at least 32 days.
- **Context read API (ADR 161).** Its open question 3 asks whether cost is
  a section. This ADR gives the data: the last estimate of a unit and the
  status of each budget.
- **Blueprints and the Catalog.** The Catalog item of a blueprint version
  shows its estimate (decision 4). The blueprint design names the read
  action of the Catalog.

## Consequences

- A team can bring any estimator: a step of the role `tool` in a runner
  job, or an adapter. The server keeps one shape and one store. A pipeline
  outside the runners has no job token, so it cannot push an estimate.
- A spend tool can match spend to units only through a tag or its own
  mapping. A unit without the tag shows no spend.
- No currency conversion: a namespace with budgets in two currencies needs
  two budgets, and each budget counts only the spend in its currency.
- `GLOSSARY.md` needs the terms Cost estimate, Spend, Budget and Threshold.

## Open questions

- Which estimator gets the first adapter? RFC 45 keeps this open.
- Which spend tool gets the first adapter? RFC 67 keeps this open.
- What is the tag key of decision 8, and does the server offer it in the
  reserved output of a unit or as a variable of a runner job?
- Does a namespace need a currency of its own for estimates, or does each
  estimator choose?
- Should the projection prorate a pending delta to the days left in the
  month? This ADR takes the whole month, so a warning comes early.
