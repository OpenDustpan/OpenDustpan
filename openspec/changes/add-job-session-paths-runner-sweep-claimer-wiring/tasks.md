## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing claimer tests in the NEW file pkg/runner/sweep_claimer_test.go: purpose match, kind match, purpose change during the claim. Check: `go test ./pkg/runner/ -run TestSweepClaimer -count=1` fails on main
- [ ] 1.2 Write failing route tests in the NEW file internal/api/runnerapi/sweep_report_test.go: report auth, bounds (413 from runner.ErrReportTooLarge), malformed lines, header checks, scope outside the target, cut stream, second upload, upload under a lost claim, and the refusal of a finish for a sweep session. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` fails on main
- [ ] 1.3 Write the pool route test TestSweepPurposeOnTeamPool in the existing file internal/api/runnerpools/admin_test.go: create a pool of a namespace with the sweep purpose, list it, and update it with another purpose. The test pins the owner decision of 23 Nov 2026, and main already passes it. Check: `go test ./internal/api/runnerpools/ -run TestSweepPurposeOnTeamPool -count=1 -v` shows the test running and passing.

## 2. Build

- [ ] 2.1 Implement the sweep claimer in the NEW file pkg/runner/sweep_claimer.go (NEW names: NewSweepClaimer, sweepClaimer). The poll in internal/api/runnerapi/protocol.go already calls the claimer for a sweep pool, so leave its purpose switch as it is. Task 2.5 sets the `Sweeps` field of the handler config in pkg/dustpan/runners.go. Check: `go test ./pkg/runner/ ./internal/api/runnerapi/ -count=1` passes
- [ ] 2.2 Add the handler of the existing route `PUT /runner/v1/session/sweep-report` (the constant PathSessionSweepReport in pkg/runner/protocol/protocol.go:115) with the streaming bound and the encrypted blob write, in the NEW adapter file internal/api/runnerapi/sweep_report.go, and declare it in internal/api/runnerapi/routes.go. Add no finish route: keep the refusal of a finish for a sweep session in internal/api/runnerapi/session.go. Map `runner.ErrReportTooLarge` to 413 with the protocol error shape in internal/api/runnerapi/errors.go. The task adds the NEW top-level test `TestSweepReport` in a `_test.go` file of `internal/api/runnerapi`, so that the Check runs it. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` passes
- [ ] 2.3 Add no refusal of the sweep purpose on a pool of a namespace, and add no installation pool. Keep `runner.Pool.Validate` in pkg/runner/runner.go and the create route in internal/api/runnerpools/admin.go as they are. Check: `go test ./internal/api/runnerpools/ -run TestSweepPurposeOnTeamPool -count=1 -v` passes, and `rg -n -i 'installation pool' pkg/runner internal/api/runnerpools` finds nothing.
- [ ] 2.4 Document the sweep entries in docs/reference/runner-api.md, and say there that a sweep runs on a pool of the namespace with the sweep purpose and that its host identity needs no write access. Check: `make docs-lint` passes
- [ ] 2.5 Add the NEW interfaces `SweepReportStore` and `SweepScopeReader` and the NEW sentinel `ErrReportTooLarge` (it wraps ErrInvalid) in the file pkg/runner/sweep_claimer.go of task 2.1. Implement both interfaces in the NEW file pkg/dustpan/sweep_report.go over the existing blob, data key and sweep store ports, with the NEW test file pkg/dustpan/sweep_report_test.go (bounds, a scope outside the target, a second upload, an encrypted blob, a stale fence). Set the `Sweeps` field in pkg/dustpan/runners.go to the claimer built with them. The implementation is not a store port, so no storetest case or schema change is needed. Check: `go test ./pkg/runner/ ./pkg/dustpan/ -run TestSweep -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step runner-sweep-claim in the NEW file e2e/runner_sweep_claim_test.go against the real runner binary and the real TLS server. The runner joins a sweep pool with a join token and polls with its runner key proof, on the existing routes under /runner/v1. The launcher opens a session with the handoff token, then uploads the report with `PUT /runner/v1/session/sweep-report` and its job-session key proof; the upload ends the sweep. Check: `make e2e` runs the runner-sweep-claim step against the TLS server.
- [ ] 4.2 Check the route actions, the wiring, the docs page and the usability walk, then run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
