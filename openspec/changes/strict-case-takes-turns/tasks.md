## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a failing test in the NEW file internal/api/tfe/strict_case_test.go that a data source job ends as blocked while another apply runs and starts again at the release (NEW test TestStrictDataSourceWaits). Check: NEW test: `go test ./internal/api/tfe -run TestStrictDataSourceWaits` fails on main
- [ ] 1.2 Write a failing test in the NEW file internal/api/tfe/strict_case_test.go that the history names the case (NEW test TestStrictHistoryNamesCase). Check: NEW test: `go test ./internal/api/tfe -run TestStrictHistoryNamesCase` fails on main

## 2. Implementation

- [ ] 2.1 Add the fail-closed check, the blocked status and the start again at the release in the NEW file internal/api/tfe/strict_gate.go (NEW type strictGate, NEW func newStrictGate). Check: NEW test: `go test ./internal/api/tfe -run TestStrictDataSourceWaits` passes

## 3. Definition of done

- [ ] 3.1 BLOCKED by the missing TFE-compatible state route and job token credential kind of ADR 368 and ADR 369, which are not on main. Run the e2e step in e2e/e2e_test.go when they are on main. Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Add a docs page for the data source case. Check: add the new file docs/guides/strict-case-takes-turns.md. `make docs-lint` passes on each edited guide.
- [ ] 3.3 Run the usability check from the docs page. Check: each error names `dustpan job show <job-id>`. A person's step.
- [ ] 3.4 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
