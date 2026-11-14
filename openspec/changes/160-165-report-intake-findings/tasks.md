## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for version, bounds, subset, hash mismatch, replay and triage in the NEW file internal/unmanaged/report_test.go. Check: `go test ./internal/unmanaged -run TestReport`

## 2. Build

- [ ] 2.1 Build the reader in the NEW file internal/unmanaged/report.go, the encrypted stream write in the NEW file internal/unmanaged/report_store.go, the findings store in the NEW file internal/unmanaged/findings.go, and the routes with their actions. Declare the routes in the NEW file internal/api/apidesc/findings.go and write the adapter in the NEW file internal/api/runnerapi/sweepreport.go. Check: `go test ./internal/api/runnerapi -run TestSweepReport`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run an e2e step with `POST /runner/v1/session/sweep-report` using a session proof, then list findings with an access token. BLOCKED by 160-165-sweep-targets-lifecycle for the sweep claim. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the docs page and run the usability check. Check: `make docs-lint`. A person's step.
- [ ] 4.3 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
