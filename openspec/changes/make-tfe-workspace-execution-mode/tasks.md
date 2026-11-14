## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for the PATCH of mode and pool, the refusals and the JSON:API errors, through PATCH ANY /api/v2/workspaces/{} with an access token, in `NEW internal/api/tfe/workspace_execution_test.go`. Check: TestTFEWorkspacePatchExecutionMode fails on main
- [ ] 1.2 Write a failing test in `NEW internal/api/tfe/workspace_execution_test.go` that a read through the tool-neutral API shows the same pool, with an access token. Check: TestTFEWorkspacePatchPoolMatchesToolNeutral fails on main

## 2. Build

- [ ] 2.1 Wire the PATCH through the authorizer and pkg/workspace to the unit pool fields. Check: The tests above pass
- [ ] 2.2 Add the docs page in `NEW docs/guides/tfe-execution-mode.md`. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the e2e step with the real OpenTofu CLI on PATCH ANY /api/v2/workspaces/{} with an access token. Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Check the wiring, the route authorization action, the usability check from the docs page alone and the PATCH latency. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
