## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a test that an inventory entry of a workspace and of a stack has no last release. Check: TestInventoryEntryHasNoLastRelease fails on main
- [ ] 1.2 Write a test that a repeated unlock within 24 hours still succeeds on the workspace row. Check: TestWorkspaceRepeatedUnlockUsesWorkspaceRow

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Remove the field from pkg/inventory, internal/api/inventory, pkg/store/postgres/inventory.go and the SQLite adapter, and the columns from both baselines. Check: sqlc diff is clean
- [ ] 3.2 Run the unlock and force-unlock e2e step against the real OpenTofu CLI on ANY /api/v2/workspaces/{}/actions/unlock with an access token. Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Update docs/reference/inventory-api.md. Check: make docs-lint
- [ ] 3.4 Do the usability check from the docs page, then run make check. Check: make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
