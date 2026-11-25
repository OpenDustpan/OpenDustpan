## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for first enrollment with a code, step-up and refusal shape in internal/mfasvc/service_test.go Check: `go test ./internal/mfasvc -run TestTotpEnrollWithCode -count=1` (NEW test) fails on main with no factor routes
- [ ] 1.2 Add failing test for single-use TOTP and WebAuthn challenges across restart in internal/mfasvc/challengesource_test.go Check: `go test ./internal/mfasvc -run TestReplayedCodeFails -count=1` (NEW test) fails on main
- [ ] 1.3 Add failing test for the SSH-signature fresh-step path in NEW file internal/mfasvc/sshstep_test.go Check: `go test ./internal/mfasvc -run TestSshSignatureFreshStep -count=1` (NEW test) fails on main
- [ ] 1.4 Add failing test for the start state in NEW file internal/mfasvc/enrollonly_test.go: with `mfa.enabled` on and a required factor (`mfa.requireForAll` on, or an SSO provider and `signIn.localPassword.enabled` on), a correct password of a person with no factor at POST /api/dustpan/v1/sign-in/password gets (a) inside `mfa.enrollmentGraceDays` a session from the password alone with no fresh step, and (b) after `mfa.enrollmentGraceDays` a session whose cookie has the restriction `enroll-only`, which answers 200 on GET /api/dustpan/v1/me/factors and on POST /api/dustpan/v1/me/factors/totp/enroll, and answers 403 with error code mfa.enrollment-required on POST /api/dustpan/v1/me/tokens. Rows for a person with a factor (pending sign-in, as in the mfa change) and for `mfa.enabled` off (no restriction) keep the mfa change result. Check: `go test ./internal/mfasvc -run TestEnrollOnlySessionAfterGrace -count=1` (NEW test) fails after the mfa change, where `NeedsSecondFactor` in internal/mfasvc/policy.go gives a person with no factor and a required factor no session

## 2. Implementation

- [ ] 2.1 Add factor enrollment, removal, step-up and enrollment-code routes with actions in internal/mfasvc Check: `GET /api/dustpan/v1/me/factors` lists factors with action me:read-factors and passes the route test
- [ ] 2.2 Reuse the local-user MFA factor code for check and audit entries in NEW file internal/mfasvc/factorservice.go Check: `go test ./internal/mfasvc -count=1` passes with TOTP, passkey and SSH sharing one code path
- [ ] 2.3 Add the installation setting and the enrollment-code issue route with audit entries in NEW file internal/mfasvc/enrollcode.go Check: `PATCH /api/dustpan/v1/settings` sets require-second-factor-for-plain-oauth with action server:manage-sign-in
- [ ] 2.4 Cover restore with an undecryptable factor seal and re-enrollment in internal/mfasvc/service_test.go Check: `go test ./internal/mfasvc -run TestRestoreLeavesFactorsUnreadable -count=1` (NEW test) passes on SQLite and PostgreSQL

- [ ] 2.5 SECURITY. Add the boundary check for factor type, base32 TOTP secret shape and WebAuthn origin and RP ID in NEW file internal/mfasvc/boundary.go Check: `go test ./internal/mfasvc -run TestBoundaryRejectsHostileInput -count=1` (NEW test) passes
- [ ] 2.6 SECURITY. Add hostile test TestBoundaryRejectsHostileInput in NEW file internal/mfasvc/boundary_test.go that sends an unknown factor type, a malformed secret and a cross-origin response straight to the handler Check: `go test ./internal/mfasvc -run TestBoundaryRejectsHostileInput -count=1` (NEW test) expects 400 and no state change
- [ ] 2.7 SECURITY. Add hostile test TestFreshStepOfOtherSignInRefused in NEW file internal/mfasvc/freshstep_test.go that creates a token with the fresh step of another sign-in Check: `go test ./internal/mfasvc -run TestFreshStepOfOtherSignInRefused -count=1` (NEW test) expects 403 with error code mfa.fresh-step-required
- [ ] 2.8 SECURITY. Replace the closed failure of the mfa change for a person with no factor and a required factor. Add function `EnrollOnlyDecision` in NEW file internal/mfasvc/enrollonly.go that reads `mfa.enrollmentGraceDays` through `mfa.PolicySource` and answers, for the sign-in gate in internal/mfasvc/signin_pending.go (created by the mfa change), a session with no restriction inside the grace, or a session with `session.RestrictionEnrollOnly` after it. The first enrollment of a factor bumps the session generation and sets new cookies with no restriction. A policy read error stays closed: no session and an audit entry. Check: `go test ./internal/mfasvc -run TestEnrollOnlySessionAfterGrace -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wiring and route with authorization actions Check: Route table lists each new route with its action; public list unchanged
- [ ] 4.2 E2e step with the real Pulumi CLI and the real OpenTofu CLI on both dialects Check: `make e2e` step uses `POST /api/dustpan/v1/me/factors/totp/enroll` and `POST /api/dustpan/v1/me/factors/step-up` with a personal access token of a plain OAuth person; BLOCKED by the mfa change that ships the factor store.
- [ ] 4.3 Docs page and usability check Check: Docs page commands run as written and each error names the one command that helps.
- [ ] 4.4 Full check Check: `make check` reports zero findings.
