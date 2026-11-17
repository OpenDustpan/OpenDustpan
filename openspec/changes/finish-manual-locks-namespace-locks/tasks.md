## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first; record each difference in the pull request body. Read each fact of "What main already does" at its file, line and function on the current main. Do not edit design.md or proposal.md during the implementation. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Conformance cases for namespace cover and join-leave in pkg/manuallock/manuallocktest/suite.go. Check: TestManuallockNamespaceCover fails on main
- [ ] 1.2 Dispatch tests for wait, running phase, timeout and release in the NEW file pkg/manuallock/dispatch_test.go. Check: TestDispatchWaitsWithReasonLocked fails on main
- [ ] 1.3 Job-token tests for OpenTofu lock and Pulumi update start with race cases in pkg/manuallock/manuallock_test.go. Check: TestToolLockWithPreLockJobToken (NEW, in pkg/manuallock/manuallock_test.go) fails on main; when it passes from the start, the pull request body says so and the task counts as done
- [ ] 1.4 Bulk route tests with mixed, duplicate and over-limit cases in the NEW file pkg/manuallock/bulk_test.go. Check: TestBulkLockPerItem fails on main
- [ ] 1.5 Route tests for the bulk routes in a NEW test file in internal/api/inventory: mixed, duplicate and over-limit cases through the HTTP handler. Check: the new test fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.
- [ ] 2.2 Add the two bulk routes to the route table of the lock routes in internal/api/inventory/locks.go (func lockRoutes), their names to internal/api/inventory/operations.go and their path constants to internal/api/refusal/lockroutes.go. Check: go test ./internal/api/inventory/... ./internal/api/refusal/... -count=1

## 3. Definition of done

- [x] 3.1 Wire the routes with the unit:lock action and audit entries. Check: go test ./pkg/manuallock/... ./internal/... -run Lock -count=1
- [ ] 3.2 E2e step: BLOCKED by the real runner pool e2e steps that the dependency change delivers; until then the step drives the real OpenTofu CLI with a personal token on PUT /api/dustpan/v1/namespaces/{namespace}/lock. Check: make e2e step refuses a covered workspace and names holder and reason. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Extend docs/guides/manual-locks.md with namespace locks and dispatch. Add the e2e step TestManualLocksGuideCommands in the NEW file e2e/manual_locks_guide_test.go: it runs each command of docs/guides/manual-locks.md, in the order of the page, against the e2e test server with a personal access token (the page uses PUT /api/dustpan/v1/namespaces/{namespace}/lock), and fails on the first command that does not give the output that the page shows. The agent writes the step and does not boot a live server; CI runs it with `make e2e`. Confirmed by CI, not by the agent. Check: the CI e2e job passes TestManualLocksGuideCommands. `make docs-lint` passes on each edited guide.
- [ ] 3.4 The usability check is the step of task 3.3 (no e2e step of its own): it follows the page alone. Run the local checks (Makefile). Check: make check reports zero findings; the CI e2e job confirms task 3.3. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
