## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the tests of the scenarios in internal/api/runnerapi/protocol_test.go (NEW; package runnerapi, which the change edits in protocol.go). Check: TestPollSkipCounterCountsJobsAfterTheBound fails on main

## 2. Definition of done

- [ ] 2.1 Wire the counter to the server metrics registry. Check: BLOCKED by the credential kind of the metrics route: the pack names the route ANY /api/server/metrics but no credential kind; the e2e step reads it with the access token of an operator principal. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Document the counter in the page docs/reference/metrics.md (NEW; no page on main covers the metrics route). The task names the counter and its meaning. Check: make docs-lint
- [ ] 2.4 Run the full check. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).
