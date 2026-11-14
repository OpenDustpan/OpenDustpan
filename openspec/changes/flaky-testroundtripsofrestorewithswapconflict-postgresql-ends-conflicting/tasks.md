## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Write the failing classifier test: in internal/servertest/roundtrips_conflict_test.go:48 add a victim classifier with injected 40P01 on each handle. BLOCKED by no product route: test-only servertest path. Check: go test ./internal/servertest/ -run TestVictimClassifier -count=1 fails on main and passes after the fix.
- [ ] 1.2 In internal/servertest/roundtrips_conflict_test.go:48 add the bounded retry of 10 with fresh connections, rollback and close of both handles, retrying only wrong-victim 40P01 and failing at once on any other error. BLOCKED by no product route: test-only servertest path. Check: Scenario wrong-victim retry: TestVictimRetryRepeatsOnWrongVictim passes with an injected wrong victim then the right victim.
- [ ] 1.3 In internal/servertest/roundtrips_conflict_test.go (NEW test TestVictimRetryExhausted) fail with the attempt count and last victim identity when all 10 attempts end with the wrong victim. BLOCKED by no product route: test-only servertest path. Check: Scenario bound exhausted: TestVictimRetryExhausted sees a failure naming count 10 and the victim identity.
- [ ] 1.4 In internal/servertest/roundtrips_conflict_test.go (NEW test TestVictimRetryFailsFastOnOtherError) fail at once on a non-40P01 error. BLOCKED by no product route: test-only servertest path. Check: Scenario fail-fast: the test sees one attempt only and the error.

## 2. Done

- [ ] 2.1 Definition of done: repeat runs on PostgreSQL pass, SQLite paths unchanged, no product route or docs change. BLOCKED by no product route: test-only servertest path with no API route, gate, or credential. Confirmed by: CI (postgres restore conflict job) Check: go test -race ./internal/servertest/ -run TestRoundTripsOfRestoreWithSwapConflict -count=50 passes against PostgreSQL, then make check reports zero findings.
