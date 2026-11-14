## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add table-driven failing tests for protected, trusted, full-sync, closed, closed-plus-trusted and closed-plus-full-sync groups for each source, plus unmapped values and repeat adds. Check: `go test ./pkg/principal -run TestApplyIdentityProviderMemberships -count=1` fails on main.
- [ ] 1.2 Add failing audit tests for refused and accepted changes, audit failure and local-source retention. Check: `go test ./pkg/principal -run TestIdentityProviderAudit -count=1` fails on main.
- [ ] 1.3 Add failing route tests for server:manage-sign-in and the fresh step. Check: `go test ./internal/api/... -run TestIdpConfigAction -count=1` fails on main.
- [ ] 1.4 Add a failing race test for a concurrent group close or trust change. Check: `go test ./pkg/principal -run TestIdentityProviderRace -count=1` fails on main.

## 2. Implementation

- [ ] 2.1 Implement the one membership function with atomic PATCH and per-operation Bulk status. Check: `go test ./pkg/principal/... -count=1` passes.
- [ ] 2.2 Wire the audit plus WARN alerts and the 168-hour token expiry check with injected logger and clock. Check: `go test ./internal/... -run Alert -count=1` passes.
- [ ] 2.3 Add conformance tests for each new port method on both store adapters. Check: `go test ./pkg/principal/... ./pkg/store/... -run Conformance -count=1` passes on SQLite and PostgreSQL.
- [ ] 2.4 Add the new actions to the sensitive action list. Check: `rg -n server:manage-sign-in` lists them.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write docs/guides/idp-membership-and-alerts.md and update the protected-groups guide sentence. Check: Each command on the page runs as written.
- [ ] 4.2 Full e2e of a refused SCIM add and a refused claim. Check: BLOCKED by the SCIM adapter and the sign-in claim adapters, which are not on main.
- [ ] 4.3 Run the full checks and the usability check from the docs page alone. Check: `make check` reports zero findings.
