---
version: 1
---

## Why

**Persona: developer.** Developer tries to judge the cost of a planned change before apply, but the server gives no estimate. This change holds the place for that estimate until the research decides the approach.

The user path this change must make work:

1. **Console.** Developer opens the unit in the console to judge cost before apply and finds no estimate

## What Changes

- Record cost estimation as a blocked later item gated on the research result of RFC 67, not on the integration of RFC 67.

Out of scope:

- RFC 67 spend and cost tool research
- Drift detection
- Runs and the run queue

Depends on:

- 67-spend-cost-tools
- `67-spend-cost-tools` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- No cost path exists yet, so the person cannot act from this change.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main records an audit kind for tool operations (`pkg/audit/audit.go:44 (tool-operation)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current), 6 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read state. The server may read the contents of every state that it stores, for each tool. Each read is an endpoint with a declared action on a unit (ADR 13, ADR 17)."
- Quote: "Actions. The actions follow the names in `pkg/identity`: - `unit:read`: the unit, its history, and the masked state read."
- Effect: A future estimate reads stored state, so it follows the masked-read and action rules when RFC 45 is refined.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Where does cost estimation run and what price source does it use?
   - Options: Server-side estimate from plan output with a pluggable price table; Runner-side estimate with a pluggable price table; No built-in estimate, export data to an external spend tool.
   - Provisional decision (owner review 14 Nov): Server-side estimate from plan output with a pluggable price table.
   - Reason: AWS Cost Explorer reports past spend and does not estimate a planned change, so an estimate needs an explicit price table; after a step back a pluggable price table fed by plan output avoids hard-coded prices. Benchmark: AWS
   - Persona impact: Developer sees no cost path until the RFC decides; no existing path changes. Escape hatch: None needed before the RFC; no path changes until RFC 45 is accepted.
2. Does a cost estimate read masked data only, or does it reveal secrets, and which action and audit entry cover it?
   - Options: Estimate uses only the masked plan and state read under unit:read; Estimate reveals named secret values under unit:read-secrets with an audit entry.
   - Provisional decision (owner review 14 Nov): Estimate from the masked read under unit:read; name any reveal and its audit entry in RFC 45.
   - Reason: ADR 41 keeps masked and plain reads apart; after a step back masked-only estimation from plan output is the safe default. Benchmark: AWS
   - Persona impact: Developer keeps masked values by default; plain values appear only where the RFC allows. Escape hatch: Estimates stay on masked reads until the RFC names a reveal path.

Questions considered and answered:

- Does any accepted ADR already decide cost estimation? No accepted ADR decides cost estimation. ADR 41 decides how a state read masks secrets and which actions cover reads, so a future estimate must use those rules. No ADR answers where estimation runs or what price source it uses.
- Which authorization action covers a cost estimate and how is it audited? ADR 41 decisions 1 and 6 answer it: a read is an endpoint with a declared action on a unit, and masked reads fall under unit:read while plain secret values need unit:read-secrets. The estimate must not take plain secrets unless its RFC names unit:read-secrets and its audit entry.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
