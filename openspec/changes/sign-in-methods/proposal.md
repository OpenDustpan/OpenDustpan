---
version: 6
---

Roadmap: `docs/roadmap.md:141` (Sign-in methods).

## Why

**Persona: developer.** A developer wants to sign in with the identity provider of their team, open a session, make an access token, and run `pulumi up` and `tofu apply` with that token.

The user path this change must make work:

1. **Docs page.** Read the sign-in docs page for the method and its provider settings.
2. **Console.** Sign in in the browser with OIDC, LDAP, CAS, plain OAuth, trusted header, or a passwordless passkey.
3. **API.** Create a personal access token from the session with `POST /api/dustpan/v1/me/tokens` and export it for the CLIs.
4. **CLI.** Run `pulumi up` and `tofu apply` with the token.
5. **E2e.** Run the matrix of the six methods over TLS against the real binary, with MFA off, with test identity providers, and check the audit entries.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A browser sign-in callback fails a check: a used state, a used CAS ticket, a user-info answer without `sub`, a CAS answer without the subject attribute. | 400 with the one generic text "sign-in failed: start the sign-in again" (exists on main). The cause is in the audit log. | `curl -s $D/api/server/sign-in-providers` |
| A trusted header comes from an address that is not on the proxy list of the provider. | 401 with code `sign-in.untrusted-proxy` (NEW in this change). | `curl -s $D/api/server/sign-in-providers` |

Evidence for the claims above:

- LDAP sign-in has an e2e test and a guide. Proof: e2e/ldap_signin_test.go (TestLDAPSignIn) and docs/guides/ldap-sign-in.md
- OIDC and plain OAuth browser sign-in have an e2e test and a guide. Proof: e2e/signin_test.go (TestSignIn; the plain OAuth provider at e2e/signin_test.go:815) and docs/guides/console-sign-in.md
- Passwordless passkey sign-in has e2e tests and a guide. Proof: e2e/passkeysignin_test.go, e2e/passkeys_test.go, docs/guides/passkeys.md
- CAS has an adapter and a guide, but no setting, no route and no e2e test. Proof: internal/casauth/casauth.go, docs/guides/cas-sign-in.md:8-10, and no CAS test in e2e/
- Trusted header has an adapter and a guide, but no setting and no route. Proof: internal/headerauth/headerauth.go, docs/guides/trusted-header-sign-in.md:9-10, and pkg/session/providerconfig.go:179-183

## What Changes

- Plain OAuth 2.0 takes the subject from `sub` only. The configurable subject claim goes away. A user-info answer without `sub` is refused with the audit reason `sign-in.subject-missing` (NEW code). The browser answer stays the generic answer of main (pull request 1).
- CAS takes the subject from a named attribute only, and a CAS provider can be stored, listed and used for a browser sign-in (pull request 2).
- Trusted header keeps the `jwt` mode only: the `plain` mode goes away. It stays off by default, with a proxy list for each provider. A request from a peer outside the list gives 401 `sign-in.untrusted-proxy` and an audit entry with the TCP peer address. A header provider can be stored, listed and used for a browser sign-in (pull request 3).
- Add the e2e matrix of the six methods, with MFA off (pull request 4).

Out of scope:

- SAML sign-in: the change saml-sign-in. It waits for the owner's decision on an XML-DSig dependency.
- MFA enrollment, freshness windows and the factor refusal for OAuth and trusted-header persons (app-level-second-factor-totp). The matrix runs with MFA off.
- SCIM provisioning and LDAP sync schedules
- Group mapping rules. The CAS and header settings of this change have no groups attribute and no groups header, so these methods give no group claim.
- IP allowlists and token life cycle
- Signed-request recovery (change signed-request-recovery)
- A new one-time ID store. Main has a store for each one-time ID of the methods in this change (see "What main already does").

Depends on:

- none. add-one-generic-settings-api is on main (openspec/changes/archive/2026-11-22-add-one-generic-settings-api).

## Usability

Where the path still costs the person time or surprises them:

