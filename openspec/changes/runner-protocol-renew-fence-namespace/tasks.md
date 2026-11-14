## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the conformance test for SessionRenew with a fence of another namespace in pkg/runner/runnertest. Check: TestSessionRenewRefusesFenceOfAnotherNamespace fails on main
- [ ] 1.2 Write the conformance test for RunnerRenew with a fence of another namespace in pkg/runner/runnertest. Check: TestRunnerRenewRefusesFenceOfAnotherNamespace fails on main
- [ ] 1.3 Write the handler tests for a key of another pool and a claim of another job on a session route in the NEW file internal/api/runnerapi/session_claim_test.go. Check: TestSessionRouteRefusesKeyOfAnotherPool and TestSessionRouteRefusesClaimOfAnotherJob fail on main
- [ ] 1.4 Write the coverage test for the runner routes against e2e/coverage.go:42. Check: TestRunnerRoutesAreInServedRoutes fails on main

## 2. Fix

- [ ] 2.1 Add the fence-namespace refusal to the sqlite adapter in pkg/store/sqlite/runner.go:1306 for SessionRenew and pkg/store/sqlite/runner.go:1223 for RunnerRenew. Check: pkg/runner/runnertest conformance passes on sqlite
- [ ] 2.2 Add the fence-namespace refusal to the postgres adapter in pkg/store/postgres/runner.go:1272 for SessionRenew and pkg/store/postgres/runner.go:1192 for RunnerRenew. Check: pkg/runner/runnertest conformance passes on postgres
- [ ] 2.3 Evaluate ActionRunnerClaim on the stored pool at the point of use for session and runner-key handlers in internal/api/runnerapi/routes.go:126 and internal/api/runnerapi/session.go:446, under the existing route ANY /runner/v1. Check: handler claim tests pass
- [ ] 2.4 Add the runner coverage rows to the existing file e2e/coverage.go:42. Check: the coverage test passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step on the existing route ANY /runner/v1 with a runner key credential that passes the ActionRunnerClaim gate, for a renew with a fence of another namespace. Confirmed by: CI (make e2e). Check: make e2e passes with a runner key on the existing route ANY /runner/v1
- [ ] 4.2 Add the NEW file docs/reference/runner-renew-errors.md with the error and the recovery command, and check the user path from the page alone. A person's step. Check: make docs-lint passes and each error names one command
- [ ] 4.3 Run the full check. Confirmed by: CI (make check). Check: make check passes
