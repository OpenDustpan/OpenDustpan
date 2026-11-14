## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add server tests in pkg/dustpan/dustpan_test.go (TestInventoryRouting) for a stale status field in a unit write and a status query parameter, against ANY /api/inventory/unit-collection with an access token Check: The new tests fail on main and pass after the removal
- [ ] 1.2 Add a restore test in pkg/backup/restore_check_test.go (TestCheckRestoreAcrossDialects) for a backup with a status column Check: The test fails on main and passes after the removal

## 2. Removal

- [ ] 2.1 Remove Status from inventory Change and Entry in pkg/inventory and update the store fixtures Check: TestHistoryConformance passes without Status
- [ ] 2.2 Remove the status column from the baseline migration and queries.sql of each dialect under pkg/store/sqlite and pkg/store/postgres, and from sqliteBackupTables in pkg/store/sqlite/backup_columns.go, then run `make generate` Check: TestCheckColumnsCoverage passes with no status column
- [ ] 2.3 Update assertInventoryEntry in pkg/store/storetest/storetest.go and remove its status comparison; inventoryEntry in pkg/store/postgres/inventory.go no longer reads status Check: Store conformance passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Remove the status field from the API description and the docs page for the inventory list Check: API description has no status field and regenerated clients match
- [ ] 4.2 Run e2e against ANY /api/inventory/unit-collection with an access token Check: `make e2e` passes
- [ ] 4.3 Verify the console src/api-units.ts parse removal merges before the next server tag Check: The console change is merged; `make check` reports zero findings
