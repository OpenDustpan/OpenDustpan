## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for each route and scope: list, read, save, delete, 404 on another scope, 403 without action. Check: TestGateConfigRoutes fails on main
- [ ] 1.2 Write failing tests for the stable unit key: reused name, rename, restore, unknown unit. Check: TestGateRowsKeyOnUnitID fails on main
- [ ] 1.3 Write failing tests for If-Match, limits, rule ID grammar, 4 KiB condition, final gates and locks held. Check: TestGateConfigWriteGuards fails on main
- [ ] 1.4 Write failing tests for fresh authentication with an access token and with an old session, and for the audit entry in the same transaction. Check: TestGateConfigFreshAndAudit fails on main
- [ ] 1.5 Write failing tests for the approval-default reset and the carry-over setting default. Check: TestApprovalDefaultReset fails on main

## 2. Implementation

- [ ] 2.1 Edit the baseline of each dialect in place so unit gate rows use the stable unit ID. Add no numbered migration. Check: `go test ./pkg/store/...` on SQLite and PostgreSQL
- [ ] 2.2 Add the routes with their authorization actions at the four scopes. Check: the route and policy tests pass
- [ ] 2.3 Add the gate configuration tables to the table list for backup and restore. Check: the backup round-trip test passes on both dialects
- [ ] 2.4 Regenerate the API description and the clients. Check: `make generate` leaves no diff

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the reference page in docs/reference. Check: `make docs-lint`
- [ ] 4.2 Add the e2e step: with the real CLI client and a personal access token of a namespace administrator, call GET /api/dustpan/v1/namespaces/{namespace}/gates, read an ID of another scope for 404, and reuse a deleted unit name for clean rows. Check: `make e2e`
- [ ] 4.3 Run the usability check from the docs page alone and confirm each error names its command. Check: the docs page walk-through passes
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings
