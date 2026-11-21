## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for version, bounds, subset, hash mismatch, replay and triage in the new test file internal/unmanaged/report_test.go (add the new file). Check: `go test ./internal/unmanaged -run TestReport`

## 2. Build

- [ ] 2.1 Build the reader by extending the existing file internal/unmanaged/report.go in place. Add the encrypted stream write in the new file internal/unmanaged/report_store.go and the findings store in the new file internal/unmanaged/findings.go. Add the routes with their actions. Declare the routes in the new file internal/api/apidesc/findings.go and write the adapter in the new file internal/api/runnerapi/sweepreport.go. Keep NEW for new behavior only. Check: `go test ./internal/api/runnerapi -run TestSweepReport`

- [ ] 2.2 SECURITY. Build the authorize layer in the file internal/api/runnerapi/sweepreport.go. Check the session proof and the live sweep claim on upload. Check the namespace:manage-sweeps action on list and triage in the adapter of the findings routes. Check: `go test ./internal/api/runnerapi -run TestSweepReportRefusesJobClaimAndBadProof`
- [ ] 2.3 SECURITY. Build the fail-closed layer in the file internal/api/runnerapi/sweepreport.go. On any error, delete the staged blob, fail the sweep once and store no findings. Check: `go test ./internal/api/runnerapi -run TestSweepReportFailsClosedOnCutStream`
- [ ] 2.4 SECURITY. Write the hostile test TestSweepReportRefusesJobClaimAndBadProof in the new file internal/api/runnerapi/sweepreport_authz_test.go. It sends a job claim, a bad proof and a caller without the action straight to the layer. Check: `go test ./internal/api/runnerapi -run TestSweepReportRefusesJobClaimAndBadProof`
- [ ] 2.5 SECURITY. Write the hostile test TestSweepReportFailsClosedOnCutStream in the new file internal/api/runnerapi/sweepreport_failclosed_test.go. It cuts the stream and checks that no findings exist and the staged blob is deleted. Check: `go test ./internal/api/runnerapi -run TestSweepReportFailsClosedOnCutStream`
- [ ] 2.6 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order: `unmanaged.bad-report` and `unmanaged.scope-not-in-target` in the NEW file `internal/errcode/testdata/catalog/unmanaged.txt`; `unmanaged.report-too-large` in the NEW file `internal/errcode/testdata/catalog/unmanaged.txt`; `unmanaged.forbidden` in the NEW file `internal/errcode/testdata/catalog/unmanaged.txt`. No route: the test reads the catalog only. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run an e2e step with `POST /runner/v1/session/sweep-report` using a session proof, then list findings with an access token. BLOCKED by 160-165-sweep-targets-lifecycle for the sweep claim. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
