---
version: 1
---

Issue: #2037.

## Problem

A namespace administrator tracks many stacks and workspaces across blueprint versions. Problem: fleet upgrades need lineage, target sets, done-checks, and waves, but the design is not ready and the work is not scheduled.

## Evidence

- Issue 2037 defers fleet lifecycle work as not scheduled with later deep design. Proof: This issue holds the owner decisions of 27 October 2026 for later work. It is not scheduled.
- Main keeps an inventory domain package. Proof: pkg/inventory

## Fix boundary

- No behavior change: fleet lifecycle work stays deferred until later design, tracked by issue 2037.

Out of scope:

- Inventory blueprint lineage, filters and counts by blueprint version, owner and state, and new blueprint and drift event types, which ship with the Catalog store and which the Catalog store work tracks
- Fleet migration object with stored inventory query as target set, done-check of successful apply of version N or higher with no drift and never at review-request merge, exemptions that always expire, and an owner for each straggler
- Server-driven upgrade waves with a cap on open changes and a stop on failures
- Batch upgrade and migration tooling for providers, modules, and blueprints with code-change recipes

## Check

- Record deferred fleet lifecycle scope from issue 2037 with no behavior change. Confirmed by: CI (`make check`). Check: `make check`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main builds inventory entries in the postgres store (`pkg/store/postgres/inventory.go:571 (func inventoryEntry)`)
- Main asserts inventory entries in the store conformance helper (`pkg/store/storetest/storetest.go:1354 (func assertInventoryEntry)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an accepted ADR already define blueprint lineage or fleet migrations? No. The pack lists no ADR and no RFC, and the keywords, evidence, and gaps lists are empty, so no decision text applies to this record.
- Should this change implement inventory filters or migration objects now? No. Issue 2037 states the work is not scheduled, the first tier ships with the Catalog store, and the migration object plus upgrade waves need deep design work first, so this change makes no behavior change.
- What target set and done-check would a future fleet migration use? The deferred scope stays in out_of_scope in plain words. There is no implementation question for this change to leave open, so open_questions reads none.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
