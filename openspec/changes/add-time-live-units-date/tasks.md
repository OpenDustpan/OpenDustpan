## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing over-limit save test in NEW pkg/unitttl/service_test.go. Check: TestSaveTimeToLiveAboveMaximumIsRefused fails on main. No route: the test calls SaveTimeToLive in memory only.
- [ ] 1.2 Write the failing pool expiry test in NEW pkg/unitttl/expiry_test.go. Check: TestExpiredPoolUnitWaitsForApproval fails on main. No route: the test opens the order in memory only.
- [ ] 1.3 Write the failing notice-only and inactivity tests in NEW pkg/unitttl/expiry_test.go. Check: TestExpiredUnitWithNoPoolGetsNoticeOnly fails on main. No route: in memory only.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the setting, the maximum check and the NEW action unit:set-time-to-live on the unit settings route. Check: PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with an access token passing the NEW action unit:set-time-to-live shows the expiry and its source.
- [ ] 3.2 Add the durable expiry claim and the three expiry actions with audit entries. Check: One destroy change order after two ticks and a restart; the test reads GET /api/dustpan/v1/namespaces/{}/units/{}/settings with an access token.
- [ ] 3.3 Ship the docs page. Check: Docs page for time to live passes `make docs-lint`.
- [ ] 3.4 Add an e2e step against the real CLI that sets a time to live through the unit settings route. Confirmed by: CI (make e2e). Check: E2e step sets a time to live through PUT /api/dustpan/v1/namespaces/{}/units/{}/settings with an access token.
- [ ] 3.5 Run the full check. Confirmed by: the driver's gates. Check: `make check` reports zero findings.
