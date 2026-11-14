## 1. Failing tests first

- [ ] 1.1 Add failing test for daily scan that creates one record for a stale pin. Check: TestUpgradeScanCreatesOneRecord fails on main.
- [ ] 1.2 Add failing test for concurrent scans and restart with one open request. Check: TestUpgradeDuplicateOpenKeepsOneRequest fails on main.
- [ ] 1.3 Add failing test for range, prerelease, downgrade, and second newer version. Check: TestUpgradeVersionRulesSkipRangesAndPrereleases fails on main.
- [ ] 1.4 Add failing test for plan start, plan failure, plan timeout, and close before plan end. Check: TestUpgradePlanLinksToRequest fails on main.
- [ ] 1.5 Add failing test for 502 forge-unreachable path. Check: TestUpgradeForgeFailureRecordsUnreachable fails on main.
- [ ] 1.6 Add failing test for poll that marks merged, closed, and superseded requests. Check: TestUpgradeClosePollMarksClosed fails on main.
- [ ] 1.7 Add failing test for service principal record and 403 for caller without grant. Check: TestUpgradeActorAndForbidden fails on main.

## 2. Build

- [ ] 2.1 Probe Forge port for pin-change pull request open. Check: Probe of Forge port proves open of a pin-change pull request or lists the missing call.
- [ ] 2.2 Add forge write call behind Forge port when probe shows a gap. Check: New forge write call behind Forge port with hostile input tests when probe fails.
- [ ] 2.3 Build scanner, POST open route, and writer with authorization actions. Check: POST /api/dustpan/v1/namespaces/{namespace}/upgrade-requests opens a request with unit:manage on the unit stable ID.
- [ ] 2.4 Build list and GET one routes with authorization actions. Check: GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests lists requests with unit:read on the unit stable ID.
- [ ] 2.5 Build close route with authorization action and merged, closed, and superseded states. Check: POST /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}/close closes a request with unit:manage on the unit stable ID.
- [ ] 2.6 Add e2e step against real CLI and fake forge. Check: E2E step with real binary over TLS: stale pin, fake forge pull request, plan runs as in milestone 5.
- [ ] 2.7 Write docs page in same pull request. Check: Docs page upgrade-pull-requests runs as written.

## 3. Definition of done

- [ ] 3.1 Meet definition of done: wiring, route, e2e, docs, usability check, make check. Check: Route declares authorization actions, e2e passes, docs page complete, usability check done, make check with zero findings.
