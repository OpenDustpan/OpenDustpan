## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Confirm the two live unit restore paths in pkg/backup/restore_steps.go:50 (func readRestoreInput). Check: Read pkg/backup/restore_steps.go:50 (func readRestoreInput) and pkg/store/postgres/unit_state.go:239 (func restoreUnitState) to list both live paths.
- [ ] 1.2 Write failing unit restore tests in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents). Check: Failing test TestUnitRestoreWritesAuditEntry fails on main in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents).

## 2. Work

- [ ] 2.1 Write the unit restore audit entry for both tools in pkg/store/postgres/unit_state.go:239 (func restoreUnitState). Check: TestUnitRestoreWritesAuditEntry passes after the change in pkg/store/postgres/unit_state.go:239 (func restoreUnitState).
- [ ] 2.2 Fix the ended-restore audit paths in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents). Check: TestRestoreEndEntryPaths passes after the change in pkg/backup/restore_steps.go:361 (func RestoreService.appendEvents).

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the service and store; add no new route. The existing routes keep their authorization actions. Check: The route test passes for ANY /api/stacks/{}/{}/{}/restore/{} and ANY /api/v2/state-versions/{}/actions/restore.
- [ ] 4.2 Add an e2e step against ANY /api/stacks/{}/{}/{}/restore/{} with an access token that restores and reads ANY /api/audit/entries. Confirmed by: CI (make e2e). Check: The e2e step calls ANY /api/stacks/{}/{}/{}/restore/{} with an access token and reads ANY /api/audit/entries. Confirmed by: CI (make e2e).
- [ ] 4.3 Write the docs page for the unit restore audit entry. Confirmed by: CI (make docs-lint). Check: The page names the entry fields and its commands run as written. Confirmed by: CI (make docs-lint).
- [ ] 4.4 Run the usability check where a Developer restores from the docs page alone. A person's step. Check: A Developer restores from the docs page alone. A person's step.
- [ ] 4.5 Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` reports zero findings. Confirmed by: CI (make check).
