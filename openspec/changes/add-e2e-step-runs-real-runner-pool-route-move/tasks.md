## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing route tests for list, read, create, change, delete, token list, create and revoke, drain and retire on the new paths Check: TestPoolRoutes_NewPaths fails on main
- [ ] 1.2 Add failing test that old pool paths answer 404 Check: TestPoolRoutes_OldPathsAreUnknown fails on main
- [ ] 1.3 Add failing tests that each mutation stores its audit entry in the same transaction Check: TestPoolRoutes_AuditEntriesStoredInTransaction fails on main
- [ ] 1.4 Add failing tests for 401 and 403 on each route Check: TestPoolRoutes_AuthRefusals fails on main

## 2. Build

- [ ] 2.1 Move the adapter to the new prefix with the same actions Check: `go test ./pkg/runner/... ./internal/api/...` passes
- [ ] 2.2 Write the audit entries of ADR 138 in each change transaction Check: `go test ./...` shows the seven audit events
- [ ] 2.3 Update ServedRoutes in e2e/coverage.go and the API description for the moved routes. BLOCKED by the real runner e2e step of the later parts, so only the route coverage check runs here Check: `make e2e` route coverage check lists the new paths

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire the routes with their authorization actions Check: The route test shows each action
- [ ] 4.2 Write the docs page for pool, drain, retire and join-token management Check: Docs commands run as written against a dev server
- [ ] 4.3 Run the usability check Check: A runner operator completes pool and token work from the docs page; each error names one command
- [ ] 4.4 Pass the full check Check: `make check` reports zero findings
