## 1. Failing tests first

- [ ] 1.1 Add table-driven failing tests for protected, trusted, full-sync, closed, closed-plus-trusted, and closed-plus-full-sync groups for sources scim, claim, and ldap, plus unmapped values and repeat adds. Check: `go test ./pkg/principal -run TestApplyIdentityProviderMemberships -count=1` fails on main.
- [ ] 1.2 Add failing tests for audit entries on refused adds and accepted changes with recorded source, provider or token ID, and refusing or allowing rule, and for audit failure with no membership. Check: `go test ./pkg/principal -run TestIdentityProviderAudit -count=1` fails on main.
- [ ] 1.3 Add failing route tests that each trusted provider, claim mapping, and account matching change declares server:manage-sign-in and needs a fresh step inside the one window with the shortest effective window, including aged and no-factor refusals and audit-failure 500. Check: `go test ./internal/api/... -run TestIdpConfigAction -count=1` fails on main.
- [ ] 1.4 Add failing race test for a concurrent group close, trust removal, or full-sync switch against an in-flight add. Check: `go test ./pkg/principal -run TestIdentityProviderRace -count=1` fails on main.

## 2. Implementation

- [ ] 2.1 Implement the one membership function with sentinel error, closed-wins precedence, trust and full-sync exceptions, source recording for scim, claim, and ldap, idempotent repeat, and one-transaction audit. Check: `go test ./pkg/principal/... -count=1` passes.
- [ ] 2.2 Wire WARN plus audit alerts for configuration changes and SCIM token expiry at 168 hours with injected logger and clock, including expired tokens, no-expiry tokens, re-armed expiry, and audit-failure 500 mapping. Check: `go test ./internal/... -run Alert -count=1` passes.
- [ ] 2.3 Add handler contract tests for SCIM error versus claim skip and prepare the test identity provider harness without requiring the missing adapters. Check: `go test ./internal/api/... -run TestIdpMappingContract -count=1` passes; full SCIM and claim e2e stays blocked until those adapters land.

## 3. Definition of done

- [ ] 3.1 Write docs page docs/guides/idp-membership-and-alerts.md for refusal behavior and the alert channel, and update the protected-groups guide sentence named in guide-implies-protected-groups-refuse to state the enforced refusal. Check: `test -f docs/guides/idp-membership-and-alerts.md` and each command on the page runs as written.
- [ ] 3.2 Run full checks with wiring, routes, contract tests, docs page, and usability check from the docs page alone. Check: `make check` reports zero findings.
