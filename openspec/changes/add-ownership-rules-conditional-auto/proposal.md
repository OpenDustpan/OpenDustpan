Issue: #902.

## Why

**Persona: namespace administrator.** A namespace administrator names owners for units and paths so a change needs owner approval, and allows unattended apply only when conditions on the source of the operation hold.

The user path this change must make work:

1. **API.** Create ownership rules and named path sets for units and paths through the API.
2. **CLI.** Start a plan from a push, schedule or the API and read the decision record for requirements and autoApply.
3. **Console.** Approve as an owner when required, or let a schedule-started plan inside unless apply with no approval.
4. **Docs page.** Follow the docs page to set owners and the unless condition.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save of an ownership rule with a bad path pattern or bad condition fails. | Save fails with 422 gate.invalid and names the bad field. List the stored rules to compare. | `make check` runs the conformance suite that covers rule validation. |
| Apply without the owner approval waits in approval wait. | The job stays in approval wait. Read the waiting change order or job to see the owners and the matched rule. | `make e2e` runs the runner path that shows the approval wait. |
| A warn on the plan stops unattended apply. | The change stays in approval wait and the record shows autoApply false with the warn. Remove the warn or get owner approval. | `make e2e` runs the warn path that shows autoApply false. |

Evidence for the claims above:

- Ownership rules, matching on units and named path sets, unless over change.planned and autoApply in the decision record are the scope of this item. Proof: Issue #902: ownership rule objects and routes (ADR 225, ADR 230); matching on units and named path sets; the `unless` Cedar body over the facts of `change.planned`; the `autoApply` result in the decision record.
- The gate engine already names the change.planned decision point and stores autoApply in the record. Proof: pkg/gate/gate.go:176: PointChangePlanned DecisionPoint = "change.planned"; pkg/gate/record.go:173: AutoApply bool `json:"autoApply"`

## What Changes

- Ownership match at change.planned that adds one owner requirement per matched enforce rule, using the route shapes of the depended changes
- Unless evaluation on the matched ownership rule over change.planned facts for conditional auto-apply, with warn and unless errors forcing autoApply false
- Decision-record fields for the rule ID, the matched path set, the unless result and autoApply
- Docs page for ownership rules and conditional auto-apply

Out of scope:

- IaC provider resources for ownership rules
- Review modes, mirrored approvals and plan comments
- Schedule, push and follows trigger setup, which #901 provides
- Ownership-rule and path-set route shapes, which rest-routes-ownership-rules-create and add-named-path-sets-paths provide
- Multi-requirement counting semantics, which several-approval-requirements-one-change provides

Depends on:

- #899
- #901
- rest-routes-ownership-rules-create
- add-named-path-sets-paths
- several-approval-requirements-one-change
- `rest-routes-ownership-rules-create` (order of the milestone review)
- `add-named-path-sets-paths` (order of the milestone review)
- `several-approval-requirements-one-change` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A broad ownership pattern adds surprise approvals; the record names the rule and the matched path set.
- Auto-apply stays invisible unless the record shows why; the record shows the unless result and any warn.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships its user docs page in the same pull request.
- Conflict: none.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 2, 4, 9).

