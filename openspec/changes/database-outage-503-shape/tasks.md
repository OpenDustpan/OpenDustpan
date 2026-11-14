## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for ready 503 on GET /health/ready, a route on the public allowlist, called with no credential Check: TestReady503WithRetryAfterWhenStoreDown fails on main; calls `GET /health/ready` through the public allowlist gate with no credential
- [ ] 1.2 Add failing adapter tests for Pulumi, TFE-compatible and tool-neutral write mapping Check: TestWriteMapsOutageTo503InEachShape fails on main; calls `ANY /api/v2/state-versions` through the authorizer gate with an access token

## 2. Build

- [ ] 2.1 Wire outage error, ready mapping, live bypass and per-API 503 mapping with 2 second store timeout Check: `curl -s http://127.0.0.1:8080/health/ready` shows 503 with Retry-After 30 during a stopped database; route `GET /health/ready`, public allowlist gate, no credential
- [ ] 2.2 Write docs page for outage signals and Retry-After 30 seconds Check: docs page command `curl -s http://127.0.0.1:8080/health/live` shows 200; route `GET /health/live`, public allowlist gate, no credential

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring, route authorization actions unchanged, e2e step deferred, docs page, usability check Check: `make check` with zero findings; full long and short outage e2e is BLOCKED by database-outage-e2e-probe
