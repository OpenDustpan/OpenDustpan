## 1. Failing tests first

- [ ] 1.1 Write failing tests for each route, the 404 on another scope, the reuse of a name, rename, restore, limits, errors, If-Match and audit. Check: TestGateConfigRoutes fails on main
- [ ] 1.2 Write failing tests for a write without fresh authentication and for two writes with one version. Check: TestGateConfigWriteGuards fails on main

## 2. Implementation

- [ ] 2.1 Add the routes and the stable-ID key. Check: `go test ./pkg/gate/... ./internal/api/server/...` on SQLite and PostgreSQL
- [ ] 2.2 Declare the actions of each route and pass the route test. Check: the route and policy tests pass

## 3. Definition of done

- [ ] 3.1 Write the reference page and add the e2e step. Check: `make docs-lint` and `make e2e`
- [ ] 3.2 Run the usability check and the full check. Check: `make check` reports zero findings
