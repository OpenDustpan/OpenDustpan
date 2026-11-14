## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a failing test that a data source job ends as blocked while another apply runs and starts again at the release (NEW test TestStrictDataSourceWaits). Check: NEW test: `go test ./internal/api/tfe -run TestStrictDataSourceWaits` fails on main
- [ ] 1.2 Write a failing test that the history names the case (NEW test TestStrictHistoryNamesCase). Check: NEW test: `go test ./internal/api/tfe -run TestStrictHistoryNamesCase` fails on main

## 2. Implementation

- [ ] 2.1 Add the fail-closed check, the blocked status and the start again at the release. Check: NEW test: `go test ./internal/api/tfe -run TestStrictDataSourceWaits` passes

## 3. Definition of done

- [ ] 3.1 Run the e2e step with the TFE-compatible state routes and a job token. Check: BLOCKED by RFC 129 point 4; then `make e2e` passes
- [ ] 3.2 Add a docs page for the data source case. Check: add the new file docs/guides/strict-case-takes-turns.md
- [ ] 3.3 Run the usability check from the docs page. Check: each error names `dustpan job show <job-id>`
- [ ] 3.4 Run the full check. Check: `make check` reports zero findings
