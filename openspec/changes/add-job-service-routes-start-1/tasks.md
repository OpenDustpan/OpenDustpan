## 1. Failing tests first

- [ ] 1.1 Add failing service tests for start, get, cancel, approve, reject, confirm, and redact log Check: `go test ./pkg/job -run TestServiceStart -count=1` fails on main
- [ ] 1.2 Add failing route tests for start, list, get, cancel, approve, reject, confirm, and redact log with actions Check: `go test ./internal/api/jobs -run TestStartJobRoute -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Implement job.Service methods over job.Store with audit entries and masked plan diff Check: `go test ./pkg/job ./internal/api/jobs -count=1` passes
- [ ] 2.2 Wire routes with authorization actions and break-glass body field under the namespace break-freeze rule Check: `go test ./internal/api/jobs -run TestRouteActions -count=1` passes

## 3. Definition of done

- [ ] 3.1 Add e2e step that starts a plan job through the REST API and reads list and get Check: Real CLI plan job run against TLS server shows job in list and get
- [ ] 3.2 Write docs page for job start, list, get, cancel, approve, confirm, and redact log Check: Docs page commands run as written in CI
- [ ] 3.3 Run usability check of the user path Check: Developer completes start to approve from docs page alone
- [ ] 3.4 Run full check suite Check: `make check` with zero findings
