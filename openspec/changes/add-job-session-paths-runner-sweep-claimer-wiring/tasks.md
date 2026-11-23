## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing claimer tests in the NEW file pkg/runner/sweep_claimer_test.go: purpose match, kind match, purpose change during the claim. Check: `go test ./pkg/runner/ -run TestSweepClaimer -count=1` fails on main
- [ ] 1.2 Write failing route tests in the NEW file internal/api/runnerapi/sweep_report_test.go: report auth, bounds, malformed lines, header checks, cut stream, second upload, concurrent finish, finish cases. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` fails on main
- [ ] 1.3 Write the pool route test TestSweepPurposeOnTeamPool in the existing file internal/api/runnerpools/admin_test.go: create a pool of a namespace with the sweep purpose, list it, and update it with another purpose. The test pins the owner decision of 23 Nov 2026, and main already passes it. Check: `go test ./internal/api/runnerpools/ -run TestSweepPurposeOnTeamPool -count=1 -v` shows the test running and passing.

## 2. Build

- [ ] 2.1 Implement the sweep claimer in the NEW file pkg/runner/sweep_claimer.go (NEW names: NewSweepClaimer, sweepClaimer). The poll in internal/api/runnerapi/protocol.go already calls the claimer for a sweep pool, so leave its purpose switch as it is. Set the `Sweeps` field of the handler config in pkg/dustpan/runners.go to the claimer. Check: `go test ./pkg/runner/ ./internal/api/runnerapi/ -count=1` passes
- [ ] 2.2 Add the report and finish routes with the streaming bound and the encrypted blob write. Declare the routes in internal/api/runnerapi/routes.go and write the handlers in the NEW adapter file internal/api/runnerapi/sweep_report.go. The task adds the NEW top-level test `TestSweepReport` in a `_test.go` file of `internal/api/runnerapi`, so that the Check runs it. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` passes
- [ ] 2.3 Add no refusal of the sweep purpose on a pool of a namespace, and add no installation pool. Keep `runner.Pool.Validate` in pkg/runner/runner.go and the create route in internal/api/runnerpools/admin.go as they are. Check: `go test ./internal/api/runnerpools/ -run TestSweepPurposeOnTeamPool -count=1 -v` passes, and `rg -n -i 'installation pool' pkg/runner internal/api/runnerpools` finds nothing.
- [ ] 2.4 Document the sweep entries in docs/reference/runner-api.md, and say there that a sweep runs on a pool of the namespace with the sweep purpose and that its host identity needs no write access. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step runner-sweep-claim in the NEW file e2e/runner_sweep_claim_test.go against the real runner binary and the real TLS server. The runner joins a sweep pool with a join token and polls with its runner key proof, on the existing routes under /runner/v1. The launcher opens a session with the handoff token, then uploads the report on the new route `POST /runner/v1/session/sweep/report` and finishes on the new route `POST /runner/v1/session/sweep/finish`, both with its job-session key proof. Check: `make e2e` runs the runner-sweep-claim step against the TLS server.
- [ ] 4.2 Check the route actions, the wiring, the docs page and the usability walk, then run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
