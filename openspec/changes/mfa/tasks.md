## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing test for a pending sign-in on local password in NEW file internal/mfasvc/signin_pending_test.go. Route: POST /api/dustpan/v1/sign-in/password, credential kind: local password with no session cookie. Check: TestMfaLocalPasswordNeedsSecondFactor fails on main.
- [ ] 1.2 Write failing test for a stale step on token creation in NEW file pkg/accesstoken/fresh_step_test.go. Route: POST /api/dustpan/v1/me/tokens, credential kind: session cookie with an old MFA step. Check: TestMfaFreshStepForTokenAndReveal fails on main.
- [ ] 1.3 Write failing test for VerifySecondFactor (NEW) with a replayed code in NEW file internal/mfasvc/verify_second_factor_test.go. Route: POST /api/dustpan/v1/sign-in/password, then the second factor step, credential kind: pending sign-in with a TOTP code. Check: TestVerifySecondFactorRefusesReplay fails on main.

## 1b. Build and hostile tests

- [ ] 1.4 SECURITY. Build the service MFA gate in NEW file internal/mfasvc/signin_pending.go: the pending sign-in type, VerifySecondFactor (NEW) with single-use TOTP codes, and the cookie-bound fresh step. Check: TestMfaLocalPasswordNeedsSecondFactor and TestVerifySecondFactorRefusesReplay pass.
- [ ] 1.5 SECURITY. Write a hostile handler test for malformed factor input in NEW file internal/mfasvc/handler_input_test.go. Route: POST /api/dustpan/v1/sign-in/password, then the second factor step, credential kind: pending sign-in with a malformed code. Check: TestSecondFactorHandlerRefusesMalformedCode passes and a malformed code gets a refusal and no session.

## 2. Side effects (computed from main)

- [x] 2.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 3. Definition of done

- [ ] 3.1 Add e2e step with the real Pulumi CLI and OpenTofu in NEW file e2e/mfa_test.go. Route: POST /api/dustpan/v1/me/tokens, credential kind: session cookie with a fresh MFA step. Check: `make e2e` passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Add docs page in NEW file docs/guides/mfa.md and run usability check. Check: Docs commands run as written. A person's step. `make docs-lint` passes on each edited guide.
- [ ] 3.3 Pass the full check. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
