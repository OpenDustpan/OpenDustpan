## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [x] 1.1 Write the failing conformance test that an expired state.write record is deleted, in the conformance case prune-expired-deletes-expired in pkg/gate/gatetest/run_records.go, with its suite entry in pkg/gate/gatetest/gatetest.go. Check: the case prune-expired-deletes-expired fails on main: it calls the NEW method PruneExpired of the Records port, which pkg/gate/store.go lacks.
- [x] 1.2 Write the test that an unexpired record and a record of another decision point stay, in the conformance case prune-expired-keeps-others in pkg/gate/gatetest/run_records.go. Check: the case prune-expired-keeps-others fails on main: it calls the NEW method PruneExpired of the Records port, which pkg/gate/store.go lacks.
- [x] 1.3 Write the test of the batch bound and the order by ID, in the conformance case prune-expired-batch-order in pkg/gate/gatetest/run_records.go, registered in pkg/gate/gatetest/gatetest.go. Check: the case prune-expired-batch-order fails on main: it calls the NEW method PruneExpired of the Records port, which pkg/gate/store.go lacks.
- [x] 1.4 Write the test that a new evaluation after a prune writes a record, in pkg/gate/statewrite_test.go next to TestEachEvaluationWritesOneRecord. Check: TestEvaluationAfterPruneRecords passes on main and guards the keep behavior.

## 2. Build

- [x] 2.1 Add pruneExpired to the Records port in pkg/gate/store.go and the pruneRequest constructor in NEW pkg/gate/prune.go. Check: `go test ./pkg/gate/... -count=1` passes.
- [x] 2.2 Implement the method in the shared store code internal/store/gatestore/tx.go and internal/store/gatestore/records.go, add the query in internal/store/sqlite/queries.sql and internal/store/postgres/queries.sql, wire the adapters in internal/store/sqlite/gate.go and internal/store/postgres/gate.go, and run the conformance suite. Check: `go test -timeout 60m ./pkg/store/... -run Prune` passes.

## 3. Side effects (computed from main)

- [x] 3.1 Confirm that no request flow changed, so the round-trip budgets stand: the change adds the port method PruneExpired with no route and no caller; the change that wires it keeps the budget task. Check: the round-trip tests in `internal/servertest/roundtrips_test.go` pass unchanged.

## 4. Definition of done

- [x] 4.1 Wiring: the method is a port method only. The follow-up change reaches it from the server. Say this on the docs page, NEW docs/guides/decision-record-retention.md. Check: `make docs-lint` passes.
- [ ] 4.2 Usability check from the docs page alone. BLOCKED by the follow-up retention-sweep-schedule-lease: no route reaches pruneExpired in this change, so no e2e step with the real tofu CLI can show a prune. The held-lock retry keeps its existing route test. Check: `make docs-lint` passes and the page names the follow-up.
- [ ] 4.3 Run the full check. Check: `make check` reports zero findings.
