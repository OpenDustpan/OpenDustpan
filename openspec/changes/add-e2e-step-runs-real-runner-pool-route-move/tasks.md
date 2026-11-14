## 1. Failing tests first

- [ ] 1.1 Add failing route tests for list, read, create, change, delete, token list, token create and token revoke on the new paths Check: TestPoolRoutes_ListReturnsPoolsWithoutSecrets fails on main
- [ ] 1.2 Add failing route tests that the old pool paths answer 404 Check: TestPoolRoutes_OldPathsAreUnknown fails on main
- [ ] 1.3 Add failing service tests that each mutation stores its audit entry in the same transaction Check: TestPoolRoutes_AuditEntriesStoredInTransaction fails on main
- [ ] 1.4 Add failing tests for 401 with no credential and 403 with no grant on each pool route Check: TestPoolRoutes_AuthRefusals fails on main

## 2. Build

- [ ] 2.1 Move the adapter to /api/dustpan/v1/namespaces/{namespace}/runner-pools with actions namespace:read-runners and namespace:manage-runners Check: `go test ./pkg/runner/... ./internal/api/...` passes
- [ ] 2.2 Write the audit entries of ADR 138 in the transaction of each change Check: `go test ./...` shows audit entries pool-created, pool-changed, pool-deleted, join-token-created, join-token-revoked, runner-drained and runner-retired
- [ ] 2.3 Update the served routes list and the API description; the live runner run stays out of scope Check: `make e2e` route coverage passes with the new paths listed

## 3. Definition of done

- [ ] 3.1 Wire the routes with authorization actions and the public list unchanged Check: Each management route shows its action in the route test
- [ ] 3.2 Write the docs page for pool and join token management Check: Docs page commands run as written against a dev server
- [ ] 3.3 Run the usability check Check: Runner operator completes pool and token work from the docs page alone, and each error names the one command that helps
- [ ] 3.4 Pass the full check with zero findings Check: `make check` reports zero findings
