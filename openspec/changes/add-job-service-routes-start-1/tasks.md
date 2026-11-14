## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing service tests for the five stubs in NEW file pkg/job/service_actions_test.go (stubs at pkg/job/service.go:343-360) Check: `go test ./pkg/job -run TestServiceActions -count=1` fails on main
- [ ] 1.2 Write failing route tests in NEW file internal/api/jobs/actions_test.go Check: `go test ./internal/api/jobs -run TestJobActionRoutes -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Implement the five methods in pkg/job/service.go (func at line 357 and its neighbors) Check: `go test ./pkg/job -count=1` passes
- [ ] 2.2 Declare the routes in NEW file internal/api/jobs/actions.go and register them in the existing adapter of internal/api/jobs Check: `go test ./internal/api/jobs -run TestJobActionRoutes -count=1` passes
- [ ] 2.3 Add the NEW error code job.wrong-state to internal/errcode/catalog_job.go (next to job.unsupported), with its message and the one command that helps, and its row in internal/errcode/catalog_test.go. Check: `go test ./internal/errcode/...`

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step in NEW file e2e/job_actions_test.go. It calls the new job cancel route and the new job approve route over TLS with an access token of a service principal that holds the run and approve grants, against the real binary. BLOCKED by the missing start route in this part for a job source: the step seeds the job through job.Service in the test server Check: `make e2e` passes
- [ ] 4.2 Write the docs page in NEW file docs/guides/jobs.md and run the usability check Check: Docs commands run as written in CI
- [ ] 4.3 Run the full check suite Check: `make check` with zero findings
