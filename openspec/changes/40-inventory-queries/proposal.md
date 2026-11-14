---
version: 1
---

## Why

**Persona: developer.** A developer tries to find stacks and workspaces across tools in one namespace. Today the inventory has no rich query. This change sets the query shape once the projection scales.

The user path this change must make work:

1. **API.** List the stacks and workspaces of a namespace through the API.
2. **Docs page.** Read the docs page for the supported filters, sort keys, and page shape.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A filter value is not valid. | The server answers 400 with the filter name. Run the list call without that filter. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| A cursor, limit, or sort key is not valid. | The server answers 400 with the field name. Run the list call with no cursor and a limit within the maximum. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| A time range has from after to. | The server answers 400 with the time field name. Run the list call with from before to. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| The namespace is not visible to the caller. | The server answers 404 with no unit data. Check the namespace name and the grant. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- This item waits on the inventory projection at scale. Proof: | [RFC 40](rfc/0040-inventory-queries.md) Inventory queries                                          | the inventory projection (ADR 13) at scale                      |
- Main has an inventory routing test. Proof: pkg/dustpan/dustpan_test.go:199 (TestInventoryRouting)

## What Changes

- Define what an inventory query covers once the projection scales.
- Keep the inventory as a read projection with no new writers.

Out of scope:

- Scale work on the inventory projection itself.
- New tool models or a merged unit model.
- Console views and generated clients.

Depends on:

- inventory projection at scale (ADR 13)

## Usability

Where the path still costs the person time or surprises them:

- The list has no cross-tool filter until this ships. The docs page states the supported filters.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has an inventory routing test. (`pkg/dustpan/dustpan_test.go:199 (TestInventoryRouting)`)
- Main has a test that names declared queries. (`pkg/dustpan/apidesc_reads_test.go:39 (TestQueryReadsNameDeclaredQueries)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 13: Separate tool models; shared access control and inventory** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 19 extends; ADR 41 changes; ADR 150 changes; ADR 247 extends; ADR 265 extends; ADR 337 extends. Chain to the end: ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 47, ADR 248, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 153, ADR 346, ADR 286, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 19, 41, 150, 247, 265, 337.
- Quote: "It is a projection: each tool package publishes changes to it through a core"
- Effect: Queries read the projection only. No new writer and no merged tool model.
- Conflict: none.

**ADR 150: Unit history: blame and point-in-time views** (not owner-tagged).

- Decisions that apply: 17 (current).
- Changed by: ADR 154 changes decisions 14, 15 and 16; ADR 154 extends; ADR 199 changes decision 1; ADR 210 changes; ADR 242 extends decision 3. Chain to the end: ADR 154, ADR 199, ADR 210, ADR 242, ADR 238, ADR 286, ADR 310, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 199, 210, 242.
- Quote: "The list keeps its filter rule: it answers only the units that the caller can read now"
- Effect: Any query keeps the filter rule on the list view and the meaning of at.
- Conflict: none.

**ADR 247: Works with Backstage: an entity tab that shows the units of a component** (not owner-tagged).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "`namespace:read`, then `unit:read` on each unit"
- Effect: Filters reuse the existing label and reference list route and its checks.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "A protocol endpoint that an external system stores (a CLI, a runner, an IdP, a cloud trust policy) is not part of `/api/dustpan/v1` and has no API version."
- Effect: The NEW query lives under /api/dustpan/v1. Tool protocol paths do not move.
- Conflict: none.

**ADR 47: Soft delete, and a unit never moves between namespaces** (not owner-tagged).

- Decisions that apply: 1 (extended).
- Changed by: ADR 151 changes decision 3; ADR 153 extends decisions 1 and 3; ADR 346 extends decisions 5 and 7. Chain to the end: ADR 151, ADR 153, ADR 346, ADR 154, ADR 210, ADR 217, ADR 220, ADR 213, ADR 248, ADR 309, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 300, ADR 353, ADR 315, ADR 356, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 151, 153, 346.
- Quote: "A delete is a soft delete. The server keeps the state of a deleted unit for a retention period. During this period a person can restore the unit."
- Effect: Queries list live units only. Deleted units stay under the restore path.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. How does a query avoid leaking unreadable units through counts, sort order, cursors after a grant change, or a unit deleted or renamed between pages?
   - Options: Check namespace:read then unit:read on every row, with no totals; Return totals with the same row filter.
   - Provisional decision (owner review 14 Nov): Check namespace:read then unit:read on every row, with no totals.
   - Reason: Row checks at use keep every page safe after grant, delete, and rename changes. Benchmark: AWS practice: list APIs filter server side by caller access and return no totals for unreadable rows.
   - Persona impact: No leak upsets no persona. Escape hatch: Fail closed on a bad cursor with 400 and no rows.
   - Owner-tagged ADRs involved: ADR 247 (their words do not change).
2. Which filters, sort keys, and page shape does the first query support?
   - Options: Filter by tool, label, reference, unit state, and last change time range with cursor pages; Full text search from the start.
   - Provisional decision (owner review 14 Nov): Filter by tool, label, reference, unit state, and last change time range with cursor pages.
   - Reason: A small set keeps query cost bounded at scale. Benchmark: AWS practice: list APIs use opaque page tokens, fixed sort keys, and per-call limits.
   - Persona impact: A small set upsets no persona. Escape hatch: Add more filters later without changing the page shape.
3. How do the last change time range and at work together, and what happens to a cursor after writes, soft delete, rename, or grant change?
   - Options: Last change time range filters the projection; at reads a past view and never mixes with live sort; One time field covers both filtering and at.
   - Provisional decision (owner review 14 Nov): Last change time range filters the projection; at reads a past view and never mixes with live sort.
   - Reason: Separate meanings keep the past view stable and the live list safe. Benchmark: SOC 2 practice: a point-in-time read states its time source and fails closed on stale input.
   - Persona impact: Separate fields confuse no persona once the docs page shows one example of each. Escape hatch: Fail closed on a stale or hostile cursor with 400.

Questions considered and answered:

- Does a query write to the inventory projection? ADR 13 answers it: the inventory is a projection that each tool package writes in its own transaction. This change adds read filters only.
- What authorization does a query use? ADR 150 decision 17 and ADR 247 decision 7 answer it: namespace:read, then unit:read on each unit, with only readable units in the answer.
- Which route does the query extend? ADR 210 and ADR 247 answer it: the NEW query extends GET /api/dustpan/v1/namespaces/{namespace}/units. Main keeps the old /api/inventory paths as the baseline.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
