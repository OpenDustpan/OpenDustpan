## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests for first enrollment with code, step-up restart and refusal shape Check: `go test ./internal/mfasvc -run TestTotpEnrollWithCode -count=1` fails on main with no factor routes
- [ ] 1.2 Add failing test for single-use TOTP and WebAuthn challenges across restart Check: `go test ./internal/mfasvc -run TestReplayedCodeFails -count=1` fails on main
- [ ] 1.3 Add failing test for the SSH-signature fresh-step factor path Check: `go test ./internal/mfasvc -run TestSshSignatureFreshStep -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Add factor enrollment, removal, step-up and enrollment-code routes with actions and policy checks in internal/mfasvc Check: `GET /api/dustpan/v1/me/factors` lists enrolled factors with action me:read-factors and passes the route test
- [ ] 2.2 Reuse the local-user MFA factor code for check and audit entries Check: `go test ./internal/mfasvc -count=1` passes with TOTP, passkey and SSH paths sharing one code path with existing /me/passkeys routes
- [ ] 2.3 Add the installation require-second-factor setting and the administrator enrollment-code issue route with audit entries Check: `PATCH /api/dustpan/v1/settings` sets require-second-factor-for-plain-oauth with action server:manage-sign-in and passes the route test
- [ ] 2.4 Cover restore into a new installation with an undecryptable factor seal and re-enrollment Check: `go test ./internal/mfasvc -run TestRestoreLeavesFactorsUnreadable -count=1` passes on SQLite and PostgreSQL

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring, route with authorization actions Check: Route table lists each new route with its action; public list unchanged
- [ ] 4.2 E2e step against the real server on both dialects Check: `make e2e` OAuth factor step with new routes `POST /api/dustpan/v1/me/factors/totp/enroll` and `POST /api/dustpan/v1/me/factors/step-up` passes on SQLite and PostgreSQL over TLS through the real binary
- [ ] 4.3 Docs page and usability check with the persona path alone Check: Docs page commands run as written and each error names the one command that helps
- [ ] 4.4 Full check with zero findings Check: `make check` reports zero findings
