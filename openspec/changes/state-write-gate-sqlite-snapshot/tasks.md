## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing snapshot test with a concurrent commit during GetDeletedUnitFacts. Check: NEW test TestDeletedUnitFactsSnapshot in pkg/store/sqlite fails on main; scenario Concurrent commit during fact read
- [ ] 1.2 Add failing test for a failed read-only transaction with no partial facts in NEW file pkg/store/sqlite/deleted_unit_facts_snapshot_test.go. Check: NEW test TestDeletedUnitFactsReadFailure fails on main; scenario Failed read transaction
- [ ] 1.3 Add failing test for the select-plus-rollback first-error rule in NEW file pkg/store/sqlite/deleted_unit_facts_snapshot_test.go. Check: NEW test TestDeletedUnitFactsSelectAndRollbackFailure fails on main; scenario Select and rollback both fail
- [ ] 1.4 Add failing test for the one fixed start-up log line in NEW file internal/servertest/nogate_log_test.go. Check: BLOCKED by the missing route and credential kind that reads deleted-unit facts; NEW test TestNoGateServiceStartLog fails on main; scenarios Server starts with no gate service and Repeat start with no gate service
- [ ] 1.5 Add failing test that start continues when the log write fails in NEW file internal/servertest/nogate_log_test.go. Check: BLOCKED by the missing route and credential kind that reads deleted-unit facts; NEW test TestNoGateServiceLogWriteFailure fails on main; scenario Log write failure at start
- [ ] 1.6 Add failing test for the explicit rollback branch in NEW file pkg/store/sqlite/deleted_unit_facts_snapshot_test.go. Check: NEW test TestFactReadRollbackBranch fails on main; scenario Rollback then close

## 2. Fix

- [ ] 2.1 Move GetDeletedUnitFacts reads into one read-only transaction with deferred close in pkg/store/sqlite/inventory.go:573 (GetDeletedUnitFacts). Check: `go test ./pkg/store/sqlite/ -run TestDeletedUnitFactsSnapshot -count=1` passes
- [ ] 2.2 Remove the blank-assign rollback pattern on the touched path only. Check: `rg -n -F '_ = rollbackErr' pkg/store/sqlite` shows no hit in the touched function
- [ ] 2.3 The start-up log line for an absent gate service is on main: recordStateWriteGates in pkg/dustpan/stategates_start.go:58-67 writes the WARN line `state.write gates: off` with the reason. Keep it, and add its test TestNoGateServiceStartLog in NEW file internal/servertest/nogate_log_test.go (one WARN line without a gate service, the INFO line `state.write gates: on` and no WARN line with one). Check: BLOCKED by the missing route and credential kind that reads deleted-unit facts; TestNoGateServiceStartLog passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Confirm the wiring with a server start with and without a gate service (no e2e step: the start path has no route and no credential), in the test of task 2.3 (internal/servertest/nogate_log_test.go, NEW), against recordStateWriteGates (pkg/dustpan/stategates_start.go:58-67). Check: without a gate service the start log holds one WARN line `state.write gates: off` with its reason and the installation scope holds one audit entry of the gates being off; with a gate service the log holds the INFO line `state.write gates: on`, no WARN line and no such audit entry.
- [ ] 4.2 Confirm no route or authorization change is needed. Check: Route table unchanged; the route test passes
- [ ] 4.3 Record the e2e step: BLOCKED by the missing route, because the pack names no existing route and credential kind that reads deleted-unit facts alone. Check: BLOCKED by a route and credential kind for the fact read; TestDeletedUnitFactsSnapshot and the storetest gate suite cover it. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.4 Record why no docs page applies. Check: Not applicable: only an operational log line
- [ ] 4.5 Run the operator start-up check. Check: Operator reads the log line with no new command
- [ ] 4.6 Run format, vet, lint and the full store test suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
