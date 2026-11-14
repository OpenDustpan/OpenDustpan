## 1. Failing tests first

- [ ] 1.1 Write failing table tests for each covered change by change kind. Check: `go test ./internal/principalsvc/ -run TestBreakGlassGuard -count=1` fails on main.
- [ ] 1.2 Write failing tests for restore refusal, busy answer, parallel disable, parallel setup finish and stale fresh step. Check: `go test ./internal/... -run TestBreakGlassRestoreAndRace -count=1` fails on main.
- [ ] 1.3 Write failing tests for rotation and the sweep audit record. Check: `go test ./internal/... -run TestBreakGlassRotate -count=1` fails on main.
- [ ] 1.4 Write failing tests for setup finish with zero, one and two accounts and the upgrade report. Check: `go test ./internal/... -run TestSetupNeedsTwoBreakGlass -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the count function and guard type with a conformance suite that both store adapters run. Check: `go test ./pkg/principal/... -count=1` passes on sqlite and postgres.
- [ ] 2.2 Call the guard in each covered write and in the restore. Check: `go test ./internal/... -run BreakGlass -count=1` passes.
- [ ] 2.3 Add the list, add and rotate routes with their actions. Check: `go test ./internal/api/... -run RouteActions -count=1` passes.
- [ ] 2.4 Update the shared e2e first-setup step to make two break-glass accounts with a passkey or a hardware key. Check: `make e2e` first setup passes for every existing step.
- [ ] 2.5 Update the guide for break-glass accounts only. Check: `make docs-lint` passes.

## 3. Definition of done

- [ ] 3.1 Add an e2e step for a refused disable, an atomic rotation, a refused restore and first setup with two accounts. Check: Real binary over TLS: the steps pass on sqlite and postgres.
- [ ] 3.2 Ship the docs page and run its commands as written. Check: The docs test runs each command.
- [ ] 3.3 Check the wiring, the route actions, the audit entries and the usability walk-through. Check: `make check` passes with zero findings.
