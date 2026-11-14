## 1. Dependencies

- [x] 1.1 PR #2981 (`SignInFromBody`, internal/sessionauth/sessionauth.go:189) and PR #2968 (the static provider and the route) are merged to `main`. Acceptance path: BLOCKED by PR #2968, which is not on main; the route is `POST /api/dustpan/v1/sign-in/providers/{provider}/password` with the LDAP user name and password. Check: `git log main` holds both, and `make e2e -run TestLDAPSignIn` passes on `main`. Confirmed by: CI (the agent writes the e2e test).

## 2. The lockout setting

- [ ] 2.1 In NEW file cmd/opendustpan/ldap_settings.go, add `ldap.subjectFailureLimit` as static configuration: flag, `DUSTPAN_LDAP_*` variable and file key, 5 by default, 1 to 100, outside the range stops the start. Check: `go test ./cmd/opendustpan/ ./internal/cli/` with a table of values 0, 1, 100, 101, a text and an empty value.
- [ ] 2.2 In internal/signinsvc/service_test.go (test) and NEW file cmd/opendustpan/ldap_provider_list.go, pass the limit to `signinsvc` and show its effective value and source in `GET /api/server/sign-in-providers`. Check: a test locks a subject after exactly the limit, and the list shows the value and the source.
- [ ] 2.3 Regenerate the API description and the clients (`make generate`). It writes internal/api/apidesc/openapi.json and the clients under clients/. Check: `make clients-check` and `sqlc diff` are clean. Confirmed by: the driver's gates.

## 3. Verification

- [ ] 3.1 In NEW file e2e/ldap_sign_in_test.go, E2E: route `POST /api/dustpan/v1/sign-in/providers/{provider}/password` with the LDAP user name and password credential: sign-in over TLS; plain connection refused with the opt-in off, allowed with it on, refused again with it off. Check: `make e2e` runs `TestLDAPSignIn` on SQLite and PostgreSQL. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 In internal/ldapauth/hostile_test.go and NEW file cmd/opendustpan/ldap_hostile_test.go, hostile tests for the settings, the connection, the method and the handler, and one test per guard that fails when the guard is removed. Check: `go test -race ./cmd/opendustpan/ ./pkg/dustpan/ ./internal/ldapauth/ ./internal/signinsvc/ ./internal/api/server/`.
- [ ] 3.3 Update `docs/guides/ldap-sign-in.md`: the lockout setting, its risk, and "groups are read and not applied". Check: `make docs-lint` exits 0.
- [ ] 3.4 In docs/guides/ldap-sign-in.md, usability check: a person follows the guide alone against the test directory and signs in. Each error they cause names the setting or the one command that helps. Check: the steps and the errors are in the pull request description. A person's step.
- [ ] 3.5 For the files of this change (docs/guides/ldap-sign-in.md, internal/ldapauth, internal/signinsvc), `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
