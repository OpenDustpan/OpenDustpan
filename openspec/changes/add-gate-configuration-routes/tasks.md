## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing route tests in NEW file internal/api/server/gate_config_routes_test.go: list, read, save, delete, 404 on another scope, 403 without action, at four scopes. Check: TestGateConfigRoutes fails on main
- [ ] 1.2 Write failing tests in NEW file pkg/gate/gatetest/unitkey_test.go for the stable unit key: reused name, rename, restore, unknown unit. Check: TestGateRowsKeyOnUnitID fails on main
- [ ] 1.3 Write failing tests in NEW file internal/api/server/gate_config_guards_test.go for If-Match, limits, rule ID grammar, 4 KiB condition, final gates and locks held. Check: TestGateConfigWriteGuards fails on main
- [ ] 1.4 Write failing tests in NEW file internal/api/server/gate_config_fresh_test.go for fresh authentication with an access token and an old session, and for the audit entry in the same transaction. Check: TestGateConfigFreshAndAudit fails on main
- [ ] 1.5 Write failing tests in NEW file internal/api/server/gate_config_default_test.go for the approval-default reset (ConfigService.DeleteApprovalDefault at pkg/gate/config.go:183) and the carry-over default. Check: TestApprovalDefaultReset fails on main

## 2. Implementation

- [ ] 2.1 Verify only: the gate store transaction in pkg/store/internal/gatestore/settings.go:129 and the gate tables in the baseline of each dialect under pkg/store/<dialect>/migrations/. Edit nothing unless the unit key test fails. Check: `go test -timeout 60m ./pkg/store/...` on SQLite and PostgreSQL
- [ ] 2.2 Add the installation rows of design decision 6 (gates, access limits, approval rules, signature carry-over) to the route table: a fourth scope in `gateObjectScopes` (internal/api/server/gate_objects.go:66-72) with the base `/api/dustpan/v1` and the infix `Installation`, and the NEW actions `installation:read-gates` and `installation:manage-gates` in pkg/identity/identity.go. The task adds the NEW top-level test `TestGateConfigRoutes` in a `_test.go` file of `internal/api/server`, so that the Check runs it. Check: `go test ./internal/api/server/... -run 'TestGateConfigRoutes' -count=1` passes for the installation rows
- [ ] 2.5 Add the approval-rule rows of the collection and the unit scope, and the approval-default rows of each scope where `Scope.HoldsApprovalDefault` is true, in internal/api/server/gate_config.go (paths at :87-89). The task adds the NEW top-level test `TestApprovalDefaultReset` in a `_test.go` file of `internal/api/server`, so that the Check runs it. Check: `go test ./internal/api/server/... -run 'TestGateConfigRoutes|TestApprovalDefaultReset' -count=1` passes
- [ ] 2.6 Add the NEW interface `CarryOverConfig` (CarryOver, PutCarryOver; design decision 7) to `GateConfig` (internal/api/server/gate_config.go:26), serve GET and PUT signature-carry-over at the four scopes in the NEW file internal/api/server/carry_over.go, and implement it in the adapter pkg/dustpan/gateconfig.go:45 (func gatesPort) over the store methods of pkg/gate/store.go:154-164. Add CarryOver and PutCarryOver to the fake coverageGateConfig in e2e/coverage_gate.go:16, or the build of the end-to-end suite fails. Join the new apidesc labels into joinLabels in internal/api/server/operations.go (:187-198). Check: the route and policy tests pass, including TestActionsWithoutRoutesAreUnclassified
- [ ] 2.3 Verify only: the backup table list in pkg/backup holds the gate rows. Edit nothing unless the round-trip test fails. Check: the backup round-trip test passes on both dialects
- [ ] 2.4 Regenerate the API description with `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` (it rewrites internal/api/apidesc/openapi.json), then the clients with `make generate` (it rewrites the files under clients/typescript/). Check: both commands leave no diff and `make clients-check` passes. Confirmed by: the driver's gates.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Write the NEW reference page docs/reference/gate-configuration.md. Check: `make docs-lint`
- [ ] 4.2 Add the e2e step in NEW file e2e/gate_config_test.go. It calls GET /api/dustpan/v1/namespaces/{namespace}/gates and GET /api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}/gates/{id} with the real CLI client and a personal access token of a namespace administrator. It reads an ID of another scope for 404, and reuses a deleted unit name for clean rows. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run the usability check from the docs page alone and confirm each error names its command. Check: the docs page walk-through passes. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
