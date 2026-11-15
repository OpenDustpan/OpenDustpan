## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.0 Tests for exchange, renew, step order and log caps and hash exist on main: internal/api/runnerapi/session_test.go:201 (TestSessionRoutes), :342 (TestOpenSession), :460 (TestSessionRenewExtendsTheLease), :650 (TestLogAndStepEnd), :836 (TestLogChunkHashIsChecked). Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` passes on main
- [ ] 1.1 Write failing tests for source, credentials, plan part upload, plan caps and the double commit in internal/api/runnerapi/session_test.go (existing file). Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` fails on main
- [ ] 1.2 Write failing hostile tests in internal/api/runnerapi/session_hostile_test.go (NEW): namespace pool with the sweep purpose, takeover during plan upload, runner key on the new session paths. Main has TestSweepExchangeIsRefused at internal/api/runnerapi/session_guard_test.go:337. Check: `go test ./internal/api/runnerapi/ -run TestSessionHostile -count=1` fails on main

## 2. Behavior and docs

- [ ] 2.1 Wire the six missing session routes (source, source object, plan part, credentials, plan upload, plan commit) to one port call after the claim check. Declare the routes in internal/api/runnerapi/routes.go (existing, near :164-199) and write the adapter in internal/api/runnerapi/session.go (existing). Check: `go test ./internal/api/runnerapi/ -run TestSession -count=1` passes
- [ ] 2.2 Add docs/reference/runner-api.md rows for the six missing session paths, their caps and error codes. Main has the rows for /session, renew and steps (:160-163). Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the session suites on SQLite and PostgreSQL. Check: `go test -race ./internal/api/runnerapi/... ./pkg/runner/...`
- [ ] 4.2 Add the e2e step in e2e/job_session_paths_test.go (NEW): the real launcher calls the route POST /runner/v1/session with the credential kind handoff token, then the session paths with the session key proof (action runner:claim). Check: `make e2e` runs the session step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Check the usability of the path from the docs page alone. Check: Each error names one command that helps. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
