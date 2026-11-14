## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write TestBreakGlassDisableWritesNotice in pkg/principal/breakglass_test.go: a disable with a second account writes one notice to each other enabled administrator, none to the actor. Check: TestBreakGlassDisableWritesNotice fails on main.
- [ ] 1.2 Write TestBreakGlassRefusalWritesOneNotice in pkg/principal/breakglass_test.go: refusal of a disable, a delete and a sweep of the last account gives 409 and one notice. Check: TestBreakGlassRefusalWritesOneNotice fails on main.
- [ ] 1.3 Write TestBreakGlassNoOtherAdministrator in pkg/principal/breakglass_test.go: the audit entry exists and no notice row exists. Check: TestBreakGlassNoOtherAdministrator fails on main.
- [ ] 1.4 Write TestBreakGlassParallelDisable in pkg/principal/breakglass_test.go: two parallel disables of the last two accounts leave one enabled. Check: TestBreakGlassParallelDisable fails on main under go test -race.
- [ ] 1.5 Write TestBreakGlassNoticeOutboxError in pkg/principal/breakglass_test.go: a forced outbox error leaves the account enabled and answers 500. Check: TestBreakGlassNoticeOutboxError fails on main.

## 2. Definition of done

- [ ] 2.1 Wire the notice into the disable route /api/server/principals/{}/disable, the delete route /api/server/principals/{} and the sweep path of pkg/accesstoken. Use an access token of a site administrator. Keep each route action as declared. Add the docs page. Confirmed by: CI (route test and make check). Check: The route test and `make check` give zero findings.
- [ ] 2.2 Add an e2e step that calls the existing route POST /api/server/principals/{}/disable, passes the site administrator action gate and uses an access token. The other administrator reads the notice. Confirmed by: CI (make e2e). Check: `make e2e` passes the new step.
- [ ] 2.3 Usability check: a site administrator completes the path from the docs page alone and the 409 text names `GET /api/server/principals`. A person's step. Check: The docs page commands run in a test.
