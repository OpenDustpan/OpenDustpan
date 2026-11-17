## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Write the failing conformance test that an expired state.write record is deleted, in NEW pkg/gate/gatetest/prune_test.go. Add the suite entry in pkg/gate/gatetest/gatetest.go and the record helper in the existing pkg/gate/gatetest/run_records.go. Check: TestPruneExpiredDeletesExpired fails on main: it calls the NEW method PruneExpired of the Records port, which pkg/gate/store.go lacks.
- [x] 1.2 Write the test that an unexpired record and a record of another decision point stay, in NEW pkg/gate/gatetest/prune_test.go. It uses the helper in the existing pkg/gate/gatetest/run_records.go. Check: TestPruneExpiredKeepsOthers fails on main: it calls the NEW method PruneExpired of the Records port, which pkg/gate/store.go lacks.
- [x] 1.3 Write the test of the batch bound and the order by ID, in NEW pkg/gate/gatetest/prune_test.go. It registers in the existing pkg/gate/gatetest/gatetest.go. Check: TestPruneExpiredBatchOrder fails on main: it calls the NEW method PruneExpired of the Records port, which pkg/gate/store.go lacks.
- [x] 1.4 Write the test that a new evaluation after a prune writes a record, in pkg/gate/statewrite_test.go next to TestEachEvaluationWritesOneRecord. Check: TestEvaluationAfterPruneRecords passes on main and guards the keep behavior.

## 2. Build

- [x] 2.1 Add pruneExpired to the Records port in pkg/gate/store.go and the pruneRequest constructor in NEW pkg/gate/prune.go. Check: `go test ./pkg/gate/... -count=1` passes.
- [x] 2.2 Implement the method in the shared store code pkg/store/internal/gatestore/tx.go and pkg/store/internal/gatestore/records.go, add the query in pkg/store/sqlite/queries.sql and pkg/store/postgres/queries.sql, wire the adapters in pkg/store/sqlite/gate.go and pkg/store/postgres/gate.go, and run the conformance suite. Check: `go test -timeout 60m ./pkg/store/... -run Prune` passes.

## 3. Side effects (computed from main)

- [x] 3.1 Confirm that no request flow changed, so the round-trip budgets stand: the change adds the port method PruneExpired with no route and no caller; the change that wires it keeps the budget task. Check: the round-trip tests in `internal/servertest/roundtrips_test.go` pass unchanged. Confirmed by: CI (the round-trip tests pass unchanged).

## 4. Definition of done

- [x] 4.1 Wiring: the method is a port method only. The follow-up change reaches it from the server. Say this on the docs page, NEW docs/guides/decision-record-retention.md. Check: `make docs-lint` passes.
- [x] 4.2 Usability check from the docs page alone. BLOCKED by the follow-up retention-sweep-schedule-lease: no route reaches pruneExpired in this change, so no e2e step with the real tofu CLI can show a prune. The held-lock retry keeps its existing route test. Check: `make docs-lint` passes and the page names the follow-up. A person's step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
