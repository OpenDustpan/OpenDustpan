## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing table tests for each covered change by change kind, with the hosted admin grant revoke and the audit entry of each refusal. Check: `go test ./internal/principalsvc/ -run TestBreakGlassGuard -count=1` (NEW test) fails on main.
- [ ] 1.2 Write failing tests for restore refusal, busy answer, parallel disable, parallel setup finish and stale fresh step. Check: `go test ./internal/... -run TestBreakGlassRestoreAndRace -count=1` (NEW test) fails on main.
- [ ] 1.3 Write failing tests for rotation, including unknown id and parallel rotation, and the sweep audit record. Check: `go test ./internal/... -run TestBreakGlassRotate -count=1` (NEW test) fails on main.
- [ ] 1.4 Write failing tests for setup finish with zero, one and two accounts, add at zero accounts, and the upgrade report with an expired account. Check: `go test ./internal/... -run TestSetupNeedsTwoBreakGlass -count=1` (NEW test) fails on main.

## 2. Build

- [ ] 2.1 Add the count function and guard type with a conformance suite that both store adapters run. Check: `go test ./pkg/principal/... -count=1` passes on sqlite and postgres.
- [ ] 2.2 Call the guard in each covered write and in the restore. Add the disable notice and the lock timeout setting. Check: `go test ./internal/... -run BreakGlass -count=1` passes.
- [ ] 2.3 Add the list, add and rotate routes with their actions. Check: `go test ./internal/api/... -run RouteActions -count=1` passes.
- [ ] 2.4 Update the shared e2e first-setup step. BLOCKED by passkey sign-in for local users, which is not on main, and by the new add route of this change. Check: `make e2e` first setup passes for every existing step.
- [ ] 2.5 Update the guide for break-glass accounts. Check: `make docs-lint` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step with the real CLI over TLS. It calls the rotate route and the disable route with an access token of a site administrator. BLOCKED by passkey sign-in for local users, which is not on main. Check: The steps pass on sqlite and postgres: a refused disable, an atomic rotation, a refused restore and first setup with two accounts.
- [ ] 4.2 Ship the docs page and run its commands as written. Check: The docs test runs each command.
- [ ] 4.3 Check the wiring, the route actions, the audit entries and the usability walk-through. Check: `make check` passes with zero findings.
