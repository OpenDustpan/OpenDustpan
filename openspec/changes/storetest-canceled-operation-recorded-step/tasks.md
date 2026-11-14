## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Conformance case

- [ ] 1.1 Find the existing Store port calls that request a cancel and record a step, and the way the suite runs on PostgreSQL. Write the failing case in NEW file `pkg/store/storetest/canceled_step_test.go`. Check: `TestCanceledOperationWithRecordedStepReturnsCanceled` covers the same-step retry and the differing-step fence on the RecordStep path.
- [ ] 1.2 Run the case on SQLite and on PostgreSQL. Swap the order of the checks in `pkg/store/postgres/durable.go:345 (func checkStepRow)` and confirm that the case fails, then revert. Check: `go test ./pkg/store/sqlite/ -run TestCanceledOperationWithRecordedStepReturnsCanceled -count=1` passes, and the PostgreSQL store suite passes on the correct order.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Run the full store suites on both dialects and make check with zero findings. Check: `go test ./pkg/store/sqlite/... -count=1` and `make check` report zero findings. Confirmed by: CI (make check, store suites on SQLite and PostgreSQL).
