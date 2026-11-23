## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing store test for state version rollback on mid-commit outage (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/state_commit_test.go. Check: NEW test `TestStateCommitRollsBackOnOutage` fails on main, passes after the fix.
- [ ] 1.2 Add failing store test for lock acquire rollback on mid-commit outage (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/lock_commit_test.go. Check: NEW test `TestLockAcquireRollsBackOnOutage` fails on main, passes after the fix.
- [ ] 1.3 Add failing store test for lock release rollback on mid-commit outage (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/unlock_commit_test.go. Check: NEW test `TestUnlockRollsBackOnOutage` fails on main, passes after the fix.
- [ ] 1.4 Add failing hostile test that sends a stale fence directly to the store layer (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/fence_test.go. Check: NEW test `TestFenceRejectsStaleWrite` fails on main, passes after the fix.
- [ ] 1.5 Add failing test that a committed lock stays held after instance loss and that force-unlock releases it (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/lock_takeover_test.go. The test also runs two concurrent lock acquires and two concurrent state version writes with different fences, and checks that one wins. Check: NEW test `TestCommittedLockStaysHeld` fails on main, passes after the fix.
- [ ] 1.6 Add failing test for idempotent retry after a lost reply (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/commit_retry_test.go. Check: NEW test `TestRetryWithSameIdentityMakesNoDuplicate` fails on main, passes after the fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Confirm that no request flow changed, so the round-trip budgets stand. Check:

## 3. Definition of done

- [ ] 3.1 Wrap state version commit and current pointer in one fenced transaction. The task adds the NEW top-level test `TestStateCommit` in a `_test.go` file of `internal/store/postgres`, so that the Check runs it. Check: `go test -race ./internal/store/postgres/ -run TestStateCommit -timeout 60m`
- [ ] 3.2 Wrap lock acquire and release in one fenced transaction with holder identity. The task adds the NEW top-level tests `TestLockAcquire`, `TestUnlock` in a `_test.go` file of `internal/store/postgres`, so that the Check runs them. Check: `go test -race ./internal/store/postgres/ -run TestLockAcquire|TestUnlock -timeout 60m`
- [ ] 3.3 Wire the atomic paths through the existing TFE routes with existing gates. Check: the CI job `e2e` is green on the pull request. Its OpenTofu steps run `tofu apply` with the real CLI on each store, PostgreSQL too. The routes are POST /api/v2/workspaces/{id}/actions/lock, POST /api/v2/workspaces/{id}/actions/unlock and POST /api/v2/workspaces/{id}/state-versions, with credential: access token. The test `TestHealthReadyOK` covers GET /health/ready, with credential: none, public route. Confirmed by: CI.
- [ ] 3.4 Document the retry behavior after a failed write in NEW file docs/guides/database-outage-recovery.md. Check: the docs page for outage recovery states the atomic commit behavior. `make docs-lint` passes on each edited guide.
- [ ] 3.5 Pass the full check suite. Check: `make check` with zero findings. Confirmed by: CI
