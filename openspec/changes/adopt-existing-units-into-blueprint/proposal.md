---
version: 1
---

Issue: #921, #922, #923, #974.

## Problem

As a namespace administrator who keeps stacks and workspaces that already exist, they want to put them under a blueprint without a rebuild, so they get upgrades and the request life cycle.

## Evidence

- Blueprint routes exist on main. Proof: ANY/GET /api/dustpan/v1/self-service/blueprints/{}
- The unit list route exists on main. Proof: GET /api/dustpan/v1/namespaces/{}/units
- Issue 921 scopes adopt to an adopt action, a no-change or diff plan and audit entries, with upgrades out, and says adoption needs a short ADR. Proof: Scope - In: an adopt action that makes a Catalog request for an existing unit; a plan that must show no change (or the change for review); audit entries. Design status: adoption needs a short ADR.

## Fix boundary

- Record the open questions of the bind model; no code or spec until the short ADR and the RFC are accepted.
- Coarse placeholder specs: none until the questions close.

Out of scope:

- Upgrade of requests to a new blueprint version (issue 922).
- Batches as records and bulk actions (issue 923).
- Versions-in-use report and upgrade actions (issue 974).

## Check

- Refine this change when its RFC is accepted. Check: A person's step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The access service resolves an existing unit for authorization. (`pkg/access/service.go:1681 (func Service.existingUnit)`)
- The error code unit.not-found exists. (`internal/errcode/catalog_unit.go:21`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 110: The Catalog: a gateway of declarative requests on the job model** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current).
- Changed by: ADR 210 changes; ADR 218 extends; ADR 222 supersedes decision 9; ADR 225 changes decision 9; ADR 286 changes; ADR 305 extends; ADR 308 extends. Chain to the end: ADR 210, ADR 218, ADR 222, ADR 225, ADR 286, ADR 305, ADR 308, ADR 247, ADR 248, ADR 335, ADR 351, ADR 354, ADR 299, ADR 360, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 210, 218, 222, 225, 286, 305, 308.
- Quote: "One request, one unit. A Catalog request asks for one unit from one blueprint, in the namespace of the request."
- Quote: "The request is the desired state."
- Effect: Adopt keeps one request for one unit and the desired-state document.
- Conflict: none.

**ADR 112: Blueprints: the document, its versions and Expr** (not owner-tagged).

- Decisions that apply: 9 (extended).
- Changed by: ADR 222 changes decision 12; ADR 222 supersedes decisions 6 and 7; ADR 224 supersedes decisions 6 and 7; ADR 228 changes decision 12; ADR 228 extends decisions 9 and 11; ADR 286 changes; ADR 360 extends. Chain to the end: ADR 222, ADR 224, ADR 228, ADR 286, ADR 360, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 248, ADR 343, ADR 319, ADR 365.
- Current rule: this ADR, with ADR 222, 224, 228, 286, 360.
- Quote: "The server computes it at the first submit and keeps it: a later change of an input never renames a unit."
- Effect: Adopt binds an existing unit, so the exists failure needs an RFC before adopt ships.
- Conflict: rfc_needed.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 3 (current), 4 (current).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "A plan with no change ends `succeeded`."
- Quote: "The policy input holds old and new values, never secret ones."
- Effect: A match ends as a no-change plan. A diff holds no secret value.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation. A feature that a person or a client can see has a route, a setting, a command, a flag, a console page or an error that a user reads. A pull request that changes only internal code needs no page."
- Effect: Adopt ships its docs page in the same pull request.
- Conflict: none.

## Open questions

1. What short ADR defines the adopt bind model, the request state and the unit-bind rule, and how is the exists failure of unit names changed to a bind? Decides: PM.
   - Options: Write a short ADR and an RFC for the bind on an existing unit; Refuse adopt on an existing unit name.
   - Recommended: Write a short ADR and an RFC for the bind on an existing unit Reason: Issue 921 says adoption needs a short ADR, and the bind conflicts with the accepted refusal.
2. What rule covers two adopts of one unit and an adopt during a running job, and how are unit inputs read and matched per tool? Decides: PM.
   - Options: One bind per unit by a unique constraint, server reads inputs by name; Person gives each answer, no server read.
   - Recommended: One bind per unit by a unique constraint, server reads inputs by name Reason: The issue gives no per-tool mapping or match rule.

Not spec-ready: the change conflicts with ADR 112: an RFC comes first.

Questions considered and answered:

- Does adopt fit the one request one unit model? Yes. ADR 110 decision 2 gives one request for one unit in one namespace.
- How are secrets handled in a diff? ADR 213 decision 4 keeps secret values out of the input, so a diff holds the flag only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
