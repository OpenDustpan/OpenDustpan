---
version: 2
---

Roadmap: `docs/roadmap.md:149` (MFA).

## Why

**Persona: developer.** A developer shares an install with other people and CI pipelines. The developer signs in with a local or LDAP account, proves identity with a second factor, creates a token, and runs the Pulumi CLI and OpenTofu with least access.

The user path this change must make work:

1. **Docs page.** Turn on require MFA in the docs page and enroll TOTP or a passkey.
2. **API.** Sign in with password, then give the second factor to get a session.
3. **CLI.** Create an access token and run pulumi up and tofu apply with that token.
4. **API.** Reveal a secret value; a stale step asks for a fresh authentication first.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Wrong password at local or LDAP sign-in | 401 {"code": 401, "message": "The user name or the password is not valid. Run `opendustpan status` and check the server address."} | `opendustpan status` |
| Wrong TOTP code or failed WebAuthn second factor | 401 {"code": 401, "message": "The second factor is not valid. Run `opendustpan status` and sign in again."} | `opendustpan status` |
| Token creation or secret reveal with an old MFA step | 403 {"code": "mfa.fresh-step-required", "message": "A fresh authentication is required. Run `opendustpan status` and start a new sign-in."} | `opendustpan status` |

Evidence for the claims above:

- MFA domain code exists in pkg/mfa and internal/mfasvc. Proof: pkg/mfa, internal/mfasvc, pkg/mfa/mfatest
- LDAP sign-in has an e2e test and a guide. Proof: e2e/ldap_signin_test.go and docs/guides/ldap-sign-in.md
- Local user sign-in has e2e tests and guides. Proof: e2e/localusers_test.go, e2e/passkeys_test.go, docs/guides/local-users.md, docs/guides/passkeys.md
- No e2e test names MFA, TOTP, or SSO in this pack. Proof: MFA e2e_count 0, TOTP e2e_count 0, SSO e2e_count 0

## What Changes

- Require a TOTP or WebAuthn second factor for local and LDAP password sign-in when require MFA is on.
- Keep SSO sign-in with no added built-in second factor.
- Require a fresh MFA step for access token creation and secret reveal inside one window.

Out of scope:

- OIDC, SAML, CAS, OAuth 2.0, trusted-header, and SCIM behavior
- Break-glass mode 2
- Token expiry email and SMTP settings
- mTLS workload sign-in
- Collections and placement
- Recovery when no administrator exists, which uses the break-glass recovery modes
- Fresh-window range, 0 behavior, and no-factor refusal wording, owned by fresh-authentication-window-0-turns and reused here through the shared check
- App-level second factor details beyond the one factor store, specified in app-level-second-factor-totp and first-step-up-key-in

Depends on:

- ldap-sign-in
- add-one-generic-settings-api
- fresh-authentication-window-0-turns
- `add-one-generic-settings-api` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A person who loses all factors waits for an administrator reset.
- A stale fresh step interrupts token creation and forces a new sign-in.
- A person with no administrator uses the break-glass recovery path for access.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests local password routes for sign-in (`pkg/dustpan/localusers_test.go:203`)
- Main tests the freshness hook for access tokens (`pkg/accesstoken/accesstoken_test.go:779`)
- Main tests that a secret reveal needs a fresh authentication (`pkg/profile/profiletest/service_test.go:157`)
- Main tests WebAuthn gates with the resolved source address (`pkg/dustpan/passkey_source_test.go:104`)
- Main has a second-factor WebAuthn test without user verification (`pkg/mfa/gowebauthn/gowebauthn_test.go:375`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (changed), 3 (extended), 4 (current), 5 (current), 6 (extended), 7 (current), 8 (extended), 9 (current).
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "Built-in MFA. An install that signs people in with no SSO identity provider (local users, LDAP) can use built-in MFA."
- Quote: "With an SSO identity provider, MFA is the job of that provider. OpenDustpan adds no MFA step of its own on top (ADR 6)."
- Quote: "Two kinds of second factor, of the same rank. WebAuthn (passkeys, Touch ID, Windows Hello, hardware keys) and TOTP. A person can use either."
- Quote: "Recovery codes. The server shows recovery codes one time, at enrollment."
- Quote: "Off by default, for any number of users. An administrator turns MFA on. Then the administrator can require it for all users."
- Quote: "What MFA covers. Console sign-in, the creation of access tokens, and the reveal of a secret value (ADR 41)."
- Quote: "Access tokens stay bearer tokens. A CLI or CI access token needs no MFA step on each use. A person creates a CLI access token after an MFA sign-in, and the token has an expiry (RFC 20)."
- Quote: "Reset. An administrator can reset the MFA of a user. Each reset writes an audit entry (ADR 41)."
- Effect: The change applies built-in MFA to local and LDAP sign-in, keeps SSO without added step, keeps bearer tokens, shows recovery codes once, keeps the separate reveal setting, and keeps an administrator reset path.
- Conflict: none.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 10 (current).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "MFA follows ADR 46. With an SSO identity provider (SAML, CAS, OAuth), MFA is the job of that provider. For local users and LDAP, built-in MFA applies."
- Effect: The change keeps SSO without built-in second factor and applies built-in MFA to local users and LDAP.
- Conflict: none.

**ADR 56: MFA freshness for token creation and secret reveal** (not owner-tagged).

- Decisions that apply: 1 (extended), 4 (changed), 5 (extended), 6 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 3 and 4; ADR 71 extends; ADR 212 extends decision 1; ADR 222 supersedes decisions 2 and 3; ADR 227 changes decision 4; ADR 227 supersedes decisions 2 and 3; ADR 301 extends decision 5. Chain to the end: ADR 69, ADR 70, ADR 71, ADR 212, ADR 222, ADR 227, ADR 301, ADR 297, ADR 142, ADR 210, ADR 216, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 232, ADR 235, ADR 365, ADR 286, ADR 335, ADR 351, ADR 339, ADR 333, ADR 149, ADR 247, ADR 273, ADR 295, ADR 288.
- Current rule: this ADR, with ADR 69, 70, 71, 212, 222, 227, 301.
- Quote: "A freshness window. Token creation and secret reveal need an MFA step that is younger than the freshness window. When the last MFA step is older, the server asks for a new MFA step before it acts."
- Quote: "Only where MFA applies. The window applies when built-in MFA covers the operation (ADR 46). With MFA off, this ADR adds no step."
- Quote: "API. Each setting has a REST API and an action (ADR 17)."
- Effect: The change requires a fresh step for token creation, token rotation, and secret reveal inside the one window, only where built-in MFA covers the operation, with error code mfa.fresh-step-required. Decisions 2 and 3 of this ADR are superseded by ADR 227.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2, 3).

