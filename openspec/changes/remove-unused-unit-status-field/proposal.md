---
version: 1
---

Issue: #1208.

## Problem

A developer lists the stacks and workspaces of a namespace and sees one fixed set of states. An unused free-string status field adds doubt about which value to trust, so the server removes it.

## Evidence

- The inventory domain lives in pkg/inventory. Proof: main lists package pkg/inventory
- SQLite lists backup tables in sqliteBackupTables. Proof: pkg/store/sqlite/backup_columns.go:77 (func sqliteBackupTables)
- Code regenerates generated stores with make generate. Proof: Makefile:336
- End to end checks run the real CLI with make e2e. Proof: Makefile:275

## Fix boundary

- Delete the stored status column from the inventory and inventory change log tables in both dialects by editing the single baseline in place, including the upsert in queries.sql, and regenerate sqlc with make generate
- Delete inventory Change Status and inventory Entry Status
- Delete the status field from the history view and the backup table list
- Delete the status field from the API description

Out of scope:

- The states array, the state filter, and badge order
- The console repository edit to src/api-units.ts, which merges before the next server tag
- Any upgrade path for the unshipped columns

## Check

- Add server tests in pkg/dustpan/dustpan_test.go (TestInventoryRouting) for a stale status field in a unit write and a status query parameter, against ANY /api/inventory/unit-collection with an access token Check: The new tests fail on main and pass after the removal
- Add a restore test in pkg/backup/restore_check_test.go (TestCheckRestoreAcrossDialects) for a backup with a status column Check: The test fails on main and passes after the removal
- Remove Status from inventory Change and Entry in pkg/inventory and update the store fixtures Check: TestHistoryConformance passes without Status
- Remove the status column from the baseline migration and queries.sql of each dialect under pkg/store/sqlite and pkg/store/postgres, and from sqliteBackupTables in pkg/store/sqlite/backup_columns.go, then run `make generate` Check: TestCheckColumnsCoverage passes with no status column
- Update assertInventoryEntry in pkg/store/storetest/storetest.go and remove its status comparison; inventoryEntry in pkg/store/postgres/inventory.go no longer reads status Check: Store conformance passes
- Remove the status field from the API description and the docs page for the inventory list Check: API description has no status field and regenerated clients match
- Run e2e against ANY /api/inventory/unit-collection with an access token Check: `make e2e` passes
- Verify the console src/api-units.ts parse removal merges before the next server tag Check: The console change is merged; `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Postgres builds an inventory entry in inventoryEntry. (`pkg/store/postgres/inventory.go:571 (func inventoryEntry)`)
- Store conformance checks an inventory entry in assertInventoryEntry. (`pkg/store/storetest/storetest.go:1354 (func assertInventoryEntry)`)
- SQLite lists backup tables in sqliteBackupTables. (`pkg/store/sqlite/backup_columns.go:77 (func sqliteBackupTables)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 265: A unit shows all its states with one fixed set of words** (not owner-tagged).

- Decisions that apply: 14 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A separate change removes the `Entry.Status` field and the `status` field of the API. They are a free string that no code sets, and no released console version reads them."
- Effect: Follows the decision: removes the free-string Entry.Status and API status field and keeps the states array as the only unit state.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Is removal of the unused status field required or optional? Required. ADR 265 decision 14 orders a separate change that removes Entry.Status and the API status field.
- Does this change add the numbered migration 0108 named in the issue? No. Project rules keep one baseline migration per dialect until the first tag. The change edits the baseline of each dialect in place. Migration 0108 named in the issue does not exist on main, so no numbered migration is added or edited.
- Where is the console parse removed? In the console repository in src/api-units.ts. It merges before the next server tag and is out of scope here, with a verification task.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
