## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Probe hardening

- [ ] 1.1 In pkg/dustpan/dustpan.go add the NEW Server.UngatedHandler test-support constructor next to Server.Handler that returns the same mux without the credential gate. Check: `go test ./pkg/dustpan -run TestUnlistedPathsMatchGatedAndUngated -count=1` fails on main and passes after, proving the NEW Server.UngatedHandler exists.
- [ ] 1.2 In pkg/dustpan/route_actions_test.go add the NEW test TestUnlistedPathsMatchGatedAndUngated that requests the fixed allowlist with no redirect and no public 2xx through Server.Handler and the NEW Server.UngatedHandler on a started server with all components enabled and requires identical non-2xx status and code. Check: The NEW TestUnlistedPathsMatchGatedAndUngated in pkg/dustpan/route_actions_test.go fails on main and passes after.
- [ ] 1.3 In pkg/dustpan/route_actions_test.go extend the probe with one exact-answer table per route family and fail any 400 or 404 that is not the table value. Check: TestNoRouteAnswersWithoutACredential in pkg/dustpan/route_actions_test.go fails with a 400 probe and passes with the table.
- [ ] 1.4 In pkg/dustpan/route_actions_test.go rewrite the probe doc comment to name the covered handler chain and the skipped misuse reporter, request cache, request log, and component gate. Check: `rg -n -F 'whole handler chain' pkg/dustpan` returns no matches and the new comment lists covered and skipped layers.

## 2. Coverage proof

- [ ] 2.1 In pkg/dustpan/route_actions_test.go show coverage by mutating one public entry and one exact-answer table value and requiring the probe to fail. Check: Temporarily remove one public entry from the table in pkg/dustpan/route_actions_test.go and `go test ./pkg/dustpan -run TestNoRouteAnswersWithoutACredential -count=1` fails, then restore it and it passes.
- [ ] 2.2 In pkg/dustpan/route_actions_test.go show positive control with a temporary unlisted middleware path that makes Handler and the NEW Server.UngatedHandler differ. Check: Add a temporary middleware in pkg/dustpan/route_actions_test.go that serves one unlisted path and the NEW TestUnlistedPathsMatchGatedAndUngated fails, then remove it and it passes.

## 3. Cleanup

- [ ] 3.1 In internal/api audit, inventory, pulumi, server, and tfe routes_test.go files delete publicPatterns only after the tightened probe and both proof tasks pass on the same tree. Check: `rg -n -F publicPatterns internal/api` returns no matches and the probe passes on the same tree.

## 4. Side effects (computed from main)

- [ ] 4.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 5. Definition of done

- [ ] 5.1 Run the route probe tests. Check: `go test ./pkg/dustpan -run 'TestNoRouteAnswersWithoutACredential|TestUnlistedPathsMatchGatedAndUngated' -count=1` passes.
- [ ] 5.2 Run the full check. Confirmed by: the driver's gates. Check: `make check` with zero findings.
