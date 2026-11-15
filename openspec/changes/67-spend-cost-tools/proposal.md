---
version: 1
---

## Why

**Persona: developer.** Developer runs stacks and workspaces and wants to see what a change will cost before apply. Today the server gives no spend context, so cost review happens outside OpenDustpan or not at all.

The user path this change must make work:

1. **CLI.** Developer runs preview and apply with the Pulumi CLI and OpenTofu as today with no cost step in the path.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer asks the server for spend and the server has no spend route. | 404 Not Found with no spend data. | `opendustpan --help` |

## What Changes

- Do RFC 67 research on spend and cost tools first with no dependency on RFC 45.
- Add no routes, no store, no background work, and no console view in this change.

Out of scope:

- Cost estimation model of RFC 45
- Drift detection
- Existing CI as executor
- Programmatic runs API

## Usability

Where the path still costs the person time or surprises them:

- Developer finds no spend or cost context in the current CLI path until the RFCs are decided.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has only an unrelated forge app-budget test, no spend route or store (`pkg/forge/github/appbudget_internal_test.go:139 (TestEverySignedCallSpendsTheAppBudget)`)
- Main records tool operations under a generic audit kind, with no spend kind (`pkg/audit/audit.go:44 (tool-operation)`)
- Main has operation-step and scheduler tests unrelated to cost (`pkg/dustpan/jobservice_applysteps_internal_test.go:107 (TestOperationStepsRefuseOtherTools)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 13: Separate tool models; shared access control and inventory** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 19 extends; ADR 41 changes; ADR 150 changes; ADR 247 extends; ADR 265 extends; ADR 337 extends. Chain to the end: ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 47, ADR 248, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 153, ADR 346, ADR 286, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 19, 41, 150, 247, 265, 337.
- Quote: "Each tool keeps its own domain model, state machine and storage schema"
- Effect: This change merges no tool model. A later cost view stays a projection over unit references and shares no types or tables between stack and workspace.
- Conflict: none.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "A read masks secret values."
- Quote: "Actions. The actions follow the names in `pkg/identity`:"
- Effect: This change adds no state read and no reveal call, so it follows the masked-read rule and the action names. A later refined change that shows cost next to state must use masked reads and reveal only.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What source gives the cost number that the user sees, and when is it computed?
   - Options: Estimates computed in server from state and price data; Estimates from external cost tool at plan time; Actual spend ingested from cloud billing only, no estimates.
   - Provisional decision (owner review 14 Nov): Decide the model in RFC 45 first, then refine integration to one source and one compute time.
   - Reason: RFC 45 must decide the estimation model before tool integration moves forward. Benchmark: AWS Cost Explorer
   - Persona impact: Developer sees cost context without extra steps; namespace administrator sees spend per stack and workspace without new permissions. Escape hatch: Estimates stay advisory with a visible source and time; an apply never waits on an estimate.
2. Which cost tools does OpenDustpan integrate with, and where does the tool run?
   - Options: Infracost or OpenCost at plan time; Cloud billing import per namespace; No third-party tool, server-computed estimates only.
   - Provisional decision (owner review 14 Nov): Name one tool and one run place in RFC 67, with server-side fetch behind egress rules.
   - Reason: RFC 67 research names the tools; the decision sets the trust boundary and the operator burden, with ADR 41 masking secrets on reads. Benchmark: ISO 27001
   - Persona impact: Developer keeps one path to run plans; cost context appears where plans already show, with no new credential step. Escape hatch: Integration is read-only and off by default; no credentials move to runners or jobs, and no secret values leave the server.
3. Who may see cost data and under which authorization action and scope?
   - Options: Cost data per unit under unit actions; Cost data per namespace under namespace actions; Cost data per installation for operators only.
   - Provisional decision (owner review 14 Nov): Scope cost reads to the unit or namespace with the existing read actions, and record the choice in RFC 67.
   - Reason: ADR 13 and ADR 4 require a tool-neutral action on a stated object and fenced writes; the refined RFC must place cost reads in that model. Benchmark: AWS Cost Explorer
   - Persona impact: Developer and namespace administrator see only cost they may read; no new access path appears by default. Escape hatch: Cost reads start closed to namespace members and open only through explicit grants.

Questions considered and answered:

- How do secret values in state stay out of a cost tool? No. ADR 41 decision 2 masks secret values on every state read and decision 6 scopes plain-text values to `unit:read-secrets`. A cost integration must never send sealed values or plain secrets to a third-party tool; estimates stay over masked structure or server-held data only.
- Does any accepted ADR already decide how spend or cost data is stored or shown? No accepted ADR decides spend display or cost-tool integration. ADR 13 keeps tool models separate and shares only access control and inventory; a cost view must stay a projection over units. ADR 41 governs any read of state that a cost view would need.
- Does main already ship any spend or cost-tool path that this change would extend? No. The pack lists no route, action, docs page, or test for spend. The only pack hits are an unrelated forge budget test and generic audit kinds, so this coarse change refines nothing on main and adds no route.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
