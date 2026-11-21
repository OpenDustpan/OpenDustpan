## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing claimer tests in the NEW file pkg/runner/sweep_claimer_test.go: purpose match, kind match, purpose change during the claim. Check: `go test ./pkg/runner/ -run TestSweepClaimer -count=1` fails on main
- [ ] 1.2 Write failing route tests in the NEW file internal/api/runnerapi/sweep_report_test.go: report auth, bounds, malformed lines, header checks, cut stream, second upload, concurrent finish, finish cases. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` fails on main
- [ ] 1.3 Write failing pool service tests in the NEW file internal/runnersvc/sweep_purpose_test.go for the purpose refusal and the purpose change. Check: `go test ./internal/runnersvc/ -run TestSweepPurposeInstallationOnly -count=1` fails on main

## 2. Build

- [ ] 2.1 Implement the sweep claimer in the NEW file pkg/runner/sweep_claimer.go (NEW names: NewSweepClaimer, sweepClaimer) and wire the poll by pool purpose in the adapter file internal/api/runnerapi/protocol.go. Check: `go test ./pkg/runner/ ./internal/api/runnerapi/ -count=1` passes
- [ ] 2.2 Add the report and finish routes with the streaming bound and the encrypted blob write. Declare the routes in internal/api/runnerapi/routes.go and write the handlers in the NEW adapter file internal/api/runnerapi/sweep_report.go. The task adds the NEW top-level test `TestSweepReport` in a `_test.go` file of `internal/api/runnerapi`, so that the Check runs it. Check: `go test ./internal/api/runnerapi/ -run TestSweepReport -count=1` passes
- [ ] 2.3 Refuse a namespace pool with the sweep purpose in the pool service in the NEW file internal/runnersvc/service.go (NEW name: the pool service). Check: `go test ./internal/runnersvc/ -run TestSweepPurposeInstallationOnly -count=1` passes
- [ ] 2.4 Document the sweep entries in docs/reference/runner-api.md. Check: `make docs-lint` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step runner-sweep-claim in the NEW file e2e/runner_sweep_claim_test.go against the real runner binary and the real TLS server. The runner joins a sweep pool with a join token and polls with its runner key proof, on the existing routes under /runner/v1. The launcher opens a session with the handoff token, then uploads the report on the new route `POST /runner/v1/session/sweep/report` and finishes on the new route `POST /runner/v1/session/sweep/finish`, both with its job-session key proof. Check: `make e2e` runs the runner-sweep-claim step against the TLS server.
- [ ] 4.2 Check the route actions, the wiring, the docs page and the usability walk, then run the full check. Check: `make check` reports zero findings.
