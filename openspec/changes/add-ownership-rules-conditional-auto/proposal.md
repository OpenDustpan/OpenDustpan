---
version: 2
---

Issue: #902.

## Why

**Persona: namespace administrator.** A namespace administrator names owners for units and paths so a change needs owner approval, and allows unattended apply only when conditions on the source of the operation hold.

The user path this change must make work:

1. **API.** Create ownership rules and named path sets for units and paths through the routes of the depended changes.
2. **API.** Start a plan from a push, a schedule or the API and read the decision record for requirements and autoApply.
3. **Console.** Approve as an owner when required, or let a schedule-started plan inside unless apply with no approval.
4. **Docs page.** Follow the docs page to set owners and the unless condition.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply without the owner approval waits in approval wait. | The job stays in approval wait. Read the gates of the unit to see the matched rule and the owners. | `GET /api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}/gates` |
| A warn on the plan stops unattended apply. | The change stays in approval wait and the record shows autoApply false. Get the owner approval. | `GET /api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}/gates` |
| Save of an ownership rule with a bad condition fails. | The save fails with 422 gate.invalid and names the field. List the stored approval rules to compare. | `GET /api/dustpan/v1/namespaces/{namespace}/approval-rules` |

Evidence for the claims above:

- Ownership matching, unless over change.planned and autoApply in the decision record are the scope of this item. Proof: Issue #902 scope: matching on units and named path sets; the unless Cedar body over the facts of change.planned; the autoApply result in the decision record.
- The gate package names the change.planned decision point and stores autoApply in the record. Proof: pkg/gate/gate.go:176 PointChangePlanned = "change.planned"; pkg/gate/record.go:173 AutoApply bool.
- Only the decision record holds autoApply today; no other non-test code names it. Proof: rg -n -F --glob '!*_test.go' 'autoApply' pkg internal cmd gives 2 matches: pkg/gate/record.go:173 and pkg/gate/testdata/record.json:194.

## What Changes

- Ownership match at change.planned that adds one owner requirement per matched enforce rule
- Unless evaluation on the matched ownership rule with operationSource, with warn and unless errors forcing autoApply false
- Decision-record fields for the rule, the matched path set, the unless result and autoApply, with an apply recheck that only adds
- Docs page for ownership rules and conditional auto-apply

Out of scope:

- IaC provider resources for ownership rules
- Review modes, mirrored approvals and plan comments
- Trigger setup, which #901 provides
- Ownership-rule and path-set route shapes, validation, limits and audit entries, which rest-routes-ownership-rules-create and add-named-path-sets-paths provide
- Multi-requirement counting, which several-approval-requirements-one-change provides

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

- A broad pattern adds surprise approvals; the record names the rule and the matched path set.
- Auto-apply is clear because the record shows the unless result and any warn.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The gate package defines the change.planned decision point. (`pkg/gate/gate.go:176`)
- The decision record stores the autoApply result. (`pkg/gate/record.go:173`)
- The record fixture uses the change.planned schema. (`pkg/gate/testdata/record.json:3`)
- The facts fixture uses the change.planned schema. (`pkg/gate/testdata/facts.json:85`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change ships its docs page in the same pull request.
- Conflict: none.

**ADR 225: Approval rules, approval defaults and ownership rules** (owner-tagged: decision 2, 9).

- Decisions that apply: 2 (current), 9 (current).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "An ownership rule needs no gate."
- Quote: "An ownership rule is stored in the server and configured through the API."
- Effect: Ownership rules in enforce mode add requirements. The unless field on an ownership rule needs an owner decision, because decision 9 lists no unless field.
- Conflict: owner_blocks.

**ADR 230: Gate configuration: actions, routes, errors and audit entries** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 6 (current).
- Changed by: ADR 268 changes decision 3; ADR 286 changes; ADR 335 extends decision 4; ADR 355 changes decision 3; ADR 355 changes decision 4. Chain to the end: ADR 268, ADR 286, ADR 335, ADR 355.
- Current rule: this ADR, with ADR 268, 286, 335, 355.
- Quote: "Gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting are stored in the server."
- Quote: "A scope holds at most 256 gates, 64 access limits, 64 approval rules, 256 ownership rules and 64 path sets."
- Effect: Rules stay stored in the server with the limits. Routes come from the depended changes.
- Conflict: none.

## Open questions

1. Does an ownership rule carry its own optional unless Cedar body for conditional auto-apply? Decides: owner.
   - Options: Add an optional unless body to the ownership rule shape; Keep the shape fixed and use unless on the wider approval default.
   - Recommended: Add an optional unless body to the ownership rule shape. Reason: Owner decision 24.3 in the issue allows an advanced ownership rule to allow auto-apply by operation-source conditions, but ADR 225 decision 9 shows no unless field.

Not spec-ready: the change conflicts with an owner-tagged decision of ADR 225; question 1 needs a change to the words of an owner-tagged decision.

Questions considered and answered:

- Where do ownership rules live and how do they add requirements? ADR 225 decisions 2 and 9: a rule in enforce mode adds one requirement with no gate. ADR 230 decision 1 keeps them in the server.
- What does a true unless excuse? Only that ownership requirement. Other requirements still need approval.
- How do count and fresh authentication interact? ADR 225 decisions 5 and 9 stay: count and freshAuthSeconds apply, and a destroy needs fresh authentication.
- Does a rule saved after the plan change the approval set? The apply recheck adds requirements and removes none.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
