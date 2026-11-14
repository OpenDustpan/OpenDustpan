## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the tests of the scenarios. Check: TestPollSkipCounterCountsJobsAfterTheBound fails on main

## 2. Definition of done

- [ ] 2.1 Wire the counter to the server metrics registry. Check: BLOCKED by the credential kind of the metrics route: the pack names the route ANY /api/server/metrics but no credential kind; the e2e step reads it with the access token of an operator principal
- [ ] 2.2 Document the counter on the metrics docs page. Check: make docs-lint
- [ ] 2.3 Usability check: an operator finds the counter from the docs page alone. Check: Manual walk of the docs page
- [ ] 2.4 Run the full check. Check: make check with zero findings
