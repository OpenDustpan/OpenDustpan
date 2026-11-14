## 1. Failing tests

- [ ] 1.1 Write the failing e2e for SP-started SAML. Check: e2e TestSamlSpSignIn fails on main
- [ ] 1.2 Write failing tests for plain OAuth `sub`, missing `sub`, user-info failure and the CAS plain-name refusal. Check: TestPlainOAuthSignIn, TestCasPlainNameRefused and TestCasPrincipalSource fail on main
- [ ] 1.3 Write failing tests for the trusted-header proxy gate, empty list, plain name and default off. Check: TestTrustedHeaderProxyGate, TestTrustedHeaderDefaultOff and TestTrustedHeaderConfigRefused fail on main
- [ ] 1.4 Write failing tests for SAML replay, expiry, store outage and IdP-started never fresh. Check: TestSamlReplayRefused, TestSamlExpiredAssertion, TestSamlStoreOutageFailsClosed and TestIdpStartedNeverFresh fail on main
- [ ] 1.5 Write failing tests for the refusal of group claims and the audit entries. Check: TestGroupClaimRefused and TestAuditEntryPerRefusal fail on main
- [ ] 1.6 Write the failing test of the disabled-person refusal. Check: TestDisabledPersonRefused fails on main
- [ ] 1.7 Write the failing matrix e2e for the seven methods. Check: e2e TestSignInMatrix fails on main

## 2. Build

- [ ] 2.1 Pull request 1: SAML adapter wired into the sign-in order. Check: `go test ./internal/signinsvc/...` passes
- [ ] 2.2 Pull request 2: OAuth and CAS subject rules. Check: `go test ./internal/oauth/... ./internal/casauth/...` passes
- [ ] 2.3 Pull request 3: trusted-header adapter. Check: `go test ./internal/signinsvc/...` passes
- [ ] 2.4 Pull request 4: matrix over OIDC, LDAP, SAML, CAS, OAuth, header and the shipped passkey sign-in, with both CLIs. Check: `make e2e` passes with audit checks

## 3. Definition of done

- [ ] 3.1 Route actions, docs page for each method, usability walk and full check. Check: route test passes; `make docs-lint`; `make check` has zero findings
