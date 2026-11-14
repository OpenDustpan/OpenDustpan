## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the new file pkg/store/postgres/restore_swap_claim_test.go with a failing PostgreSQL slow-swap test with an injected 60 s lock wait. Check: TestRestoreSwapKeepsClaimDuringSlowLock fails on main with claim_lost.
- [ ] 1.2 Add the new file pkg/store/sqlite/restore_swap_claim_test.go with a failing SQLite slow-swap test with an injected 60 s block. Check: TestRestoreSwapKeepsClaimDuringSlowBlock fails on main with claim_lost.
- [ ] 1.3 Add a failing test that a deadlocked swap records SQLSTATE 40P01 in pkg/store/postgres/restore_swap_conflict_internal_test.go:36. Check: TestRestoreSwapRecordsDeadlockAttempt fails on main.

## 2. Fix

- [ ] 2.1 Pre-extend the lease before LOCK TABLE in pkg/store/postgres/restore.go:391 (func keepControlTables). Check: TestRestoreSwapKeepsClaimDuringSlowLock passes.
- [ ] 2.2 Apply the same pre-extend in pkg/store/sqlite/restore.go:593 (func copyControlTables). Check: TestRestoreSwapKeepsClaimDuringSlowBlock passes.
- [ ] 2.3 Record the failed attempt and the lease deadline in the operation record in pkg/store/postgres/restore_operation.go. Check: `GET /api/server/restores/{id}` with an access token shows the attempt and the deadline.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the fix through the route ANY /api/server/restores with its authorization actions, using an access token. Check: The route test for ANY /api/server/restores with an access token passes.
- [ ] 4.2 Run an e2e restore step through ANY /api/server/restores with an access token against the real directory. Check: The e2e restore step through ANY /api/server/restores with an access token shows 1 swap run.
- [ ] 4.3 Update the restore guide page with the lost-claim and deadlock text in the same pull request. A person's step. Check: The restore guide page runs as written and each error names `GET /api/server/restores/{id}`.
- [ ] 4.4 Usability check of the restore path from the restore guide, including the lost-claim text. A person's step. Check: The Operator finishes a restore from the guide and each error names `GET /api/server/restores/{id}`.
- [ ] 4.5 Run make check with zero findings. Confirmed by: CI (make check). Check: make check reports zero findings.
