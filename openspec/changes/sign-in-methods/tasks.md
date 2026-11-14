## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write the failing e2e for SAML sign-in. BLOCKED by the missing SAML adapter and test identity provider; it will use the existing browser routes `GET /api/server/sign-in/{}` and `GET /api/server/sign-in/{}/callback` with a session cookie, then a personal access token. Check: e2e TestSamlSpSignIn fails on main
- [ ] 1.2 Write failing tests for the SAML refusals: bad signature, unpinned certificate, wrong issuer, unknown provider, IdP-started with wrong audience, wrong recipient, age over 5 minutes, and a valid IdP-started assertion. Check: TestSamlRefusals and TestSamlIdpStartedChecks fail on main
- [ ] 1.3 Write failing tests for header proxy gate, spoofed X-Forwarded-For, IPv6 and CIDR match, and bad signature. Check: TestTrustedHeaderProxyGate, TestTrustedHeaderSpoofedForwardedFor and TestTrustedHeaderBadSignature fail on main
- [ ] 1.4 Write failing tests for replay of OAuth state, CAS ticket and request IDs, and for store outage and disabled person on each browser path. Check: TestBrowserReplayRefused and TestBrowserStoreOutageFailsClosed fail on main
- [ ] 1.5 Write failing matrix e2e. It uses the existing `POST /api/dustpan/v1/sign-in/passkey/begin` and `POST /api/dustpan/v1/sign-in/passkey/finish` with a passkey credential for the passkey method, and the browser routes with a session cookie for the others. Check: e2e TestSignInMatrix fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Route actions, docs pages, usability walk. Check: `make check` has zero findings
