## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing test for the Retry-After header of the ready 503 in `pkg/dustpan/health_test.go` (existing file; see TestHealthReadyFailures at line 251 and TestHealthReadyOK at line 193). The route is the ready probe of `pkg/dustpan/health.go`, mounted in `pkg/dustpan/dustpan.go`; credential: none, a public route: `GET /health/ready` is on the public list `publicRoutes` (pkg/dustpan/route_actions_test.go:50, "readiness probe"). Check: TestHealthReadyRetryAfterOnStoreFailure (NEW) fails on main, because main sends no Retry-After on a failed store ping; the test sends `GET /health/ready` (credential: none, an unauthenticated public route) to a server whose store ping fails; route `GET /health/ready`, public allowlist gate, no credential.

## 2. Build

- [ ] 2.1 Set the Retry-After header of the failed-store ready 503 with `problem.SetStoreRetryAfter` in `pkg/dustpan/health.go` (readiness.ServeHTTP :288 and serveComponents :403). Check: `curl -s -i http://127.0.0.1:8080/health/ready` (credential: none, a public route of `publicRoutes`, pkg/dustpan/route_actions_test.go:50) shows 503 with Retry-After 5 during a stopped database; route `GET /health/ready`, public allowlist gate, no credential.

## 3. Side effects (computed from main)

- [ ] 3.1 Confirm that no request flow changed, so the round-trip budgets stand. Confirmed by: CI (the round-trip tests pass unchanged).

## 4. Definition of done

- [ ] 4.1 Wiring, route authorization actions unchanged, e2e step deferred, usability check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only); full long and short outage e2e is BLOCKED by database-outage-e2e-probe.
