## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.0 Tests for exchange, renew, step order and log hash exist on main: internal/api/runnerapi/session_test.go:201 (TestSessionRoutes), :342 (TestOpenSession), :460 (TestSessionRenewExtendsTheLease), :701 (TestLogAndStepEnd), :895 (TestLogChunkHashIsChecked). Tests for the three plan paths exist on main: internal/api/runnerapi/plan_test.go:301 (TestPlanUploadAndCommit, expects 204 for a repeat commit), :514 (TestPlanCommitIsChecked), :606 (TestPlanRoutesWithALostClaim), :640 (TestPlanRoutesNeedTheKeyOfTheSession). Check: `go test ./internal/api/runnerapi/ -run 'TestSession|TestPlan' -count=1` passes on main
- [ ] 1.1 Write failing tests for the phase and step byte caps and the phase chunk cap of the log in internal/api/runnerapi/session_test.go (existing file). Check: `go test ./internal/api/runnerapi/ -run TestLogPhaseCap -count=1` fails on main, because the NEW test `TestLogPhaseCapStoresNothing` finds no check at internal/api/runnerapi/session.go:557-575, which holds only the step chunk cap, and the server does not check protocol.MaxLogBytesPerStep, protocol.MaxLogChunksPerPhase and protocol.MaxLogBytesPerPhase
- [ ] 1.2 Write failing hostile tests in internal/api/runnerapi/session_hostile_test.go (NEW): namespace pool with the sweep purpose, runner key on the log and step paths, takeover during a log write, plan commit with other parts after a commit (conflict, stored plan unchanged). Main has TestSweepExchangeIsRefused at internal/api/runnerapi/session_guard_test.go:337. Check: `go test ./internal/api/runnerapi/ -run TestSessionHostile -count=1` fails on main for the cap and namespace-pool cases

## 2. Behavior and docs

- [ ] 2.1 Check the step and phase caps in the chunk transaction of the job recorder (job.DurableRecorder.AppendLog), with the limits of pkg/runner/protocol/protocol.go:156-162, and map a breach to invalid in internal/api/runnerapi/session.go (existing). A refused chunk keeps no bytes. Check: `go test ./internal/api/runnerapi/ -run 'TestSession|TestLogPhaseCap' -count=1` passes
- [ ] 2.2 Add rows to docs/reference/runner-api.md for the step and phase caps of the log and the plan part caps. Main has the rows for the session paths (:160-172) and the plan commit text (:226-236). Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request, or the pull request body says that the cap check adds no round trip.

## 4. Definition of done

- [ ] 4.1 Run the session suites on SQLite and PostgreSQL. Check: `go test -race ./internal/api/runnerapi/... ./pkg/runner/... ./pkg/job/...`
- [ ] 4.2 Add the log cap step to the e2e in e2e/job_session_paths_test.go (NEW): the real launcher calls the route POST /runner/v1/session with the credential kind handoff token, then sends a log chunk above a cap with the session key proof (action runner:claim) and gets invalid. Check: `make e2e` runs the session step.
- [ ] 4.3 Check the usability of the path from the docs page alone. Check: Each error names one command that helps.
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
