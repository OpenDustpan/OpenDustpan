## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing route tests in NEW file internal/api/server/gate_config_routes_test.go: list, read, save, delete, 404 on another scope, 403 without action, at four scopes. Check: TestGateConfigRoutes fails on main
- [ ] 1.2 Write failing tests in NEW file pkg/gate/gatetest/unitkey_test.go for the stable unit key: reused name, rename, restore, unknown unit. Check: TestGateRowsKeyOnUnitID fails on main
- [ ] 1.3 Write failing tests in NEW file internal/api/server/gate_config_guards_test.go for If-Match, limits, rule ID grammar, 4 KiB condition, final gates and locks held. Check: TestGateConfigWriteGuards fails on main
- [ ] 1.4 Write failing tests in NEW file internal/api/server/gate_config_fresh_test.go for fresh authentication with an access token and an old session, and for the audit entry in the same transaction. Check: TestGateConfigFreshAndAudit fails on main
- [ ] 1.5 Write failing tests in NEW file internal/api/server/gate_config_default_test.go for the approval-default reset (ConfigService.DeleteApprovalDefault at pkg/gate/config.go:183) and the carry-over default. Check: TestApprovalDefaultReset fails on main

## 2. Implementation

- [ ] 2.1 Verify only: the gate store transaction in pkg/store/internal/gatestore/settings.go:129 and the gate tables in the baseline of each dialect under pkg/store/<dialect>/migrations/. Edit nothing unless the unit key test fails. Check: `go test ./pkg/store/...` on SQLite and PostgreSQL
- [ ] 2.2 Add the routes: declare them in internal/api/server/gate_config.go:24 (GateConfig port) and serve them from the adapter in pkg/dustpan/gateconfig.go:45 (func gatesPort), with their authorization actions at the four scopes. Check: the route and policy tests pass, including TestActionsWithoutRoutesAreUnclassified
- [ ] 2.3 Verify only: the backup table list in pkg/backup holds the gate rows. Edit nothing unless the round-trip test fails. Check: the backup round-trip test passes on both dialects
- [ ] 2.4 Regenerate the API description and the clients with the generate target (Makefile:335). It rewrites internal/api/apidesc/openapi.json and the files under clients/typescript/. Check: `make generate` leaves no diff and `make clients-check` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the NEW reference page docs/reference/gate-configuration.md. Check: `make docs-lint`
- [ ] 4.2 Add the e2e step in NEW file e2e/gate_config_test.go. It calls GET /api/dustpan/v1/namespaces/{namespace}/gates and GET /api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}/gates/{id} with the real CLI client and a personal access token of a namespace administrator. It reads an ID of another scope for 404, and reuses a deleted unit name for clean rows. Check: `make e2e`
- [ ] 4.3 Run the usability check from the docs page alone and confirm each error names its command. Check: the docs page walk-through passes
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings
