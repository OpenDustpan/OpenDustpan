---
version: 3
---

Issue: #3510.

## Why

**Persona: namespace administrator.** A namespace administrator tries to place a stack or workspace in another namespace. The server has no move endpoint for it, so the administrator needs a stated export and import path and a list of what does not carry over.

The user path this change must make work:

1. **Docs page.** Read the guide for moving a stack or workspace to another namespace by export and import.
2. **API.** List the source unit in its namespace, then export it.
3. **API.** Import it into the target namespace under a free name, and read the dry run plan.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The target name already holds a unit, deleted units included | The replay skips each version that the target already holds with the same serial, lineage and MD5. It does not skip a unit. Deleted units hold the name too. List deleted units, then pick a free target name. | `GET /api/dustpan/v1/namespaces/{namespace}/deleted-units` |
| The source unit is not found in its namespace | The server answers 404. Check the source name in the unit list of the source namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- Main serves unit routes scoped to one namespace. Proof: GET /api/dustpan/v1/namespaces/{}/units
- Main has a unit lifecycle helper on the server. Proof: (read only) `pkg/dustpan/dustpan.go` line 2943 (func unitLifecycles), (read only) `pkg/dustpan/dustpan_test.go` line 154 (func TestStackLifecycle), (read only) `pkg/store/storetest/unit_query.go` line 443 (func testQueryUnitsUnitPages)
- A unit never moves between namespaces and export plus import is the way out. Proof: docs/adr/0047-soft-delete-and-units-stay-in-their-namespace.md decisions 5 and 7

## What Changes

- Record that a unit never moves between namespaces and that export plus import is the way out.
- Refine the guide to state what an export and import move does not carry over: grants, sharing rules, dependents and the import hold.

Out of scope:

- A one-click move endpoint between namespaces
- Move of a namespace between installations
- A server-assisted copy endpoint

Depends on:

- none

Builds on (not a change):

- unit lifecycle (RFC 17, ADR 47)

## Usability

Where the path still costs the person time or surprises them:

- No one-click path exists. The person exports and imports, and the guide must list what does not carry over.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main lists the guide pages that the docs runner skips. (`docs/commands-pending-pages.txt:1`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 47: Soft delete, and a unit never moves between namespaces** (not owner-tagged).

- Decisions that apply: 5 (extended), 7 (extended).
- Changed by: ADR 151 changes decision 3; ADR 153 extends decisions 1 and 3; ADR 346 extends decisions 5 and 7. Chain to the end: ADR 151, ADR 153, ADR 346, ADR 154, ADR 210, ADR 217, ADR 220, ADR 213, ADR 248, ADR 309, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 300, ADR 353, ADR 315, ADR 356, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 151, 153, 346.
- Quote: "A unit never moves between namespaces. The server has no action and no endpoint for it. A unit belongs to its namespace."
- Quote: "Export and import are the way out. A person exports a unit and imports it into any namespace, on any installation (ADR 29)."
- Effect: This change adds no move action and no move endpoint. It refines RFC 66 as an export and import guide.
- Conflict: none.

**ADR 346: A unit moves between collections, never between namespaces** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No one-click move between namespaces (owner, 2026-11-07). The server has no one-click move of a unit to another namespace. Such a move would break grants. A person exports the unit and imports it into the other namespace, as ADR 47 decision 7 says."
- Effect: This change follows the owner rule: no one-click move of a unit to another namespace.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does RFC 66 stay as an export and import guide with no one-click move between namespaces?
   - Options: Refine RFC 66 as an export and import guide with no new endpoint; Propose a one-click move endpoint between namespaces.
   - Provisional decision (owner review 14 Nov): Refine RFC 66 as an export and import guide with no new endpoint
   - Reason: ADR 47 decisions 5 and 7 and ADR 346 decision 1 already decide no one-click move, so the guide path upsets no persona. Benchmark: AWS follows owner-approved migration paths with no silent cross-tenant move and a stated export and import path.
   - Persona impact: A namespace administrator keeps the export and import path and sees which grants need a redo. Escape hatch: A person exports the unit and imports it into the other namespace.
   - Owner-tagged ADRs involved: ADR 346 (their words do not change).

Questions considered and answered:

- Does this change add a move endpoint between namespaces? No. ADR 47 decisions 5 and 7 and ADR 346 decision 1 say the server has no action and no endpoint for a move between namespaces. Export and import are the way out.
- What server help is allowed for a move between namespaces? Only export and import, with no new endpoint, per ADR 346 decision 1 and ADR 47 decision 7.
- What breaks on an export and import move? The guide lists grants, sharing rules and dependents, and the import hold and carry-over findings that the import tool already records.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
