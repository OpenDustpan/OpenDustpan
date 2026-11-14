## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing route tests in NEW file pkg/runner/pool_routes_test.go for list, read, create, change, delete, token list, create and revoke, drain and retire on the new paths, with a personal token of a principal that holds namespace:manage-runners. Name TestPoolRoutes_NewPaths as NEW. Check: TestPoolRoutes_NewPaths fails on main
- [ ] 1.2 Add failing test in NEW file pkg/runner/pool_routes_test.go that old pool paths answer 404. Name TestPoolRoutes_OldPathsAreUnknown as NEW. Check: TestPoolRoutes_OldPathsAreUnknown fails on main
- [ ] 1.3 Add failing tests in NEW file pkg/runner/pool_routes_test.go that each mutation stores its audit entry in the same transaction. Name TestPoolRoutes_AuditEntriesStoredInTransaction as NEW. Check: TestPoolRoutes_AuditEntriesStoredInTransaction fails on main
- [ ] 1.4 Add failing tests in NEW file pkg/runner/pool_routes_test.go for 401 and 403 on each route. Name TestPoolRoutes_AuthRefusals as NEW. Check: TestPoolRoutes_AuthRefusals fails on main

## 2. Build

- [ ] 2.1 Move the adapter to the new prefix with the same actions. Edit the route declaration in e2e/coverage.go and the adapter in internal/api/runnerapi/routes.go. Test the path with the existing route GET /api/runners/{namespace}/pools before the move, and with the NEW route GET /api/dustpan/v1/namespaces/{namespace}/runner-pools after the move, each with a personal token that holds namespace:read-runners. Check: `go test ./pkg/runner/... ./internal/api/...` passes
- [ ] 2.2 Write the audit entries in each change transaction in pkg/runner/coordinator.go (NEW file if absent on main) with runner.AuditRules. Check: `go test ./...` shows the audit events
- [ ] 2.3 Update ServedRoutes in e2e/coverage.go: replace the old pool route GET /api/runners/{namespace}/pools with GET /api/dustpan/v1/namespaces/{namespace}/runner-pools, called with a personal token that holds namespace:read-runners Check: `make e2e` route coverage check lists the new paths

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the routes with their authorization actions Check: The route test shows each action
- [ ] 4.2 Write the docs page for pool, drain, retire and join-token management Check: Docs commands run as written against a dev server
- [ ] 4.3 Run the usability check Check: A runner operator completes pool and token work from the docs page; each error names one command
- [ ] 4.4 Pass the full check Check: `make check` reports zero findings
