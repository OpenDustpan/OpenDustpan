## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing planner tests for settings resolution, override authorization, manual lock refusal and missing location in pkg/dustpan/jobservice_internal_test.go. Check: TestStartResolvesFromSettings fails on main (main lacks NEW `TestStartResolvesFromSettings`; the nearest test on main is pkg/dustpan/jobservice_internal_test.go:347)
- [ ] 1.2 Write failing read-check tests for run-as principal loss and manual-fire intersection in the existing file pkg/trigger/readcheck_test.go: extend it in place and add new tests for the new behavior only. Check: TestTriggerOutputReadDeniesAfterGrantLoss fails on main (main lacks NEW `TestTriggerOutputReadDeniesAfterGrantLoss`; the nearest test on main is pkg/trigger/readcheck_test.go:32)
- [ ] 1.3 Write a failing role test for the role mapping in pkg/access/planner_role_test.go. Check: TestJobActionsInBuiltInRoles fails on main (main lacks NEW `TestJobActionsInBuiltInRoles`; the nearest test on main is pkg/access/planner_role_test.go:13)

## 2. Build

- [ ] 2.1 Add location, pool, profile ID and steps to unit settings with validation in internal/api/settings/serve.go. Check: go test ./pkg/unitsettings/...
- [ ] 2.2 Wire the read check into the output read handlers in pkg/dustpan/triggers.go. Check: go test ./pkg/dustpan
- [ ] 2.3 Add the actions to the built-in roles in pkg/access. Check: go test ./pkg/access -run TestActionsWithoutRoutesAreUnclassified

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 BLOCKED by the missing e2e helper for the trigger fire route and the job-token output read: add the e2e step against the real binary after the helper exists. Check: BLOCKED by the e2e helper.
- [ ] 4.2 Write the docs page docs/guides/unit-settings-pool.md (NEW) and the usability check from the page alone. Check: make docs-lint.
- [ ] 4.3 Run the full check. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
