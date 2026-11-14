## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write tests for validation, TLS refusal, subject allowlist, immutable URL, delete and uniqueness. Check: NEW test `TestLDAPProviderConfig` in pkg/session: `go test ./pkg/session -run TestLDAPProviderConfig -count=1` fails on main
- [ ] 1.2 Write tests for group claims, removal counting, protected groups, absent user, disabled user, referral, empty password and limits. Check: NEW test `TestLDAPGroupClaims` in internal/signinsvc: `go test ./internal/signinsvc -run TestLDAPGroupClaims -count=1` fails on main
- [ ] 1.3 Write tests for the static binding, concurrent first start, lost row, rebind action, audit entry and session revocation. Check: NEW test `TestStaticLDAPBinding` in internal/api/server: `go test ./internal/api/server -run TestStaticLDAPBinding -count=1` fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the routes with actions, audit entries and hostile tests per layer. Check: `go test ./... -run LDAPProvider -count=1`
- [ ] 3.2 Add the real LDAP directory fixture to e2e. Add an e2e step that calls the new routes /api/dustpan/v1/sign-in/ldap-providers with the access token of a site administrator, signs in through POST /api/dustpan/v1/sign-in/providers/{provider}/password, and runs the real CLIs with a personal token. Check: `make e2e` runs the LDAP steps
- [ ] 3.3 Write the guide page and run the usability check from the page alone. Check: `make docs-lint` and `make check` report zero findings
