## 1. Failing tests

- [ ] 1.1 Add failing API test for local password sign-in with no second factor. Check: TestMfaLocalPasswordNeedsSecondFactor fails on main.
- [ ] 1.2 Add failing API test for LDAP password sign-in with no second factor. Check: TestMfaLdapPasswordNeedsSecondFactor fails on main.
- [ ] 1.3 Add failing API tests for stale fresh step on POST /api/dustpan/v1/me/tokens, token rotation, and secret reveal with code mfa.fresh-step-required. Check: TestMfaFreshStepForTokenRotationAndReveal fails on main.
- [ ] 1.4 Add failing API tests for enrollment recovery codes shown once and administrator reset. Check: TestMfaEnrollReset fails on main.

## 2. Wiring

- [ ] 2.1 Wire pending sign-in, TOTP and WebAuthn verification, and session issue in the one factor store. Check: POST /api/dustpan/v1/sign-in/password then second-factor issues a session; SSO sign-in issues a session with no second factor.
- [ ] 2.2 Register require MFA and reveal settings through the settings API of add-one-generic-settings-api. Check: GET settings resource shows mfa.requireForAll and the separate secret-reveal MFA setting with effective values through the settings API.
- [ ] 2.3 Wire administrator reset path with authorization action. Check: Administrator reset clears factors, writes an audit entry, and sends a notice.
- [ ] 2.4 Wire audit entries for MFA enrollment, use, and reset. Check: Each enrollment, second-factor use, and reset writes the audit entry of the sign-in service.

## 3. Definition of done

- [ ] 3.1 Add e2e step against the real CLI with require MFA on. Check: `make e2e TEST=local-mfa-require` signs in a local user and an LDAP user with TOTP, runs pulumi up and tofu apply, and checks audit entries.
- [ ] 3.2 Add user docs for MFA enrollment, sign-in, and recovery codes. Check: Docs page commands run as written in CI.
- [ ] 3.3 Run usability check. Check: Developer completes sign-in from the docs alone; each error names the one command that helps.
- [ ] 3.4 Pass definition of done. Check: `make check` passes with zero findings.