- Decisions that apply: 1 (extended), 2 (current), 3 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication. A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71). It is fresh when its age is not above the window."
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes). Each step that an ADR names as "fresh MFA", "a fresh MFA step" or "a fresh authentication" uses it: token creation and rotation, a secret reveal, the changes of ADR 70 decisions 16"
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step. No value turns the check off. The other values are 60 to 900 seconds. A window of 0 is the strictest value, so a change to 0 needs no fresh authentication. A change to a longer window needs one, as ADR"
- Effect: The change uses the one fresh-authentication window and the 403 error for token creation, token rotation, and reveal.
- Conflict: none.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 20 (changed), 21 (changed).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333.
- Quote: "A passkey or TOTP as the second factor. When built-in MFA is on (ADR 46), a password sign-in makes a pending sign-in, not a session (RFC 21 P7). The person then gives a passkey (user verification `preferred`), a TOTP cod"
- Quote: ""Require MFA" applies to the password (owner, through RFC 21 P7). The installation setting `mfa.requireForAll` makes each password sign-in ask for a second factor. While an SSO provider exists and `signIn.localPassword.e"
- Effect: A password sign-in creates a pending sign-in and requires a passkey or TOTP second factor when require MFA applies.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 12 (current), 20 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "One session generation for each person. Each principal has a session generation, a count that starts at 1. Each cookie carries the generation that the issue read. A cookie authenticates and refreshes only while its gener"
- Quote: "Sign-in paths. - **MFA.** A pending sign-in is a sealed cookie (decisions of the owner, item 1). It cannot authenticate a request. The factor lock of the person counts the wrong factors (ADR 285 decision 14). A good fact"
- Effect: A pending sign-in cannot authenticate a request, wrong factors count under the factor lock, and the generation check stays.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current), 4 (current), 11 (current), 12 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The step belongs to one sign-in (PM). The server records the fresh step in the sealed access cookie of the sign-in that did it, and in no other sign-in. The kind of the step is one of the kinds of ADR 297 decision 1 (`pa"
- Quote: "Only when MFA is on (PM). ADR 56 decision 5 holds. On an installation with MFA off, the server asks for no fresh step, as for every other method. The rules of this ADR apply when MFA is on."
- Effect: The fresh step is bound to the sealed access cookie of one sign-in; applies only when MFA is on.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 17).

- Decisions that apply: 16 (current), 17 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "LDAP is a password method (owner, ADR 285 decision 21). For the fresh step, an LDAP person follows ADR 285 decision 21, as a local password user does. A password alone is a fresh authentication only for a person with no"
- Effect: An LDAP person is treated like a local password user for the fresh step.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add e2e step against the real CLI with require MFA on." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: the state diagram after this change: `NoSession` does not resolve on main; use the name main has, or mark it NEW.

Questions considered and answered:

- Does an SSO sign-in ask for a built-in second factor? No. ADR 46 decision 2 and ADR 55 decision 10 leave MFA to the SSO provider.
- Which operations need a fresh MFA step? Token creation, rotation and reveal, per ADR 46 decision 6, ADR 56 decisions 1 and 5 and ADR 227 decision 2. Bearer token use needs none (ADR 46 decision 8).
- Which second factors satisfy sign-in? WebAuthn and TOTP, equal rank (ADR 46 decision 3); passkey verification preferred (ADR 285 decision 20).
- What happens to a password sign-in when require MFA is on? It creates a pending sign-in that cannot authenticate (ADR 285 decisions 20, 21; ADR 297 decision 20).
- Which values does this change set? None new. It reuses pkg/mfa and the failure counts of ADR 285 decision 14.
- How do lost factors work? Recovery codes shown once and administrator reset with audit (ADR 46 decisions 4 and 9); notice and generation bump (ADR 301 decision 10).

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
