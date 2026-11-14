## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service tests for Start, Get, Cancel, Approve, Reject, Confirm, and RedactLog with audit entries Check: `go test ./pkg/job -run TestServiceStart -count=1` fails on main
- [ ] 1.2 Add failing route tests for new start, list, get, cancel, approve, reject, confirm, and redact log routes with actions Check: `go test ./internal/api/jobs -run TestStartJobRoute -count=1` fails on main
- [ ] 1.3 Add failing masking tests for plan diff secrets, missing plan, timeline masking, literal patterns, and chunk-split secrets Check: `go test ./pkg/job -run TestMaskPlanDiff -count=1` fails on main
- [ ] 1.4 Add failing tests for same-key repeat, different-body reuse, cross-scope key, and concurrent first call Check: `go test ./pkg/job -run TestIdempotentStart -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Implement job.Service Start, Get, List, Cancel, Approve, Reject, Confirm, and RedactLog over job.Store with audit entries and masked plan diff Check: `go test ./pkg/job ./internal/api/jobs -count=1` passes
- [ ] 2.2 Wire new start, list, get, timeline, plan-diff, cancel, approve, reject, confirm, and redact routes with authorization actions and break-glass body field under the namespace break-freeze rule Check: `go test ./internal/api/jobs -run TestRouteActions -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step that starts a plan job through the new POST jobs route with a bearer access token and reads list, get, timeline, and masked diff Check: Start a plan job with `POST /api/dustpan/v1/namespaces/{namespace}/jobs` with a bearer access token, then read it with `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}`; both calls succeed and the list holds the job
- [ ] 4.2 Write docs page for job start, list, get, timeline, masked diff, cancel, approve, reject, confirm, and redact log Check: Docs page commands run as written in CI
- [ ] 4.3 Run usability check of the user path Check: Developer completes start to approve from docs page alone
- [ ] 4.4 Run full check suite Check: `make check` with zero findings
