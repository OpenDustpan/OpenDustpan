## 1. Dependencies

- [x] 1.1 PR #2981 (`SignInFromBody`) and PR #2968 (the static provider and the route) are merged to `main`. Check: `git log main` holds both, and `make e2e -run TestLDAPSignIn` passes on `main`.

## 2. The lockout setting

- [ ] 2.1 Add `ldap.subjectFailureLimit` as static configuration: flag, `DUSTPAN_LDAP_*` variable and file key, 5 by default, 1 to 100, outside the range stops the start. Check: `go test ./cmd/opendustpan/ ./internal/cli/` with a table of values 0, 1, 100, 101, a text and an empty value.
- [ ] 2.2 Pass the limit to `signinsvc` and show its effective value and source in `GET /api/server/sign-in-providers`. Check: a test locks a subject after exactly the limit, and the list shows the value and the source.
- [ ] 2.3 Regenerate the API description and the clients (`make generate`). Check: `make clients-check` and `sqlc diff` are clean.

## 3. Verification

- [ ] 3.1 E2E: sign-in over TLS; plain connection refused with the opt-in off, allowed with it on, refused again with it off. Check: `make e2e` runs `TestLDAPSignIn` on SQLite and PostgreSQL.
- [ ] 3.2 Hostile tests for the settings, the connection, the method and the handler, and one test per guard that fails when the guard is removed. Check: `go test -race ./cmd/opendustpan/ ./pkg/dustpan/ ./internal/ldapauth/ ./internal/signinsvc/ ./internal/api/server/`.
- [ ] 3.3 Update `docs/guides/ldap-sign-in.md`: the lockout setting, its risk, and "groups are read and not applied". Check: `make docs-lint` exits 0.
- [ ] 3.4 Usability check: a person follows the guide alone against the test directory and signs in. Each error they cause names the setting or the one command that helps. Check: the steps and the errors are in the pull request description.
- [ ] 3.5 `make check` reports zero findings.
