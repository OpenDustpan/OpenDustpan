## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add store test for state version rollback on mid-commit outage (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/state_commit_test.go. Check: NEW test `TestStateCommitRollsBackOnOutage` is a regression guard: it passes on main and on the change.
- [ ] 1.2 Add store test for lock acquire rollback on mid-commit outage (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/lock_commit_test.go. Check: NEW test `TestLockAcquireRollsBackOnOutage` is a regression guard: it passes on main and on the change.
- [ ] 1.3 Add store test for lock release rollback on mid-commit outage (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/unlock_commit_test.go. Check: NEW test `TestUnlockRollsBackOnOutage` is a regression guard: it passes on main and on the change.
- [ ] 1.4 Add hostile test that sends a stale fence directly to the store layer (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/fence_test.go. Check: NEW test `TestFenceRejectsStaleWrite` is a regression guard: it passes on main and on the change.
- [ ] 1.5 Add test that a committed lock stays held after instance loss and that force-unlock releases it (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/lock_takeover_test.go. The test also runs two concurrent lock acquires and two concurrent state version writes with different fences, and checks that one wins. Check: NEW test `TestCommittedLockStaysHeld` is a regression guard: it passes on main and on the change.
- [ ] 1.6 Add test for idempotent retry after a lost reply (PostgreSQL only: a mid-commit outage of the database server; SQLite runs in the process) in NEW file internal/store/postgres/commit_retry_test.go. The test drives the workspace service over the PostgreSQL store with a retry of the same upload token: the service returns the committed result and the store holds no second version. The store keeps the port contract: a finalize of a version that is not pending returns `ErrUploadDenied`, as the SQLite store does, so the PostgreSQL store has no idempotent finalize of its own. The same-token retry stays in the service (`Service.confirmRetry`). Check: NEW test `TestRetryWithSameIdentityMakesNoDuplicate` is a regression guard: it passes on main and on the change.

## 2. Side effects (computed from main)

- [ ] 2.1 Confirm that no request flow changed, so the round-trip budgets stand. Check:

## 3. Definition of done

- [ ] 3.1 Wrap state version commit and current pointer in one fenced transaction. The task adds the NEW top-level test `TestStateCommit` in a `_test.go` file of `internal/store/postgres`, so that the Check runs it. Check: `go test -race ./internal/store/postgres/ -run TestStateCommit -timeout 60m`
- [ ] 3.2 Wrap lock acquire and release in one fenced transaction with holder identity. The task adds the NEW top-level tests `TestLockAcquire`, `TestUnlock` in a `_test.go` file of `internal/store/postgres`, so that the Check runs them. Check: `go test -race ./internal/store/postgres/ -run TestLockAcquire|TestUnlock -timeout 60m`
- [ ] 3.3 Wire the atomic paths through the existing TFE routes with existing gates. Check: the CI job `e2e` is green on the pull request. Its OpenTofu steps run `tofu apply` with the real CLI on each store, PostgreSQL too. The routes are POST /api/v2/workspaces/{id}/actions/lock, POST /api/v2/workspaces/{id}/actions/unlock and POST /api/v2/workspaces/{id}/state-versions, with credential: access token. The test `TestHealthReadyOK` covers GET /health/ready, with credential: none, public route. Confirmed by: CI.
- [x] 3.4 Document the retry behavior after a failed write in NEW file docs/guides/database-outage-recovery.md. Check: the docs page for outage recovery states the atomic commit behavior. `make docs-lint` passes on each edited guide.
- [ ] 3.5 Pass the full check suite. Check: `make check` with zero findings. Confirmed by: CI
