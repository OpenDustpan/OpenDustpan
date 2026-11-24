## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for a direct grant, a group grant and a member add, each with a covering and a non-covering caller, in the NEW file pkg/access/cover_grant_test.go. Check: go test ./pkg/access/... ./pkg/principal/... fails on main
- [ ] 1.2 Write failing tests for the protecting grant: no identity provider members, members with no trust decision, members with a trust decision, in the NEW file pkg/access/protect_grant_test.go. Check: go test ./pkg/access/... fails on main
- [ ] 1.3 Write failing tests for flags: growth by grant, role change, member add and principal grant; acknowledge; revoke; regrowth; end with grant, principal, group or membership; list filtered by reach, in the NEW file pkg/access/flagged_edges_test.go. Check: go test ./pkg/access/... fails on main
- [ ] 1.4 Write failing conformance tests for concurrent grant, member add and revoke on both adapters, in the NEW conformance file pkg/store/storetest/cover_concurrent.go, which the adapter test files of internal/store/sqlite and internal/store/postgres run. Check: go test -timeout 60m ./pkg/store/... fails on main
- [ ] 1.5 Write failing tests for token create and rotation to a protected member, a member of a group that becomes protected later, and an unprotected member, in the existing file pkg/accesstoken/accesstoken_test.go. Check: go test ./pkg/accesstoken/... fails on main
- [ ] 1.6 Write a failing test that account-administrator reach holds no operator-family action, in the NEW file pkg/access/account_admin_reach_test.go. Check: go test ./pkg/access/... fails on main

## 2. Implementation

- [ ] 2.1 Add the cover proof, the checks, the flag record and its end rule in pkg/access/service.go, pkg/principal/service.go (AddMember, line 496) and pkg/accesstoken. Check: go test ./pkg/access/... ./pkg/principal/... ./pkg/accesstoken/... passes
- [ ] 2.2 Map the sentinels in internal/api/server/tokens.go and add the NEW flag routes with their actions and the API description. Declare the routes in the NEW file internal/api/server/flagged_edges.go and the declaration in the route table of internal/api/server (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand). Join the new apidesc labels into joinLabels in internal/api/server/operations.go (:187-198), the one join point of the labels. Check: go test ./internal/api/server/... passes

## 3. Store adapters and backup

- [ ] 3.1 Add the flag record to both adapters. Create the NEW files internal/store/sqlite/flags.go and internal/store/postgres/flags.go with CreateFlag, the acknowledge, the revoke, the list filtered by reach and the end rule, each in the transaction of its write. Add the flag table in name order to internal/store/sqlite/migrations/0168_baseline.sql and internal/store/postgres/migrations/0168_baseline.sql, and the flag queries in name order to internal/store/sqlite/queries.sql and internal/store/postgres/queries.sql. Regenerate the generated query code with `make generate`; never edit it by hand. Check: go test -timeout 60m ./internal/store/sqlite/... ./internal/store/postgres/... -run 'TestAccessConformance' passes
- [ ] 3.2 Write the audit entry of each flag set, acknowledge, revoke and end in the same transaction as the flag change, in internal/store/sqlite/audit.go and internal/store/postgres/audit.go. Check: go test -timeout 60m ./pkg/store/... passes
- [ ] 3.3 End the flags of a revoked edge in the revocation sweep: in pkg/maintenance/reducing.go, and in internal/store/sqlite/maintenance_revocations.go and internal/store/postgres/maintenance_revocations.go. Cover the sweep with a case in pkg/store/storetest/maintenance_revocations.go and in pkg/store/storetest/delete_grants_audit.go (a deleted grant ends its flag and writes one audit entry). Check: go test ./pkg/maintenance/... passes
- [ ] 3.4 Put the flag table in the backup and in the restore: pkg/backup/access_tables.go, pkg/backup/backup.go, pkg/backup/portable_tables.go, internal/store/sqlite/backup_columns.go and internal/store/postgres/backup.go. Record each column of the new table in pkg/backup/devstrip_coverage_test.go (strip or keep, with a reason). Check: go test ./pkg/backup/... passes
- [ ] 3.5 Add the move cases of the flag to pkg/access/accesstest/moves.go, and the concurrent grant, member add and revoke cases to the NEW file pkg/store/storetest/cover_concurrent.go. Check: go test -timeout 60m ./pkg/store/... ./pkg/access/... passes
- [ ] 3.6 List the NEW docs page docs/guides/act-as-reach-and-flagged-edges.md in docs/commands-pending-pages.txt. Check: make docs-lint passes

## 4. Side effects (computed from main)

- [ ] 4.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 4.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 5. Definition of done

- [ ] 5.1 Add e2e steps in the NEW file e2e/cover_flagged_edges_test.go with a site administrator access token, through the existing routes POST /api/server/grants, POST /api/server/groups/{id}/members and POST /api/server/principals/{id}/tokens, and the NEW routes GET /api/dustpan/v1/flagged-edges with acknowledge and revoke. The routes are declared in internal/api/server/operations.go and internal/api/server/flagged_edges.go (NEW), and the adapter files are internal/api/server/tokens.go and internal/api/server/flagged_edges.go (NEW). BLOCKED by the groups-roles-grants change for the reach function. Check: make e2e passes.
- [ ] 5.2 Write the NEW docs page docs/guides/act-as-reach-and-flagged-edges.md about act-as reach and flagged edges, and walk the user path from it. Check: make docs-lint passes
- [ ] 5.3 Run the full check. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only)
