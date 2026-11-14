## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing restore replay test in NEW file pkg/backup/restore_revocation_test.go. Check: NEW test TestReplayRevocationAfterRestore in NEW file pkg/backup/restore_revocation_test.go fails on main.
- [ ] 1.2 Write the failing control-table coverage test in NEW file pkg/store/storetest/revocation_tombstone_test.go, covering pkg/store/postgres/restore.go:391 (func keepControlTables) and pkg/store/sqlite/restore.go:593 (func copyControlTables). Check: NEW test TestRevocationTombstoneIsControlTable in NEW file pkg/store/storetest/revocation_tombstone_test.go fails on main.

## 2. Work

- [ ] 2.1 Add the NEW tombstone table by an in-place edit of the one baseline migration file of each dialect under pkg/store/sqlite/migrations/ and pkg/store/postgres/migrations/. Check: TestRevocationTombstoneIsControlTable in pkg/store/storetest/revocation_tombstone_test.go passes.
- [ ] 2.2 Write a tombstone in each live revocation path in the same transaction, starting from pkg/maintenance/revocation_test.go:23 (TestRevocationValidateKinds). Check: Extended pkg/maintenance/revocation_test.go:23 TestRevocationValidateKinds passes.
- [ ] 2.3 Add the fenced ReplayRevocations step after the swap in pkg/backup/restore_operation.go:676 (func EncodeRestoreSwap). Check: TestReplayRevocationAfterRestore in pkg/backup/restore_revocation_test.go passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire replay into the restore route ANY /api/server/restores with its authorization actions. Check: Route ANY /api/server/restores runs the fenced replay step.
- [ ] 4.2 Run an e2e restore with a revoked access token through ANY /api/server/restores with an access token. Check: e2e step calls ANY /api/server/restores with an access token: the revoked token stays refused after restore.
- [ ] 4.3 Write the docs page for restore revocations. Check: The docs page commands run as written.
- [ ] 4.4 Check usability of the persona path. Check: The persona completes the path from the docs page alone and each error names the one command.
- [ ] 4.5 Run the full check. Check: `make check` with zero findings.
