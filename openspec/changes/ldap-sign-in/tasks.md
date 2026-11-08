## 1. Provider settings

- [ ] 1.1 Add `session.LDAPSettings` and the mode `ldap` to `ProviderConfig.Validate` with an allowlist. Check: `go test ./pkg/session/...` including a field-list test against `ldapauth.Config` and a table of hostile settings.
- [ ] 1.2 Store and read the settings in both dialects (edit the baseline in place, run `make generate`). Check: `go test -race -timeout 60m ./pkg/store/...` and `sqlc diff` is clean.

## 2. Wiring

- [ ] 2.1 Build one `ldapauth.Method` for each enabled provider in `cmd/opendustpan` and give the set to `signinsvc` with `Locks` and `NameKeys`. Check: a test that starts the server with an LDAP provider and reaches the method.
- [ ] 2.2 Read `egress.allowPlainConnections` on each connection through a reader. Check: a test that flips the setting between two connections.

## 3. Route

- [ ] 3.1 Add the sign-in route for a password method (path from open question 1) with declared actions and the public-list entry. Check: the route test passes and the API description is regenerated (`make generate`).
- [ ] 3.2 One `401` answer for each failure. Check: a table test of the handler for each failure in the spec.

## 4. Verification

- [ ] 4.1 E2E: sign-in over TLS; sign-in over a plain connection with the opt-in on; refusal with the opt-in off. Check: `make e2e` runs the three steps against a test directory.
- [ ] 4.2 Hostile tests for the settings, the connection, the method and the handler, and a test per guard that fails when the guard is removed. Check: `go test -race ./internal/ldapauth/... ./internal/signinsvc/... ./internal/api/...`.
- [ ] 4.3 Update `docs/guides/ldap-sign-in.md`. Check: the usability check below.
- [ ] 4.4 Usability check: a person follows the guide alone against the test directory and signs in. Each error they cause names the one command that helps.
- [ ] 4.5 `make check` reports zero findings; `make docs-lint` passes.