- Decisions that apply: 2 (current), 4 (current), 9 (current).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "An ownership rule needs no gate."
- Quote: "A default has a count, an optional `freshAuthSeconds`, optional `approvers` and an optional `unless` Cedar body over the facts of `change.planned`."
- Quote: "An ownership rule is stored in the server and configured through the API."
- Effect: Ownership rules in enforce mode add requirements with no gate. Conditional auto-apply follows the unless model of the approval requirements change; the unless body on the advanced ownership rule needs an owner decision because decision 9 lists no unless field.
- Conflict: owner_blocks.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 5 (current), 6 (current).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "Gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting are stored in the server."
- Quote: "A scope holds at most 256 gates, 64 access limits, 64 approval rules, 256 ownership rules and 64 path sets."
- Effect: Ownership rules and path sets are stored in the server with read and manage actions, limits, errors and audit entries defined by the gate configuration routes change.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The spec-ready gate fails. The routes GET .../ownership-rules and GET /api/dustpan/v1/decision-records/{record-id} are not in the API description or the route table, so the change cannot be built as written until they are named as new routes or replaced by existing ones. Resolution: answered: This change no longer adds routes; it uses the routes of rest-routes-ownership-rules-create and add-named-path-sets-paths, and error commands use make checks.
- R1.2 (missing scenario): The spec has no requirement for ownership rule and path-set objects, their routes, validation (gate.invalid on a bad path or unless body), limits (256 rules, 64 path sets, 4 KiB condition per ADR 230 decision 6) or audit entries (ADR 230 decision 5). The What Changes list and task 1.3 cover them, but the spec does not. Resolution: answered: Requirements now cover validation, limits, audit entries, evaluate mode, counting, fail-closed unless errors and record path sets.
- R1.3 (adr conflict): The change puts an unless condition on the ownership rule. ADR 225 decision 4 places the optional unless Cedar body on an approval default. The ownership rule shape of ADR 225 decision 9 has no unless field. No ADR gives unless to ownership rules, so the change needs an RFC or an owner decision first. Resolution: open question: Covered by the owner open question on the ownership-rule unless field.
- R1.4 (open question): The design says a true unless removes the approval need for auto-apply. It does not say whether this removes only the ownership requirement or also the requirements from gates and approval defaults. ADR 225 decision 2 names three sources of requirements, and no ADR answers this. Resolution: open question: Covered by the owner open question on the scope of a true unless.
- R1.5 (open question): The unless body reads an operation source fact of change.planned, but the change never names that fact, its values or how a push, schedule or API start sets it. The schedule scenario cannot be built or tested without this. Resolution: open question: Covered by the PM open question that names the operationSource fact and its push, schedule and api values.
- R1.6 (missing scenario): No scenario covers an unless evaluation error, an unknown fact or a Cedar runtime failure. The design risk says such a failure can fail closed to no auto-apply, but no requirement or test states it. Resolution: answered: The unless-error requirement fails closed to autoApply false, with a scenario and a conformance test.
- R1.7 (missing scenario): The mode 'evaluate' of an ownership rule (ADR 225 decision 9 example) is not covered. The spec says only enforce rules add a requirement, and does not say what evaluate mode records. Resolution: answered: The evaluate-mode requirement records the match with no requirement.
- R1.8 (missing scenario): A plan that matches two enforce rules, or two path sets of one rule, is not covered. ADR 225 decision 3 says one rule gives one requirement, and the spec does not state how matches are counted. Resolution: answered: The counting requirement adds one requirement per matched enforce rule, with a two-rule scenario.
- R1.9 (wrong claim): The error table says a plan waiting for approval returns 403 gate.not-approver, and a warn returns 409. ADR 230 decision 4 maps gate.not-approver to a denial, not to a wait. The table does not match the error model. Resolution: answered: Error answers now keep a wait as a wait and reserve denial codes for denials.
- R1.10 (failure mode): A rule or path set saved after the plan but before apply is not covered. The spec does not say whether the requirement is re-evaluated at apply or the decision record is kept. Resolution: open question: Covered by the PM open question on single evaluation at change.planned.
- R1.11 (missing scenario): The design says the decision record names the matched path set, but the record requirement names only the rule ID and the unless result. Resolution: answered: Record requirement writes the rule ID, the matched path set and the unless result.
- R1.12 (failure mode): Hostile or broad path globs and unless bodies at the limits are not tested for cost or matching time at change.planned. Tasks name no limit or timing test. Resolution: answered: The limit task checks 256 rules, 64 path sets, 4 KiB conditions and the matching budget.
- R2.1 (adr conflict): ADR 225 decision 4 places the optional unless Cedar body on approval defaults and decision 9 gives ownership rules no unless field, yet the change exempts auto-apply through an unless on the covering default while proposal and issue 902 speak of an unless on the wider rule; the drafter's own open question 1 is marked provisional, so the spec-ready gate does not hold. Quote: 'A default has a count, an optional freshAuthSeconds, optional approvers and an optional unless Cedar body over the facts of change.planned.' Resolution: open question: The exemption is on the ownership rule per owner decision 24.3, pending the owner open question for the new field.
- R2.2 (owner decision conflict): Owner decision 24.3 (13 Nov scope of issue 902) says unattended apply is gated by approval rules and only an advanced ownership rule may allow automatic apply by source conditions; the change instead lets an approval default's unless mark autoApply true and excuses the ownership requirement, which moves the exemption to a different object than the owner decided. Resolution: open question: Same as R2.1: the exemption lives on the ownership rule, pending the owner decision in open questions.
- R2.3 (open question): The operationSource fact of change.planned (push, schedule, api) is named only as a provisional decision; the schedule scenario and task 2.1 cannot be built or tested until the fact name, its values and its population at plan start are fixed in the fact schema. Resolution: open question: Kept as a PM open question that fixes the operation-source fact name and its push, schedule and api values.
- R2.4 (open question): Whether a true unless excuses only the ownership requirement or every requirement (gate and approval-default sources of ADR 225 decision 2) is decided only provisionally; the spec's autoApply scenario does not state which requirements are excused, so it cannot be implemented as written. Resolution: open question: Spec states that a true unless excuses only that ownership requirement, pending owner confirmation in open questions.
- R2.5 (failure mode): Single evaluation at change.planned versus re-evaluation at apply is left provisional; a rule or path set saved between plan and apply can silently change the approval set, and the design does not require the record to be invalidated. Resolution: open question: Design states single evaluation at change.planned with the record reused at apply, pending the PM open question.
- R2.6 (missing scenario): No scenario covers a plan that touches the path set of an ownership rule when no covering approval default exists, so the unless step has no source and the autoApply value is unspecified. Resolution: answered: The path-match requirement adds one owner approval for a rule without unless, and the conditional auto-apply requirement adds autoApply true for a rule with a met unless.
- R2.7 (missing scenario): No scenario covers a plan that matches an enforce rule and whose owners are the author only, or an author who is the sole eligible approver under ADR 225 decision 7 (authorMayApprove), so the requirement may be unsatisfiable. Resolution: open question: Covered by the author-eligibility requirement and the PM open question for the wait against refuse choice.
- R2.8 (wrong claim): The error table row for the apply waiting for owner approval names the record route, but the ADR 230 decision 3 route for decision records is not listed as new in the ADR check, and the proposal cites ADR 230 for the new ownership routes without naming the route rows that ADR 230 decision 3 actually lists. Resolution: answered: No new decision-record route is claimed; error commands use make checks.
- R2.9 (failure mode): Matching cost at change.planned for hostile broad globs is covered only by a task with a budget named but no value; the budget is not defined, so the check cannot fail on a measured threshold. Resolution: answered: The limit task checks 256 rules in 500 ms at change.planned.
- R2.10 (scope): Task 2.1 requires an e2e run with a real runner and real CLI over TLS for a schedule-started unit, but the schedule trigger setup is listed as out of scope, so the e2e path depends on work this item excludes. Resolution: answered: E2E starts the schedule-sourced plan through the trigger paths that #901 provides.
- M.15 (duplicate): The ownership-rule routes and the path-set routes duplicate rest-routes-ownership-rules-create and add-named-path-sets-paths. The unless body duplicates the unless of several-approval-requirements-one-change. The change has no failing-test task. Resolution: answered: Routes and path-set shapes now come from rest-routes-ownership-rules-create and add-named-path-sets-paths; unless semantics follows several-approval-requirements-one-change; those changes are in depends_on; failing tests come first in tasks.

