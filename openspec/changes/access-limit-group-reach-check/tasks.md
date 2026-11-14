## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add hostile-row service tests for PutAccessLimit group reach refusal in NEW file pkg/gate/configreach_test.go. No route: the test calls the service only. Check: TestPutAccessLimitRefusesHostileGroupLimit fails on main.
- [ ] 1.2 Add service test for decision error failing closed with the NEW reason access-limit.decision-error in NEW file pkg/gate/configreach_test.go. No route: the test calls the service only. Check: TestPutAccessLimitDecisionErrorFailsClosed fails on main.
- [ ] 1.3 Add service test for the allowed and narrowing paths where the saver holds the reach in NEW file pkg/gate/configreach_test.go. No route: the test calls the service only. Check: TestPutAccessLimitStoresAllowedGroupLimit fails on main.

## 2. Fix

- [ ] 2.1 Move the check into PutAccessLimit with fail-closed error handling in pkg/gate/config_objects.go:182 (func ConfigService.PutAccessLimit). Check: TestPutAccessLimitRefusesHostileGroupLimit passes with the service check active.
- [ ] 2.2 Keep the handler reach helper as defense in depth with a comment in internal/api/server/gate_objects.go:588 (func Handler.limitGroupReach) that names the service check as authoritative. Check: Hostile save through a direct service call that bypasses the handler in pkg/gate/config_objects.go:182 is refused.
- [ ] 2.3 Audit decision errors with the NEW reason access-limit.decision-error in internal/api/server/gate_objects.go:517 (func Handler.writeAccessLimit) using the single service audit helper. Check: Hostile save and forced reach error through PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id} with an access token show 403 and 500, with the NEW reason access-limit.decision-error for decision errors.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the service check through the existing PUT access-limit routes in internal/api/server/gate_objects.go:517 with their authorization actions. Check: PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id} with an access token refuses a hostile group limit.
- [ ] 4.2 Update the docs page for access limits in docs/reference/. The page states the service check and both audit reasons. Check: Docs page for access limits states the service check and the audit reasons.
- [ ] 4.3 Run the usability check of the save path from the docs page alone. A person's step. Check: Saver completes the PUT save path from the docs page alone.
- [ ] 4.4 Run make check with zero findings. Confirmed by: the driver's gates. Check: make check with zero findings.
