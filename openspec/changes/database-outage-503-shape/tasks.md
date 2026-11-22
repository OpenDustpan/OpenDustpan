## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a failing server test for the Retry-After header of the ready 503 in `pkg/dustpan/health_test.go` (existing file; see TestHealthReadyFailures at line 251 and TestHealthReadyOK at line 193). The route is the ready probe of `pkg/dustpan/health.go`, mounted in `pkg/dustpan/dustpan.go`; credential: none, a public route: `GET /health/ready` is on the public list `publicRoutes` (pkg/dustpan/route_actions_test.go:50, "readiness probe"). Check: TestHealthReadyRetryAfterOnStoreFailure (NEW) calls the existing route `GET /health/ready` (credential: none, a public route of `publicRoutes`, pkg/dustpan/route_actions_test.go:50) and fails on main, because main sends no Retry-After on a failed store ping; the test reuses the failing store ping of the existing test helpers. route `GET /health/ready`, public allowlist gate, no credential.
- [ ] 1.2 Add a failing adapter test for the TFE-compatible write mapping in NEW file `internal/api/tfe/outage_test.go`. Check: TestWriteMapsOutageTo503InTFEShape (NEW) fails on main (Retry-After is 5, not 30); it calls the existing route `POST /api/v2/workspaces/{id}/state-versions` (`internal/api/tfe/state_write.go:168`, Handler.createStateVersion) with an access token that has unit:write, and the store returns `namespace.ErrUnavailable`.
- [ ] 1.3 Add a failing adapter test for the Pulumi write mapping in NEW file `internal/api/pulumi/outage_test.go`. Check: TestWriteMapsOutageTo503InPulumiShape (NEW) fails on main (Retry-After is 5, not 30); the store returns `namespace.ErrUnavailable` on a stack write. The tool-neutral shape reads the same constant, and `pkg/dustpan/ipallow_deny_internal_test.go:971` already computes its expected value from it.

## 2. Build

- [ ] 2.1 Set `StoreRetryAfter` to 30 seconds in `internal/api/problem/problem.go:370`, and set the Retry-After header of the failed-store ready 503 with `problem.SetStoreRetryAfter` in `pkg/dustpan/health.go` (readiness.ServeHTTP :288 and serveComponents :403). The three adapters (TFE, Pulumi, HTTP backend) already read the constant and need no edit. Check: `curl -s -i http://127.0.0.1:8080/health/ready` (credential: none, a public route of `publicRoutes`, pkg/dustpan/route_actions_test.go:50) shows 503 with Retry-After 30 during a stopped database; route `GET /health/ready`, public allowlist gate, no credential. This task waits for the owner to confirm 30 (see the proposal, Open questions).
- [ ] 2.2 Write docs page for outage signals and Retry-After 30 seconds in NEW file `docs/outage-signals.md`, and change `Retry-After: 5` to 30 in `docs/guides/high-availability.md:129` and `docs/guides/several-instances.md:175` and `:297`. Check: `make docs-lint` passes on each edited guide; docs page command `curl -s -i http://127.0.0.1:8080/health/live` shows 204; route `GET /health/live`, credential: none, a public route on the public list `publicRoutes` (pkg/dustpan/route_actions_test.go:49, "liveness probe")

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring, route authorization actions unchanged, e2e step deferred, docs page, usability check Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only); full long and short outage e2e is BLOCKED by database-outage-e2e-probe.
