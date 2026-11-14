## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for first enrollment with a code, step-up and refusal shape Check: `go test ./internal/mfasvc -run TestTotpEnrollWithCode -count=1` (NEW test) fails on main with no factor routes
- [ ] 1.2 Add failing test for single-use TOTP and WebAuthn challenges across restart Check: `go test ./internal/mfasvc -run TestReplayedCodeFails -count=1` (NEW test) fails on main
- [ ] 1.3 Add failing test for the SSH-signature fresh-step path Check: `go test ./internal/mfasvc -run TestSshSignatureFreshStep -count=1` (NEW test) fails on main

## 2. Implementation

- [ ] 2.1 Add factor enrollment, removal, step-up and enrollment-code routes with actions in internal/mfasvc Check: `GET /api/dustpan/v1/me/factors` lists factors with action me:read-factors and passes the route test
- [ ] 2.2 Reuse the local-user MFA factor code for check and audit entries Check: `go test ./internal/mfasvc -count=1` passes with TOTP, passkey and SSH sharing one code path
- [ ] 2.3 Add the installation setting and the enrollment-code issue route with audit entries Check: `PATCH /api/dustpan/v1/settings` sets require-second-factor-for-plain-oauth with action server:manage-sign-in
- [ ] 2.4 Cover restore with an undecryptable factor seal and re-enrollment Check: `go test ./internal/mfasvc -run TestRestoreLeavesFactorsUnreadable -count=1` (NEW test) passes on SQLite and PostgreSQL

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring and route with authorization actions Check: Route table lists each new route with its action; public list unchanged
- [ ] 4.2 E2e step with the real Pulumi CLI and the real OpenTofu CLI on both dialects Check: `make e2e` step uses `POST /api/dustpan/v1/me/factors/totp/enroll` and `POST /api/dustpan/v1/me/factors/step-up` with a personal access token of a plain OAuth person; BLOCKED by the mfa change that ships the factor store
- [ ] 4.3 Docs page and usability check Check: Docs page commands run as written and each error names the one command that helps
- [ ] 4.4 Full check Check: `make check` reports zero findings
