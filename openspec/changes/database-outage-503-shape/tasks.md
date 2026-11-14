## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for ready 503 on GET /health/ready in `pkg/dustpan/health_test.go` (existing file; see TestHealthReadyOK at line 193). The route is the ready probe of `pkg/dustpan/health.go`, mounted in `pkg/dustpan/dustpan.go` (see :1953); credential: none, public probe route. Check: TestReady503WithRetryAfterWhenStoreDown (NEW) fails on main; the test needs a store that fails its ping, and main has no such outage hook. BLOCKED by the missing store outage error and ping failure hook (NEW in `pkg/dustpan/outage.go`)
- [ ] 1.2 Add failing adapter tests for Pulumi, TFE-compatible and tool-neutral write mapping in NEW file `internal/api/tfe/outage_test.go`. Check: TestWriteMapsOutageTo503InEachShape (NEW) fails on main; calls the existing route `POST /api/v2/state-versions`, passes the authorizer gate, with an access token

## 2. Build

- [ ] 2.1 Wire outage error, ready mapping, live bypass and per-API 503 mapping with 2 second store timeout in `internal/api/tfe/states.go:602` (Handler.writeStateVersion), `internal/api/tfe/locks.go:103` (Handler.lockWorkspace) and NEW file `pkg/dustpan/outage.go`. Check: `curl -s http://127.0.0.1:8080/health/ready` shows 503 with Retry-After 30 during a stopped database; route `GET /health/ready`, public allowlist gate, no credential
- [ ] 2.2 Write docs page for outage signals and Retry-After 30 seconds in NEW file `docs/outage-signals.md`. Check: docs page command `curl -s http://127.0.0.1:8080/health/live` shows 200; route `GET /health/live`, public allowlist gate, no credential

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring, route authorization actions unchanged, e2e step deferred, docs page, usability check Check: `make check` with zero findings; full long and short outage e2e is BLOCKED by database-outage-e2e-probe. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
