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
2. **One period: a month.** Each estimate and each budget is a cost for one
   month. Each period is a calendar month in UTC (`cost.Period`). Plain text
   shows "+EUR 31.20 a month". The console formats the amount for the
   locale of the person.

### Estimates

3. **The estimate of a plan.** `cost.Estimate` has a subject, a source, a
   currency, the cost of each resource before and after the change, and the
   sums `Before` and `After` of the priced resources. `Delta` is `After`
   minus `Before`. A resource that the estimator cannot price is in the
   estimate as unpriced, with a note. The work order shows the count of
   unpriced resources next to the delta, because the delta does not count
   them. `cost.Summed` computes the sums, and `Validate` refuses an
   estimate whose sums do not match its resources.
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
   - **Push.** A team that runs an estimator in its own pipeline, or in a
     custom step of a runner job, sends the result through
     `PUT /api/jobs/{id}/phases/{n}/cost-estimate`, with the source `api`.

   Both write through `cost.EstimateStore.PutEstimate`. A second estimate of
   one subject replaces the first. This answers the option "generic
   extension points" of RFC 67 for estimates: the server keeps the shape,
   and any tool can fill it.
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
   the status. A tool that does not answer keeps the last status, with its
   time.

### Budgets

10. **A budget is a limit for each month.** `cost.Budget` belongs to a
    namespace, or to one collection of it. It has an amount for each month
    and 1 to 5 thresholds in percent, 80 and 100 by default.
11. **The status of a budget.** `cost.Evaluate` gives the status for a
    month:
    - `Actual`: the spend so far,
    - `Forecast`: the larger of `Actual` and the forecasts of the tool,
    - `Pending`: the sum of the deltas of the plans of the jobs that wait
      or run,
    - `Projected`: `Forecast` plus `Pending`, and its percent of the
      amount.

    The level is `ok`, `warning` at or above a threshold, or `exceeded` at
    or above 100 percent. A pending delta is the cost of a whole month, so a
    warning comes early, never late.
12. **A warning, once.** When a status crosses a threshold that the last
    status of the same month did not cross (`cost.NewlyCrossed`), the
    server sends the event `budget.threshold` to the owners of the
    namespace (ADR 145). Each threshold warns once a month.
13. **A budget never blocks.** No budget refuses a start, a dispatch or a
    state write. The work order shows the impact of the change:
    `cost.ImpactOf` gives the percent before and after the delta, "budget
    after this change: 92% of EUR 4,000.00".
14. **A policy can block.** The input of the decision at the start of a job
    (ADR 103 decision 1) gets the cost: the delta, the unpriced count, the
    currency, and for each budget of the namespace the percent before and
    after. A policy can deny or gate a start on it. The policy owns that
    rule, so a block is never silent: the denial names the policy and its
    reason.

    The estimate of a plan comes after the plan phase. A policy that reads
    cost gates the next phase. The job model re-evaluates the gate of a
    phase when its estimate arrives. An estimate that does not arrive in 15
    minutes leaves the gate closed with the reason "no estimate", and a
    person with `job:approve` can approve it.

### API, actions and audit

15. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/jobs/{id}/phases/{n}/cost-estimate` | `job:read` | unit |
    | `PUT /api/jobs/{id}/phases/{n}/cost-estimate` | `job:write-cost` | unit |
    | `GET /api/catalog/{blueprint}/versions/{v}/cost-estimate` | the read action of the Catalog | blueprint |
    | `GET /api/namespaces/{ns}/budgets[/{id}]` | `namespace:read-cost` | namespace |
    | `POST`, `PUT`, `DELETE /api/namespaces/{ns}/budgets[/{id}]` | `namespace:manage-budgets` | namespace |
    | `GET /api/namespaces/{ns}/spend?month=` | `namespace:read-cost` | namespace |

    Spend can be sensitive for a business, so it has its own read action.
    The built-in roles `viewer` and above get `namespace:read-cost`. The role
    `admin` gets `namespace:manage-budgets`. The runner token of a job gets
    `job:write-cost` on its unit.
16. **Audit entries.** A pushed estimate, and a create, a change and a
    delete of a budget write one entry each, of the kind `change`, in the
    scope of the namespace. An estimate of the `Estimator` port, a spend
    read and a budget evaluation write no entry: they are not actions of a
    person.
17. **Ports with conformance first.** `cost.EstimateStore` and
    `cost.BudgetStore` get the suite `pkg/cost/costtest`, with a reference
    adapter in memory. `cost.Estimator` and `cost.SpendSource` get their
    suites with their first adapters.

## Requests to other designs

- **Job model (ADR 103).** The decision input gets the cost of decision
  14. A gate that a policy makes on cost waits for the estimate, as
  decision 14 says.
- **Blueprints and the Catalog.** The Catalog item of a blueprint version
  shows its estimate (decision 4). The blueprint design names the read
  action of the Catalog.

## Consequences

- A team can bring any estimator: a pipeline step, a runner step, or an
  adapter. The server keeps one shape and one store.
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
