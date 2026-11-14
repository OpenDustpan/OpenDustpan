## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write TestNewStoreTicketsRefusesNil and TestPruneKeepsLiveRows in pkg/session and pkg/session/sessiontest. Check: `go test -race ./pkg/session/...` fails on main for each named test.
- [ ] 1.2 Write TestPruneRunLogsCount, TestPruneRunLogsFailure and TestPruneCounterSumsCounts in cmd/opendustpan. Check: `go test -race ./cmd/opendustpan/...` fails on main.
- [ ] 1.3 Extend the restore case in pkg/store/storetest/ticket_restore.go for the SQLite and PostgreSQL adapters under pkg/store: the first prune deletes expired rows and live rows stay. Check: `go test -timeout 60m ./pkg/store/... -run TicketRestore` fails on main.

## 2. Build

- [ ] 2.1 Wire NewStoreTickets and the NEW prune operation in cmd/opendustpan, with the log record. Check: TestPruneRunLogsCount passes.
- [ ] 2.2 Add the NEW counter in internal/metrics. Check: TestPruneCounterSumsCounts passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 The CAS replay e2e step is BLOCKED by subject-sources-can-change-cas, which wires internal/casauth to a sign-in route. No route and no credential exist to call today. The step stays unticked until the blocker merges. Check: The e2e step stays unticked until the blocker merges. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write docs/guides/cas-sign-in.md with the 1-hour record, the hourly prune and the metric. The prune adds no route and no authorization action. Check: `make docs-lint` reports nothing.
- [ ] 4.3 Check that an Operator finds the prune record and the metric from the docs page alone. Check: The Operator finds the log record and the counter. Confirmed by: CI (an e2e step in the NEW file e2e/session_cas_ticket_store_has_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.4 Run `make check` for the whole change. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
