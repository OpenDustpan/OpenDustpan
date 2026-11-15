## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Write failing service tests for the five stubs in NEW file pkg/job/service_actions_test.go (stubs at pkg/job/service.go:343-360) Check: `go test ./pkg/job -run TestServiceActions -count=1` fails on main
- [x] 1.2 Write failing route tests in NEW file internal/api/jobs/actions_test.go Check: `go test ./internal/api/jobs -run TestJobActionRoutes -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Implement the five methods in pkg/job/service.go (func at line 357 and its neighbors) Check: `go test ./pkg/job -count=1` passes
- [x] 2.2 Declare the routes in NEW file internal/api/jobs/actions.go and register them in the existing adapter of internal/api/jobs. The task adds the NEW top-level test `TestJobActionRoutes` in a `_test.go` file of `internal/api/jobs`, so that the Check runs it. Check: `go test ./internal/api/jobs -run TestJobActionRoutes -count=1` passes
- [ ] 2.3 Add the NEW error code job.wrong-state to internal/errcode/catalog_job.go (next to job.unsupported), with its message and the one command that helps, and its row in internal/errcode/catalog_test.go. Check: `go test ./internal/errcode/...`
- [x] 2.4 Approve records one verdict on the gate of a job in the state awaiting-approval. Add the NEW event approval-recorded (awaiting-approval to awaiting-approval) in `pkg/job/state.go`; every other state refuses it. Add the NEW fields `Approvers` (nil is the closed policy: it counts no signature, so it never opens a gate) and NEW `MaxPlanAge` (default job.DefaultMaxPlanAge, 24 hours, `pkg/job/artifact.go:20`) to ServiceConfig (`pkg/job/service.go:104`), and pass the policy from `pkg/dustpan/jobservice.go`. The NEW route is POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/approvals with the action job:approve on the unit, called with an access token, declared in internal/api/jobs/actions.go (NEW, task 2.2) and served by the adapter of internal/api/jobs; a caller without job:read gets the same 404 as for an unknown job. The checks fail closed in this order: the verdict and the comment, job:read then job:approve, the state, the plan age. Two distinct approvers, approval defaults and signature carry-over stay out of scope (several-approval-requirements-one-change and approval-defaults-take-effect-live); the fresh step of a destroy approval stays with policy-approvals. Check: `go test ./pkg/job/... ./internal/api/jobs/...` passes, with a hostile case for each check

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [x] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step in NEW file e2e/job_actions_test.go. It calls the new job cancel route and the new job approve route over TLS with an access token of a service principal that holds the run and approve grants, against the real binary. BLOCKED by the missing start route in this part for a job source: the step seeds the job through job.Service in the test server Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [x] 4.2 Write the docs page in NEW file docs/guides/jobs.md and run the usability check Check: Docs commands run as written in CI. A person's step. `make docs-lint` passes on each edited guide.
- [ ] 4.3 Run the full check suite Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
