## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 BLOCKED by the open questions of the proposal and by the dependency to be written (work that carries the delegation). Check: each answer is a repository record, and the dependency is on main.

## 1. Failing tests first

Each scenario of the delta spec maps to one named test that fails on main.

- [ ] 1.1 In the NEW file `internal/delegationsvc/resolve_test.go`, add failing tests: `TestRequestAfterEndIsRefused`, `TestReplayOfAnAllowAnswerAfterEndIsRefused`, `TestStoreErrorFailsClosed`, `TestDatabaseGapAnswers503`. Check: each fails on main with a build error.
- [ ] 1.2 In the NEW file `pkg/job/delegation_end_test.go`, add failing tests: `TestWaitingJobIsCanceledWithDelegationEnded`, `TestRunningPhaseStopsAtTheNextCall`, `TestConfirmedPhaseFinishesByDefault`, `TestNoNewPhaseStartsAfterTheEnd`. BLOCKED by open question 1 for the setting. Check: each fails on main.
- [ ] 1.3 In the NEW file `internal/trigger/delegation_end_test.go`, add failing tests: `TestTriggerOfAnEndedDelegationIsDisabled`, `TestScheduleOfAnEndedDelegationIsDisabled`, `TestJobFromAnEndedDelegationWriteDoesNotStart`. Check: each fails on main.
- [ ] 1.4 In the NEW file `internal/agent/delegation_end_test.go`, add failing tests: `TestRunEndsWithDelegationEnded`, `TestFinishedToolCallStands`. Check: each fails on main.
- [ ] 1.5 In the NEW file `internal/api/server/delegation_revoke_agent_test.go`, add the failing test `TestAgentCannotRevokeADelegation`. Check: it fails on main with 404.

## 2. Build

- [ ] 2.1 Call `Resolve` at each use of a delegation in the service and the handler, with no allow cache. Check: the tests of 1.1 pass, and a refusal answers 403 `delegation.ended` with the delegation ID and the cause.
- [ ] 2.2 Add the stop reason `delegation-ended` to the stop reasons of jobs, triggers, schedules and runs, and cancel the waiting work. BLOCKED by open question 3 for the idle and queued work. Check: the tests of 1.2 to 1.4 pass.
- [ ] 2.3 Write the refusal entry in the shape of `every-step-audited`. BLOCKED by open question 4. Check: `GET /api/audit/entries` shows the end entry and each refusal entry with the agent, the delegation and the cause.
- [ ] 2.4 Add the NEW error code `delegation.ended` to `internal/errcode` and its line in the NEW file `internal/errcode/testdata/catalog/delegation.txt` in the name order. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes. If `person-delegates-dusty` merged first, add the line to its file in the name order.
- [ ] 2.5 SECURITY. Add the hostile tests of the layers table of `design.md`, and for each guard a test that fails when the guard is removed. Check: remove each guard in turn in a local run and see its test fail.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`) when a route or an answer changes. Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.
- [ ] 3.2 The answers of the open questions become ADRs or ADR amendments tagged "(owner, YYYY-MM-DD)", and `make adr-index` regenerates `docs/adr/INDEX.md`. This change adds no ADR itself. Check: the generators leave no diff.

## 4. Definition of done

- [ ] 4.1 Extend `e2e/audit_export_test.go` with the revoke case, against the real binary over TLS on PostgreSQL: revoke through the route of `person-delegates-dusty`, read `GET /api/audit/entries`, and check that a request of the agent is refused. BLOCKED by the Dusty run job. Check: the step passes in CI.
- [ ] 4.2 Extend `docs/guides/audit-log.md` with a revoke section and the exact verify command. Check: the e2e step of 4.1 runs each command as written, and `make docs-lint` passes.
- [ ] 4.4 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
