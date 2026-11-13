## 1. Failing tests first

- [ ] 1.1 Write failing table tests for disable, delete, sweep, grant revoke, credential revoke, passkey removal, expiry change, policy block and IP allowlist change, each by change kind. Check: `go test ./internal/principalsvc/ -run TestBreakGlassGuard -count=1` fails on main.
- [ ] 1.2 Write failing tests for the restore refusal (self-hosted and hosted namespace), the lock timeout answer, the parallel disable, the parallel setup finish, the retry after busy, the deadlock with restore and the stale fresh step. Check: `go test ./internal/... -run TestBreakGlassRestoreAndRace -count=1` fails on main.
- [ ] 1.3 Write failing tests for rotation success, a failed insert, removal of the last credential, a sign-in during a lock wait and the sweep audit record. Check: `go test ./internal/... -run TestBreakGlassRotate -count=1` fails on main.
- [ ] 1.4 Write failing tests for setup finish with zero, one and two accounts, a hardware-key account and the upgrade report. Check: `go test ./internal/setupsvc/ -run TestSetupNeedsTwoBreakGlass -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the count function and the guard type to pkg/principal with a conformance suite that both store adapters run. Check: `go test ./pkg/principal/... -count=1` passes on sqlite and postgres.
- [ ] 2.2 Call the guard in the transaction of each covered write and in the restore. Check: `go test ./internal/... -run BreakGlass -count=1` passes.
- [ ] 2.3 Add the rotation route with its action and fresh-authentication check. Check: `go test ./internal/... -run BreakGlassRotate -count=1` passes.
- [ ] 2.4 Gate setup finish and add the report and add routes. Check: `go test ./internal/... -run SetupBreakGlass -count=1` passes.
- [ ] 2.5 Update the guide for break-glass accounts: setup, guard, per-use review, host recovery. Check: `make docs-lint` passes with no ADR, RFC, issue or pull request number.

## 3. Definition of done

- [ ] 3.1 Add an e2e step for a refused disable, an atomic rotation and a refused restore. Check: Real binary over TLS: disable of the last account gives 409 and the new credential signs in.
- [ ] 3.2 Add an e2e step for first setup with two accounts. Check: Real binary over TLS: setup finishes only with two accounts.
- [ ] 3.3 Ship the docs page and run its commands as written. Check: The docs test runs each command.
- [ ] 3.4 Check the wiring, the route actions, the audit entries and the usability walk-through. Check: `make check` passes with zero findings.
