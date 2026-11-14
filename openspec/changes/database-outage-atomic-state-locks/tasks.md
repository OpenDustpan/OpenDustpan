## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing store test for state version rollback on mid-commit outage in NEW file pkg/store/postgres/state_commit_test.go. Check: NEW test `TestStateCommitRollsBackOnOutage` fails on main, passes after the fix.
- [ ] 1.2 Add failing store test for lock acquire rollback on mid-commit outage in NEW file pkg/store/postgres/lock_commit_test.go. Check: NEW test `TestLockAcquireRollsBackOnOutage` fails on main, passes after the fix.
- [ ] 1.3 Add failing store test for lock release rollback on mid-commit outage in NEW file pkg/store/postgres/unlock_commit_test.go. Check: NEW test `TestUnlockRollsBackOnOutage` fails on main, passes after the fix.
- [ ] 1.4 Add failing hostile test that sends a stale fence directly to the store layer in NEW file pkg/store/postgres/fence_test.go. Check: NEW test `TestFenceRejectsStaleWrite` fails on main, passes after the fix.
- [ ] 1.5 Add failing test for fenced takeover of a committed lock in NEW file pkg/store/postgres/lock_takeover_test.go. Check: NEW test `TestCommittedLockSurvivesTakeover` fails on main, passes after the fix.
- [ ] 1.6 Add failing test for idempotent retry after a lost reply in NEW file pkg/store/postgres/commit_retry_test.go. Check: NEW test `TestRetryWithSameIdentityMakesNoDuplicate` fails on main, passes after the fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wrap state version commit and current pointer in one fenced transaction. Check: `go test -race ./pkg/store/postgres/ -run TestStateCommit -timeout 60m`
- [ ] 3.2 Wrap lock acquire and release in one fenced transaction with holder identity. Check: `go test -race ./pkg/store/postgres/ -run TestLockAcquire|TestUnlock -timeout 60m`
- [ ] 3.3 Wire the atomic paths through the existing TFE routes with existing gates. Check: GET /health/ready plus `tofu apply` against the real CLI on PostgreSQL with an access token.
- [ ] 3.4 Document the retry behavior after a failed write. Check: Docs page for outage recovery states the atomic commit behavior.
- [ ] 3.5 Pass the full check suite. Check: `make check` with zero findings.
