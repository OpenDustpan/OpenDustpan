## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing API test for local password sign-in that creates a pending sign-in with no session. Check: TestMfaLocalPasswordNeedsSecondFactor fails on main.
- [ ] 1.2 Add failing API test for LDAP password sign-in with a pending sign-in. Check: TestMfaLdapPasswordNeedsSecondFactor fails on main.
- [ ] 1.3 Add failing test for SSO with no second factor and refused local password. Check: TestMfaSsoNeedsNoSecondFactor fails on main.
- [ ] 1.4 Add failing tests for stale fresh step on token creation, rotation and reveal, window 0, access token. Check: TestMfaFreshStepForTokenRotationAndReveal fails on main.
- [ ] 1.5 Add failing tests for enrollment, recovery codes, removal and reset. Check: TestMfaEnrollReset fails on main.
- [ ] 1.6 Add failing test for mfa.requireForAll and the reveal setting. Check: TestMfaRequireForAllAndRevealSetting fails on main.

## 2. Wiring

- [ ] 2.1 Wire pending sign-in, TOTP and WebAuthn verification and session issue. Check: POST /api/dustpan/v1/sign-in/password then second factor issues a session.
- [ ] 2.2 Register require MFA and reveal settings through the settings API. Check: GET settings shows mfa.requireForAll with its effective value.
- [ ] 2.3 Wire administrator reset with its authorization action. Check: Reset with installation:manage-sign-in clears factors and writes an audit entry.
- [ ] 2.4 Wire audit entries for enrollment, use, removal and reset. Check: Each of these writes an audit entry.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step against the real CLI with require MFA on. Check: `make e2e TEST=local-mfa-require` runs pulumi up and tofu apply with the token and checks audit entries.
- [ ] 4.2 Add user docs for MFA. Check: Docs commands run as written in CI.
- [ ] 4.3 Run usability check. Check: Developer completes sign-in from the docs alone; each error names one command.
- [ ] 4.4 Pass definition of done. Check: `make check` passes with zero findings.
