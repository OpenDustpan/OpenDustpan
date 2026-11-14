## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing construction test for a store without MinimumChecker in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW test `TestNewServerRefusesStoreWithoutMinimumChecker` in NEW file pkg/dustpan/minimum_checker_test.go fails on main, passes after fix.
- [ ] 1.2 Add failing construction test for a decorating wrapper that hides MinimumChecker in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW test `TestNewServerRefusesHidingWrapper` in NEW file pkg/dustpan/minimum_checker_test.go fails on main, passes after fix.
- [ ] 1.3 Add failing construction tests for a nil maintenance service and for a store without store-check restore rows in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW tests `TestNewServerRefusesNilMaintenanceService` and `TestNewServerRefusesStoreWithoutRestoreRows` fail on main, pass after fix.
- [ ] 1.4 Add failing construction test for a store without purge-record support for the backlog gauge in NEW file pkg/dustpan/minimum_checker_test.go. Check: NEW test `TestNewServerRefusesStoreWithoutPurgeRecords` fails on main, passes after fix.
- [ ] 1.5 Add failing storetest conformance cases for CheckBinaryMinimum accept, refuse and read failure in NEW file pkg/store/storetest/minimum_checker_test.go. Check: NEW tests `TestMinimumCheckerAccept`, `TestMinimumCheckerRefuse` and `TestMinimumCheckerReadFailure` in NEW file pkg/store/storetest/minimum_checker_test.go fail on main, pass after fix on sqlite and postgres suite runs.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the MinimumChecker assertion into NewServer and the same refusal into the store-check and purge-gauge setup paths. Check: `go test ./pkg/dustpan ./pkg/store/... -count=1` passes.
- [ ] 3.2 Update the embedder docs page for the required store interfaces, run the usability check from the docs page alone, and run `make check`. Check: `make check` passes with zero findings. Confirmed by: CI (make check). No route and no e2e step: construction-time change only. Wiring is NewServer refusal. Docs page is the embedder store-interface page. Usability check is the embedder startup path from the docs page alone.
