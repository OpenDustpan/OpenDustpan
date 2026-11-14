---
version: 1
---

Issue: #940, #1145, #1148, #1150.

## Problem

A site administrator tries to find unmanaged resources of a namespace from the inventory services of each cloud, without platform work on the runners of a team.

## Evidence

- The pack names no converter symbol. Proof: The keyword evidence lists matches for check, check_results and import only; no converter symbol appears.
- The pack shows the package pkg/runner, which holds runner pools. Proof: pkg/runner in the pack main list.
- The pack shows the package internal/unmanaged, which holds the sweep and finding code. Proof: internal/unmanaged in the pack main list.

## Fix boundary

- Needs an RFC that supersedes ADR 164 decisions 1 and 5 first.
- After it: a converter port with a conformance suite, a first converter, the resource index fed from converter output, scheduling in an operator pool, a test that no code path starts a sweep as a runner job, and a docs page.
- A pool may have the sweep purpose only when it is an operator pool. A namespace pool with the sweep purpose is refused at create and at update with the NEW error code `runner.sweep-pool-not-operator` (fail closed). The scenario is written when the RFC is accepted.
- Converter reports keep the bounds and the scope rules of the runner report; an incomplete scope resolves no finding.

Out of scope:

- Health checks from state check results (#1145)
- Client-encrypted OpenTofu state (#1148)
- Resource search and import code (#1150)

## Check

- Refine this change when its RFC is accepted. Check: RFC accepted that supersedes ADR 164.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The OpenTofu adapter builds the resource index from state. (`pkg/workspace/tofu_state.go:1737 (func tofuResourceIndex)`)
- Runner pool error codes exist, for example a pool that is in use. (`internal/errcode/catalog_runner.go:22 (runner.pool-in-use)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 1 (current), 5 (changed).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "A sweep runs on a runner of the team, never in the server."
- Effect: Conflicts with the runner sweep path. An RFC must supersede it before implementation.
- Conflict: rfc_needed.

**ADR 163: The resource index and unmanaged resources found from state** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 166 changes decisions 5 and 11; ADR 210 changes; ADR 216 changes decision 9. Chain to the end: ADR 166, ADR 210, ADR 216, ADR 286, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 166, 210, 216.
- Quote: "A tool-neutral projection. The **resource index** holds, for the latest state item of each unit, one entry for each managed resource: the unit, the key of the resource in its state (the URN or the address), the type, and its **resource keys**."
- Effect: The index stays. Converter output feeds it in the same shape.
- Conflict: none.

## Open questions

Not spec-ready: the change conflicts with ADR 164: an RFC comes first.

Questions considered and answered:

- May a namespace pool have the sweep purpose? No. Owner decision, 13 Nov 2026 (queue P14): only an operator pool may; a namespace pool is refused.
- Does an accepted ADR allow converter-fed sweep? No. ADR 164 decisions 1 to 5 require a team runner, so an RFC is needed.
- Does the server run converter code? No. ADR 164 decision 1 forbids a cloud adapter in the server. The converter runs on the operator pool; the server only reads its report.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