- Each browser sign-in error gives one generic answer; the cause is in the audit log.
- A plain OAuth provider whose user-info answer has no `sub` cannot sign anyone in. The GitHub user API, which the console guide uses as its example (docs/guides/console-sign-in.md:146), sends `id` and no `sub`, so that example stops working. The guide names the claim and uses a provider that sends `sub`.
- A CAS provider needs a subject attribute. The CAS guide says which attribute stays the same when a person is renamed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error code `sign-in.failed` exists. (`internal/errcode/catalog_signin.go:8`, the list of codes in `internal/errcode/testdata/catalog/sign-in.txt`)
- The browser callback answers each refusal with the generic `errSignInFailed`: 400 and no code. (`internal/api/server/signin.go:61`, `internal/api/server/signin.go:484 (answerSignInError)`, `internal/api/server/signin.go:739-740 (classifySignIn)`)
- Plain OAuth reads the subject from a claim name in the provider settings, with the default `sub`. It never falls back to `name`, `preferred_username` or `email`; those claims fill the display fields only. (`internal/oauth/plain.go:25-52 (Claims, WithDefaults)`, `internal/oauth/plain.go:269-284 (identity)`, `pkg/session/provideroauth.go:80`, `internal/api/server/providers.go:85-92 (providerClaims)`)
- A user-info answer without a usable subject gives `oauth.ErrUserInfo`. The method wraps it in `session.ErrMethodAnswer`, and the sign-in service records the reason "identity provider answer failed a check". (`internal/oauth/plain.go:21-23`, `internal/oauth/method.go:347-365 (methodError)`, `internal/signinsvc/refuse.go:131-138 (answerReason)`, `internal/signinsvc/service.go:71`)
- An untrusted peer maps to the reason "request does not come from a trusted proxy". The audit entry has no code and no peer address. (`internal/signinsvc/refuse.go:143-144`, `internal/signinsvc/refuse.go:50-84 (record)`)
- The audit entry of a browser sign-in holds the client address from `clientip.String`, not the TCP peer. (`internal/api/server/signin.go:574-586 (signInEntry)`)
- The header adapter checks the TCP peer against the proxy list of the provider and does not read a forwarded header for that check. It has a `jwt` mode and a `plain` mode. (`internal/headerauth/headerauth.go:30-39`, `internal/headerauth/headerauth.go:213-218 (New)`, `internal/headerauth/headerauth.go:341-350`, `internal/headerauth/plain.go`)
- The CAS adapter uses the CAS user when no subject attribute is set. (`internal/casauth/casauth.go:91-94`, `internal/casauth/assertion.go:18`, `internal/casauth/assertion.go:98-104 (attrOrUser)`)
- Neither adapter is wired: the provider settings refuse the modes, the store allows `oidc` and `oauth` only, and the build of a stored provider refuses the other modes. (`pkg/session/providerconfig.go:179-183`, `internal/store/sqlite/migrations/0168_baseline.sql:482`, `internal/store/postgres/migrations/0168_baseline.sql:1442`, `pkg/dustpan/storedproviders.go:217`)
- The callback never fills the CAS ticket. (`internal/api/server/signin.go:544 (parseCallback)`)
- The one-time IDs of these methods already have stores. The OAuth and OIDC state is a sign-in request that works once, with an expiry and a prune. (`internal/store/sqlite/migrations/0168_baseline.sql:386-394`, `pkg/session/service.go:257 (BeginRequest)`, `pkg/session/service.go:280 (TakeRequest)`, `pkg/session/service.go:291 (PruneRequests)`) A CAS ticket is recorded as used, with a one-hour life. (`internal/store/sqlite/migrations/0168_baseline.sql:1655-1662`, `pkg/session/usedtickets.go:14`, `pkg/session/usedtickets.go:24-33`) A failed take of the request, a store error included, refuses the callback. (`internal/api/server/signin.go:531-535`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 3 (current), 6 (extended), 7 (extended).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 369, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 370.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "Trusted-header sign-in is off by default. The operator turns it on and names the proxies that may set the header. The server ignores the header from any other source."
- Effect: Trusted header stays off by default; the server issues the session and the token (decision 3). For plain OAuth, the claim mapping of decision 7 stays for the display claims only; the subject is `sub`.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 29 (changed by the owner decision of 13 Nov 2026), 35 (current), 36 (changed by the owner decision of 13 Nov 2026), 37 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Each header provider has its own list of proxies (PM)."
- Effect: Each header provider has its own stored list. The list is a sensitive setting with an alert (ADR 70 decision 22). A refused header gets an audit entry (decision 37).
- Conflict: decision 29 (PM) says "With no attribute, the subject is the CAS `user`", and decision 36 (PM) keeps a `plain` mode. The owner decision of 13 Nov 2026 (queue P20) refuses both: "CAS needs an attribute, no plain header mode". An owner decision wins over a PM decision, so this change follows the owner decision. The ADR 321 text waits for the owner, as the owner decision says.

ADR 332 decisions 1 to 5 are about SAML only, and decisions 6 to 8 are about group mapping rules. Both topics are out of scope, so ADR 332 does not apply to this change.

## Open questions

none

Questions considered and answered:

- Does each method need its own token type? No. ADR 55 decision 3.
- Is a plain header or a mutable subject safe? No. Owner decision, 13 Nov 2026 (queue P20): refuse mutable subject sources.
- Does the matrix run with MFA on? No. With MFA off no fresh step applies (ADR 301 decision 12); the MFA path belongs to another change.
- Does the browser answer name the missing `sub`? No. The answer stays the generic answer of main; the code `sign-in.subject-missing` goes to the audit entry only.
- Does this change need a new one-time ID store? No. Main has the sign-in request store and the used-ticket store. SAML needs a store of assertion IDs, and SAML is out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
