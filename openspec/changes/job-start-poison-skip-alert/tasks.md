## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the failing tests of the spec scenarios in internal/metrics/poison_skip_test.go (NEW). Check: go test ./internal/metrics/... fails on main

## 2. Definition of done

- [ ] 2.1 Wire the counter and the alert into the server and add the docs page. Check: e2e step reads ANY /api/server/metrics with an installation administrator access token after 10 skips. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.3 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
