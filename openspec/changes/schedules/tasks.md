## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test in NEW file pkg/durable/interval_test.go that a production build refuses the NEW test-only 10 s interval value. Check: NEW `go test ./pkg/durable/... -run TestIntervalOverrideRefused` fails on main and passes with the change
- [ ] 1.2 Write the failing store test in NEW file pkg/store/storetest/schedule_claim.go that two workers race ClaimDue (NEW) and only one wins. Check: NEW store test TestScheduleClaim on sqlite and postgres fails on main, passes with the change, calls no route

## 2. Implementation

- [ ] 2.1 Add the exit-test step in NEW file internal/servertest/ha_schedules_test.go that sets the test-only 10 s interval, runs 60 s on three instances behind the proxy, then calls existing `ANY /api/server/instances` with an access token and counts one row per start key at most. Check: e2e schedules step passes on three instances: calls existing `ANY /api/server/instances` through its declared authorization actions gate with an access token credential and counts durable_operations start keys

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wiring and route: keep the existing `ANY /api/server/instances` route with its authorization actions, checked with an access token. Check: Route test shows existing `ANY /api/server/instances` declares its authorization actions gate and refuses without an access token credential
- [ ] 4.2 e2e step against the real proxy and three serve binaries, with an access token on existing `ANY /api/server/instances`. Check: `make e2e-ha` schedules step passes on three serve binaries with an access token credential on existing `ANY /api/server/instances` through its declared authorization actions gate. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Docs page and usability check with measured figures, date, and no SLA. Check: docs/guides/high-availability.md states the measured tick figures with date and no SLA wording. A person's step.
- [ ] 4.4 Run all checks. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
