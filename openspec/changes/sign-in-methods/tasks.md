## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the failing e2e for SAML sign-in in NEW file e2e/saml_signin_test.go. BLOCKED by the missing SAML adapter and test identity provider; it will use the existing browser routes `GET /api/server/sign-in/{}` and `GET /api/server/sign-in/{}/callback` (declaration file internal/api/server/signin.go, adapter file NEW internal/signin/saml/saml.go) with a session cookie, then a personal access token. Check: e2e TestSamlSpSignIn fails on main
- [ ] 1.2 Write failing tests in NEW file internal/signin/saml/saml_test.go for the SAML refusals: bad signature, unpinned certificate, wrong issuer, unknown provider, IdP-started with wrong audience, wrong recipient, age over 5 minutes, and a valid IdP-started assertion. Check: TestSamlRefusals and TestSamlIdpStartedChecks fail on main
- [ ] 1.3 Write failing tests in NEW file internal/signin/header/header_test.go for header proxy gate, spoofed X-Forwarded-For, IPv6 and CIDR match, and bad signature. Check: TestTrustedHeaderProxyGate, TestTrustedHeaderSpoofedForwardedFor and TestTrustedHeaderBadSignature fail on main
- [ ] 1.4 Write failing tests in NEW file internal/api/server/signin_browser_test.go for replay of OAuth state, CAS ticket and request IDs, and for store outage and disabled person on each browser path. Check: TestBrowserReplayRefused and TestBrowserStoreOutageFailsClosed fail on main
- [ ] 1.5 Write failing matrix e2e in NEW file e2e/signin_matrix_test.go. It uses the existing `POST /api/dustpan/v1/sign-in/passkey/begin` and `POST /api/dustpan/v1/sign-in/passkey/finish` with a passkey credential for the passkey method, and the browser routes `GET /api/server/sign-in/{}` and `GET /api/server/sign-in/{}/callback` with a session cookie for the others. Check: e2e TestSignInMatrix fails on main

## 1b. Build tasks

- [ ] 1.6 SECURITY. Build the SAML adapter in NEW file internal/signin/saml/saml.go. It checks the signature against pinned certificates, issuer, audience, recipient and age, and it refuses an unpinned or wrapped signature. Check: TestSamlRefusals and TestSamlWrappedSignature pass
- [ ] 1.7 SECURITY. Build the trusted-header adapter in NEW file internal/signin/header/header.go. It checks the signed token and the socket peer address against the proxy list of the provider, and it never reads X-Forwarded-For. Check: TestTrustedHeaderProxyGate and TestTrustedHeaderSpoofedForwardedFor pass
- [ ] 1.8 SECURITY. Build the CAS adapter in NEW file internal/signin/cas/cas.go. It checks the ticket and takes the subject from a named attribute. Check: TestCasHostileInputs passes
- [ ] 1.9 SECURITY. Build the plain OAuth adapter in NEW file internal/signin/oauth/oauth.go. It checks the state and takes the identity from `sub` only. Check: TestOAuthHostileInputs passes
- [ ] 1.10 SECURITY. Build the one-time ID store in NEW file internal/signin/onetimeid/store.go. It has a unique key, an expiry, deletion of expired rows and a size bound, and it fails closed. Check: TestOneTimeIdStoreBound and TestOneTimeIdStoreFull pass
- [ ] 1.11 SECURITY. Write hostile tests for the CAS and OAuth adapters in NEW file internal/signin/cas/cas_test.go and NEW file internal/signin/oauth/oauth_test.go. They send a replayed ticket, a replayed state and a mutable subject straight to the adapter. Check: TestCasHostileInputs and TestOAuthHostileInputs pass
- [ ] 1.12 SECURITY. Write hostile tests for the one-time ID store in NEW file internal/signin/onetimeid/store_test.go. They send a duplicate ID, an expired ID, a too long ID and a flood straight to the store. Check: TestOneTimeIdStoreBound, TestOneTimeIdStoreFull and TestOneTimeIdLengthBound pass

## 2. Side effects (computed from main)

- [x] 2.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 3. Definition of done

- [ ] 3.1 Route actions, docs pages, usability walk. Check: `make check` has zero findings. Confirmed by: CI (the agent runs the fast checks only)
