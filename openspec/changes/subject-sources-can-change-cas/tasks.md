## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing tests of the save scenarios (allowed and mutable sources, partial PATCH) in pkg/session. Check: `go test ./pkg/session/...` fails on main
- [ ] 1.2 Write failing tests of the missing-subject and fail-closed scenarios in internal/casauth, internal/ldapauth, internal/oauth and the sign-in service. Check: `go test ./internal/casauth/... ./internal/ldapauth/... ./internal/oauth/...` fails on main

## 2. Adapters

- [ ] 2.1 Add the subject allowlist for CAS, LDAP and OAuth and the NEW error code `sign-in.subject-mutable` in internal/errcode/catalog_signin.go. Check: `go test ./internal/casauth/... ./internal/ldapauth/... ./internal/oauth/...`
- [ ] 2.2 Refuse plain header mode and run the check at provider load in pkg/session. Check: `go test ./pkg/session/...`

## 3. Definition of done

- [ ] 3.1 Update the sign-in guides, add an e2e step through `PATCH /api/server/sign-in-providers/{provider}` with a site administrator access token, and run the usability check. Confirmed by: CI (`make e2e` and `make check` with zero findings). Check: `make e2e` and `make check` with zero findings
