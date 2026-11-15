## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests that prove main's behavior

A test that passes on main from the start counts as done: main already has the behavior.

- [ ] 1.1 Add the snapshot test TestDeletedUnitFactsSnapshot (SQLite only: the change is the read transaction of the SQLite store) in the NEW file pkg/store/sqlite/deleted_unit_facts_snapshot_test.go: a concurrent commit during GetDeletedUnitFacts (pkg/store/sqlite/inventory.go:573) does not change the facts of the call. Check: `go test -timeout 60m ./pkg/store/sqlite/ -run TestDeletedUnitFactsSnapshot -count=1` passes.
- [ ] 1.2 Add TestDeletedUnitFactsReadFailure (SQLite only) in pkg/store/sqlite/deleted_unit_facts_snapshot_test.go: a failed read-only transaction returns no partial facts. Check: `go test -timeout 60m ./pkg/store/sqlite/ -run TestDeletedUnitFactsReadFailure -count=1` passes.
- [ ] 1.3 Add TestDeletedUnitFactsSelectAndRollbackFailure (SQLite only) in the NEW file pkg/store/sqlite/deleted_unit_facts_rollback_internal_test.go (package sqlite, because the test needs an unexported helper): when a select and the rollback both fail, the call closes the transaction and returns the select error (inReadTx, pkg/store/sqlite/sqlite.go:1447). Check: `go test -timeout 60m ./pkg/store/sqlite/ -run TestDeletedUnitFactsSelectAndRollbackFailure -count=1` passes.

## 2. Already on main (no work)

- [x] 2.1 GetDeletedUnitFacts reads in one read-only transaction: pkg/store/sqlite/inventory.go:573 through inReadTx (pkg/store/sqlite/sqlite.go:1447). Check: the code on main.
- [x] 2.2 The start WARN line "state.write gates: off" with its reason and the audit entry, and the INFO line "state.write gates: on": pkg/dustpan/stategates_start.go:58-67, proven by TestStateWriteGatesOffIsRecordedAtStart (pkg/dustpan/stategates_start_internal_test.go:64). Check: the test on main.

## 3. Definition of done

- [ ] 3.1 No route, authorization or docs change. The production change is limited to `pkg/store/sqlite/sqlite.go` and `pkg/store/sqlite/inventory.go`, as the design describes. Check: the route test passes and no file under docs/ changes.
- [ ] 3.2 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
