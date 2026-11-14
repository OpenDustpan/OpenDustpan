## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fixtures

- [ ] 1.1 In pkg/store/storetest/restore_operation.go:778 (func testRestoreOpEnter) reuse one built export and staged copy per hostile-body group, copy per case and vary only the approvers row, with a check that each varied row reaches strip and swap. Check: `go test -count=1 ./pkg/store/sqlite/ -run TestDevDataConformance -v` passes with unchanged assertions and lower wall time
- [ ] 1.2 In pkg/store/storetest and the sqlite and postgres package test setup files behind pkg/store/postgres/postgres_test.go:909 (TestDevDataConformance) give each store test a copy of one migrated template database with unique names, no sessions on the postgres template, checkpointed sqlite copy, parallel-safe copies, and fail-closed setup. Check: `go test -count=1 ./pkg/store/sqlite/ ./pkg/store/postgres/` passes without weakened assertions and with per-copy cleanup

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 In pkg/store/sqlite/wal_test.go:779 (TestWALGuardBusyBackoff) confirm timing-sensitive suites pass without flakes under load. Check: `go test -count=1 ./pkg/store/sqlite/ -run 'TestWALGuardBusyBackoff|TestJobConformance|TestDurableScheduleConformance'` passes
- [ ] 3.2 Run full store suites and lint for pkg/store/sqlite and pkg/store/postgres changes. Check: `make check` with zero findings
