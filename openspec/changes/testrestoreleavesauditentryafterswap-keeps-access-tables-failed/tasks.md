## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Diagnose and fix

- [ ] 1.1 Reproduce both flakes under load and record the cause: the colliding name, the LastSeenAt and clock offset of each join, and the restore row visibility. If the cause is in production code, stop and file a new change. Check: `go test -race -count=50 -run TestRestoreLeavesAuditEntryAfterSwap ./pkg/store/postgres/` with `DUSTPAN_TEST_POSTGRES_URL` set while `go test -race ./...` runs. No route: the test calls the store only.
- [ ] 1.2 Isolate the restore test with a unique restore name per run in pkg/store/postgres/postgres_test.go. Check: `go test -race -count=50 -run TestRestoreLeavesAuditEntryAfterSwap ./pkg/store/postgres/` under full-suite load. No route: the test calls the store only.
- [ ] 1.3 Isolate the join test with a unique runner name per run and a fixed clock per subtest in internal/api/runnerapi/refusal_test.go. Check: `go test -race -count=50 -run TestJoinIsRefused ./internal/api/runnerapi/` under full-suite load. No route: the test uses only its existing test server route.
- [ ] 1.4 Break the guarded behavior in a scratch copy of pkg/runner/member.go and pkg/store/postgres/restore_access.go, then restore it. Check: Both tests fail with the break and pass without it. No route: the test calls the store only.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run the full check and confirm no new failure. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
