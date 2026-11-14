## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test for the count of operations skipped by the bound in pkg/store/internal/jobstore/claim_skips_test.go (NEW). Check: TestClaimPollCountsBoundSkips fails on main

## 2. Fix

- [ ] 2.1 Correct the Route.IdempotencyKey comment in internal/api/registry/operations.go:14 and in the route files of the operations packages. Check: rg -n -F 'Route.IdempotencyKey' internal shows the fixed text
- [ ] 2.2 Count and log the operations skipped by the bound of 64 in pkg/store/internal/jobstore/claim_skips.go (NEW). Check: go test ./pkg/store/...

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wiring: the counter is reachable from the server metrics Check: The metrics route lists the counter
- [ ] 4.2 E2E step: BLOCKED by the missing name of the runner poll route and runner credential kind in the pack; the step is a unit test until the owner of the runner protocol names them Check: TestClaimPollCountsBoundSkips. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Docs page note and usability check from the docs page alone Check: make docs-lint. A person's step.
- [ ] 4.4 All checks pass Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
