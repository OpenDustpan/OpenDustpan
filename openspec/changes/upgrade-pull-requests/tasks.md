## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for daily scan that creates one record for a stale pin. Check: TestUpgradeScanCreatesOneRecord fails on main.
- [ ] 1.2 Add failing test for concurrent scans and restart with one open request. Check: TestUpgradeDuplicateOpenKeepsOneRequest fails on main.
- [ ] 1.3 Add failing test for range, prerelease, downgrade, and second newer version. Check: TestUpgradeVersionRulesSkipRangesAndPrereleases fails on main.
- [ ] 1.4 Add failing test for crash after forge create with branch lookup reuse. Check: TestUpgradeBranchLookupReusesRequest fails on main.
- [ ] 1.5 Add failing test for plan start, plan failure, and close before plan end. Check: TestUpgradePlanLinksToRequest fails on main.
- [ ] 1.6 Add failing test for plan status posted to the forge request. Check: TestUpgradeStatusPostedToForge fails on main.
- [ ] 1.7 Add failing test for no link, revoked credential, rate limit, collision, moved base, and absent pin. Check: TestUpgradeForgeRefusalsStored fails on main.
- [ ] 1.8 Add failing test for poll that marks merged, closed with suppression, and superseded. Check: TestUpgradeClosePollMarksClosed fails on main.

## 2. Build

- [ ] 2.1 Build daily scanner with jitter, unique key, and suppression. Check: Scanner compares unit settings pins with `GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions` and modules versions routes.
- [ ] 2.2 Build writer with idempotency and audit record. Check: Forge open through Forge port with branch lookup, audit entry with namespace service principal, credential reference, and reason.
- [ ] 2.3 Wire plan start and forge status post. Check: Plan start through milestone 5 plan path with `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` showing linked result.
- [ ] 2.4 Add e2e step against the fake forge and the registry routes. Credential kind: a personal access token with unit:manage and unit:read; the forge connection uses its stored credential reference. Check: E2E: set a stale pin in unit settings, publish a newer release with `PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{}`, see the request on the fake forge, read it with `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}`, then poll close.
- [ ] 2.5 Write docs page in same pull request. Check: Docs page upgrade-pull-requests runs as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the scanner into the server start. Add the routes `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests` and `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}`, both with action unit:read, called with a personal access token. Run the e2e step, finish the docs page and the usability check. Check: Route test shows the unit:read action on both routes; the e2e step above passes with a personal access token; docs complete; `make check` with zero findings.