## Open questions

1. Does the advanced ownership rule carry its own optional unless Cedar body over change.planned facts for conditional auto-apply? Decides: owner.
   - Options: Add an optional unless Cedar body to the ownership rule shape for conditional auto-apply; Keep the ownership rule shape fixed and use the unless of another object.
   - Recommended: Add an optional unless Cedar body to the ownership rule shape for conditional auto-apply. Reason: ADR 225 decision 9 lists no unless field for ownership rules, while owner decision 24.3 in issue #902 allows an advanced ownership rule to allow automatic apply by operation-source conditions.
2. Does a true unless excuse only that ownership requirement or every requirement of the change? Decides: owner.
   - Options: A true unless excuses only that ownership requirement; A true unless excuses every requirement of the change.
   - Recommended: A true unless excuses only that ownership requirement; gate and approval-default requirements still need approval. Reason: ADR 225 decision 2 names three requirement sources and no ADR states the scope of an exemption for auto-apply.
3. Which change.planned fact carries the source of the operation and which values mark a push, a schedule and an API start? Decides: PM.
   - Options: Add operationSource to change.planned facts with values push, schedule and api; Reuse an existing change.planned fact with no schema change.
   - Recommended: Add operationSource to change.planned facts with values push, schedule and api. Reason: The schedule scenario cannot be built or tested until the fact name and its values are fixed.
4. Does a rule or path set saved after the plan but before the apply change the approval set of that apply? Decides: PM.
   - Options: Evaluate once at change.planned and keep the record at apply; Re-evaluate ownership rules at apply and refuse on a new match.
   - Recommended: Evaluate once at change.planned and keep the record at apply. Reason: No ADR states whether ownership evaluation repeats at apply or the decision record is kept.
5. What happens when the owners of a matched rule leave no eligible approver other than the author? Decides: PM.
   - Options: Keep the requirement in approval wait when only the author is eligible and authorMayApprove is false; Refuse the plan at change.planned when the requirement has no eligible approver.
   - Recommended: Keep the requirement in approval wait when only the author is eligible and authorMayApprove is false. Reason: ADR 225 decision 7 makes the author eligible only when authorMayApprove is true, so a matched rule can leave an unsatisfiable requirement.

Not spec-ready: the change conflicts with an owner-tagged decision of ADR 225; question 1 needs a change to the words of an owner-tagged decision.

Questions considered and answered:

- Where do ownership rules live and how do they add requirements? ADR 225 decision 2 and decision 9: an ownership rule in enforce mode that applies adds one requirement and needs no gate. ADR 230 decision 1: it is stored in the server and set through the API routes of the ownership-routes change.
- How does conditional auto-apply use unless? The unless model follows several-approval-requirements-one-change: one rule gives one requirement and the unless body excuses only that requirement. Issue #902 decision 24.3 puts the unless body on the advanced ownership rule itself, which needs the owner decision in open questions.
- Which routes, audit entries and limits apply to ownership rules and path sets? Limits, errors and audit entries follow ADR 230 decisions 5 and 6 and the route shapes of rest-routes-ownership-rules-create and add-named-path-sets-paths.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
