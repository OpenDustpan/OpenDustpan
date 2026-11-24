## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Conformance suites and failing tests

- [ ] 1.1 Write the conformance cases of the pending sign-in and of the recovery codes in NEW file pkg/mfa/mfatest/pending.go and replace the skip in `Run` in pkg/mfa/mfatest/suite.go. The cases are the names in `CaseNames`. Check: `go test ./pkg/mfa/mfatest -count=1` passes with a fake store that the suite test holds, and each case fails against a store that breaks its rule.
- [ ] 1.2 Write the conformance case of the new TOTP port method in internal/stepup/stepuptest/suite.go before any adapter implements it. The cases: a higher step passes and sets the last step; an equal or lower step fails with `stepup.ErrUsed`; a factor that is not active fails with `stepup.ErrNotFound`; the failure count, the lock and the disable of the step-up stay as they were; two calls at the same time with one step: one wins. Check: `go test ./internal/stepup/stepuptest -count=1` fails on a store that counts a step-up failure.
- [ ] 1.3 Write failing test for a pending sign-in on local password in NEW file internal/mfasvc/signin_pending_test.go. Route: POST /api/dustpan/v1/sign-in/password with require MFA on and no session cookie. Check: TestMfaLocalPasswordNeedsSecondFactor fails on main.
- [ ] 1.4 Write failing test for a stale step on token creation in NEW file pkg/accesstoken/fresh_step_test.go. Route: POST /api/dustpan/v1/me/tokens, credential kind: session cookie with an old MFA step. Check: TestMfaFreshStepForTokenAndReveal fails on main.
- [ ] 1.5 Write failing test for a replayed TOTP code on a pending sign-in in NEW file internal/mfasvc/second_factor_test.go. Route: POST /api/dustpan/v1/sign-in/second-factor, credential kind: pending sign-in with a TOTP code. Check: TestSecondFactorRefusesReplay fails on main.
- [ ] 1.6 Write failing test for a step-up lockout that does not block a sign-in in internal/mfasvc/second_factor_test.go. Check: TestStepUpLockoutNeverBlocksSignIn fails on main.

## 2. Adapters

- [ ] 2.1 Add the new method to `stepup.TOTPStore` in internal/stepup/stepup.go. The method accepts a time step for a sign-in, with the single-use rule of `AcceptStep`, and touches no step-up counter. Check: `go build ./...` passes.
- [ ] 2.2 Add the tables of the pending sign-in, the recovery codes and the TOTP factor in name order to internal/store/sqlite/migrations/0168_baseline.sql and internal/store/postgres/migrations/0168_baseline.sql. The store keeps digests and the sealed secret, never a code. Check: `go test ./internal/store/sqlite -run TestBaseline -count=1` passes.
- [ ] 2.3 Add the queries in name order to internal/store/sqlite/queries.sql and internal/store/postgres/queries.sql and run `make generate`. Check: `sqlc diff` is clean.
- [ ] 2.4 Write the SQLite adapters in NEW files internal/store/sqlite/mfa_pending.go, internal/store/sqlite/mfa_recovery.go and internal/store/sqlite/totp.go, and wire them to the suites in NEW file internal/store/sqlite/mfa_test.go. Check: `go test ./internal/store/sqlite -run TestMFA -timeout 60m` passes.
- [ ] 2.5 Write the PostgreSQL adapters in NEW files internal/store/postgres/mfa_pending.go, internal/store/postgres/mfa_recovery.go and internal/store/postgres/totp.go, and wire them to the suites in NEW file internal/store/postgres/mfa_test.go. Check: `go test ./internal/store/postgres -run TestMFA -timeout 60m` passes.

## 3. The gate and the route

- [ ] 3.1 SECURITY. Build the service MFA gate in NEW file internal/mfasvc/signin_pending.go: it creates `mfa.Pending` on a correct local or LDAP password when require MFA is on, and it turns the pending sign-in into a session only through `PendingStore.CompletePending` (`pkg/mfa/mfa.go:315`) after a valid second factor. A TOTP code goes through the new single-use method of the TOTP port. A recovery code goes through `RecoveryCodeStore.UseRecoveryCode`. A passkey goes through the assertion ceremony with the pending ID in `Binding`. A wrong factor counts in `PendingStore.FailPending` and on no step-up counter. Do not add a pending type and do not add a second single-use rule. Check: TestMfaLocalPasswordNeedsSecondFactor, TestSecondFactorRefusesReplay and TestStepUpLockoutNeverBlocksSignIn pass.
- [ ] 3.2 Change the local sign-in in internal/localusersvc/signin.go and the directory sign-in in internal/api/server/passwordsignin.go so that a correct password with require MFA on makes a pending sign-in and no session. Check: the route test shows 202 and no session cookie.
- [ ] 3.3 Add the route POST /api/dustpan/v1/sign-in/second-factor in NEW file internal/api/server/secondfactor.go, with the body, the answers and the errors of the spec. Add the error code to internal/errcode/catalog_localuser.go. Check: the route test passes for each method.
- [ ] 3.4 SECURITY. Write the hostile handler test in NEW file internal/mfasvc/handler_input_test.go. Route: POST /api/dustpan/v1/sign-in/second-factor, credential kind: pending sign-in with a malformed code. Check: TestSecondFactorHandlerRefusesMalformedCode passes and a malformed code gets a refusal and no session.

## 4. Side effects (computed from main)

- [x] 4.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.
- [ ] 4.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand.

## 5. Definition of done

- [ ] 5.1 Add e2e step with the real Pulumi CLI and OpenTofu in NEW file e2e/mfa_test.go. The person signs in with a password, gives a registered passkey at POST /api/dustpan/v1/sign-in/second-factor, and creates a token at POST /api/dustpan/v1/me/tokens. Check: `make e2e` passes.
- [ ] 5.2 Add docs page in NEW file docs/guides/mfa.md and run usability check. Change the sentence in docs/guides/passkeys.md that says the server does not ask for a passkey as a second factor yet. Check: Docs commands run as written. `make docs-lint` passes on each edited guide.
- [ ] 5.3 Pass the full check. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only)
