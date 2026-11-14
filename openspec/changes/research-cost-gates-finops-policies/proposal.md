---
version: 1
---

Issue: #907, #980, #981, #1146.

## Problem

As a namespace administrator I set cost rules on stacks and workspaces, but I cannot tell how a cost estimate should enter gates, Expr and approval rules or which FinOps policy comes first.

## Evidence

- Issue 907 asks how competitors express cost policy. Proof: How do HCP Terraform, Spacelift, env zero, Infracost and FinOps tools express cost policy?
- Issue 907 acceptance needs a research note with primary sources and options. Proof: A research note with primary sources and options.
- Issue 907 acceptance needs a spike issue opened from the recommendation. Proof: A spike issue opened from its recommendation.

## Fix boundary

- Research note on cost gates, FinOps policies, competitor behavior and options for estimate facts and approval rules
- Spike issue opened from the recommendation of the note

Out of scope:

- FinOps export format selection while parked in #980; in scope is only which cost facts and gates a FinOps policy needs, not the FOCUS export shape
- Environment and promotion model in #981
- Push route and store implementation in #1146
- Any estimator adapter selection in RFC 45

## Check

- Write the NEW file docs/research/cost-gates-finops.md with primary sources and options. Check: The NEW file docs/research/cost-gates-finops.md covers HCP Terraform, Spacelift, env zero and Infracost, cost facts, gates, Expr as gate.Engine adapter, approval rules, fail closed for each option, the 15 minute wait, FinOps policy scope without export shape and a minimal spike on push data. No route: the test reads the file only.
- Open the spike issue and record its number in the NEW file docs/research/cost-gates-finops.md. Check: `gh issue view <number recorded in the note>` shows the open spike issue. No route: the test reads the issue only.
- Run check for the changed files. Confirmed by: CI (make check). Check: `make check` with zero findings. No route: the test reads docs files only.

Depends on:

- `test-no-gate-schema-has` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Gate approval-rules service exists on main. (`pkg/gate/config.go:206 (func ConfigService.ApprovalRules)`)
- Gate config handler for approval rules exists on main. (`internal/api/server/gate_config.go:422 (func Handler.listApprovalRules)`)
- OpenTofu e2e helper exists on main. (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 144: A pluggable cost port: estimates, actual spend and budgets** (not owner-tagged).

- Decisions that apply: 5 (extended), 6 (current), 13 (current).
- Changed by: ADR 149 extends decision 15; ADR 210 changes; ADR 222 supersedes decision 14; ADR 224 extends decision 14; ADR 226 supersedes decision 14; ADR 259 extends decision 5; ADR 286 changes. Chain to the end: ADR 149, ADR 210, ADR 222, ADR 224, ADR 226, ADR 259, ADR 286, ADR 247, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 240, ADR 258, ADR 263, ADR 248, ADR 343, ADR 319, ADR 365, ADR 297, ADR 358, ADR 366, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 149, 210, 222, 224, 226, 259, 286.
- Quote: "Two ways in, one store. - **Pull.** The `cost.Estimator` port reads the masked plan of a phase and answers the cost of each resource."
- Quote: "No estimator in the binary now. The binary has no built-in estimator. An adapter of the `Estimator` port is a separate package, and a dependency needs the approval of the owner."
- Effect: Research stays inside the cost model: push then pull, no built-in estimator, budget never blocks.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 1, 3, 8).

- Decisions that apply: 1 (current), 3 (extended), 8 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "Cost gates (owner). A cost gate is an ordinary gate at `change.planned` on the `cost` and `budget` facts."
- Quote: "With no cost estimate, `cost.available` is `false` and `cost.deltaMonthly` is 0."
- Effect: Research treats cost as ordinary facts at change.planned, with a missing estimate as available false and fail closed.
- Conflict: none.

**ADR 259: Cost estimates start with the push route, and the first pull adapter comes later** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Slice 1 is the push route. The first slice of cost estimates ships the push route of ADR 144 decision 5, the store, the reads, the budget status and the facts of ADR 226."
- Effect: Research does not change slice order: push route first, no pull adapter.
- Conflict: none.

**ADR 222: One model for access, gates, approvals and facts** (owner-tagged: decision 7).

- Decisions that apply: 7 (extended).
- Changed by: ADR 286 changes; ADR 335 extends decision 7; ADR 351 extends; ADR 354 changes decision 5. Chain to the end: ADR 286, ADR 335, ADR 351, ADR 354.
- Current rule: this ADR, with ADR 286, 335, 351, 354.
- Quote: "Cedar, linked and as a service (owner). The server links `cedar-go` and can also call an external Cedar service, behind one deep engine port (ADR 224 decision 9)."
- Effect: Research frames any Expr option as an adapter of the engine port.
- Conflict: none.

**ADR 335: Gate decisions: deny gates, held locks, unlocked declares and the engine port** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: ADR 377 extends decision 7. Chain to the end: none.
- Current rule: this ADR, with ADR 377.
- Quote: "The gate engine port stays pluggable (owner, 2026-11-07). The port `gate.Engine` of ADR 224 decision 9 stays the one place where an engine plugs in."
- Effect: Expr is only a possible adapter of gate.Engine; Cedar stays default.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does research need to select the first pull estimator adapter? ADR 144 decision 5 gives pull and push, ADR 259 decision 1 orders push first with no adapter, so research selects no pull adapter.
- What do gates do when no estimate arrived? ADR 226 decisions 3 and 8 give cost.available false and a notify-only budget, so every cost gate fails closed on available.
- How do #980, #981 and #1146 divide from #907? #980 is parked and #981 is promotion, so both stay out; #1146 owns the push route; this change owns gate and FinOps policy research.
- How does Expr fit the gate engine? ADR 222 decision 7 and ADR 335 decision 7 keep Cedar as default behind gate.Engine; Expr is an adapter with conformance.
- What happens on the 15 minute wait and a late estimate? ADR 226 decision 8 waits at most 15 minutes, then cost.available is false.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
