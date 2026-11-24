---
version: 10
---

Issue: #4064

Roadmap: `docs/roadmap.md:149` (MFA).

## Why

**Persona: developer.** A developer shares an install with other people and CI pipelines. The developer signs in with a local or LDAP account, proves identity with a second factor, creates a token, and runs the Pulumi CLI and OpenTofu with least access.

The user path this change must make work:

1. **Docs page.** Turn on require MFA and enroll TOTP or a passkey.
2. **API.** Sign in with password at POST /api/dustpan/v1/sign-in/password, then give the second factor at POST /api/dustpan/v1/sign-in/second-factor to get a session.
3. **API.** Create an access token at POST /api/dustpan/v1/me/tokens, then run pulumi up and tofu apply with it.
4. **API.** Reveal a secret value at POST /api/v2/state-versions/{id}/actions/reveal; a stale step asks for a fresh authentication first.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Wrong password at local or LDAP sign-in | 401 {"code": 401, "message": "The user name or the password is not valid. Run `opendustpan status` and check the server address."} | `opendustpan status` |
| Wrong TOTP code or failed WebAuthn second factor | 401 {"code": 401, "message": "The second factor is not valid. Run `opendustpan status` and sign in again."} | `opendustpan status` |
| Token creation or secret reveal with an old MFA step | 403 {"code": "mfa.fresh-step-required", "message": "A fresh authentication is required. Run `opendustpan status` and start a new sign-in."} | `opendustpan status` |

Evidence for the claims above:

- MFA domain code exists. Proof: pkg/mfa, internal/mfasvc, pkg/mfa/mfatest
- LDAP sign-in has an e2e test and a guide. Proof: e2e/ldap_signin_test.go and docs/guides/ldap-sign-in.md
- Local user sign-in has e2e tests and guides. Proof: e2e/localusers_test.go, e2e/passkeys_test.go, docs/guides/local-users.md, docs/guides/passkeys.md
- No e2e test names MFA, TOTP, or SSO in this pack. Proof: keyword evidence: MFA e2e_count 0, TOTP e2e_count 0, SSO e2e_count 0
- The token routes exist on main. Proof: route ANY /api/dustpan/v1/me/tokens in the pack main list

## What Changes

- Require a TOTP, recovery code or passkey second factor for local and LDAP password sign-in when require MFA is on.
- Add one route for the second factor of a pending sign-in, `POST /api/dustpan/v1/sign-in/second-factor`, with a method field.
- Add the SQLite and PostgreSQL adapters of `mfa.PendingStore`, `mfa.RecoveryCodeStore` and `stepup.TOTPStore`, with their conformance suites.
- Add one port method that accepts a TOTP time step for a sign-in and touches no step-up counter.
- Keep SSO sign-in with no added built-in second factor.
- Require a fresh MFA step for access token creation and secret reveal inside one window.

Out of scope:

- OIDC, SAML, CAS, OAuth 2.0, trusted-header, and SCIM behavior
- Signed-request recovery, and the reset of the factors of a person by a temporary administrator of a recovery with no fresh step (ADR 372 decision 6), owned by the signed-request-recovery change
- Token expiry email and SMTP settings
- mTLS workload sign-in
- Collections and placement
- Fresh-window range and no-factor refusal wording, owned by fresh-authentication-window-0-turns
- TOTP enrollment, recovery code issue and the other factor routes, owned by app-level-second-factor-totp, which depends on this change for the stores

Depends on:

- ldap-sign-in
- add-one-generic-settings-api
- fresh-authentication-window-0-turns

## Usability

Where the path still costs the person time or surprises them:

- A person who loses all factors waits for an administrator reset, or uses the break-glass path of the administrator.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests the freshness hook for access tokens (`pkg/accesstoken/accesstoken_test.go:779`)
- Main has the pending sign-in: `mfa.Pending` (`pkg/mfa/mfa.go:275`), `mfa.PendingStore` (`pkg/mfa/mfa.go:289`) with `CreatePending`, `FailPending` and `CompletePending` (`pkg/mfa/mfa.go:315`). This change adds no second pending type.
- Main has the passkey sign-in ceremony: `FinishSignIn` (line 374) and the pending ID in `Binding` (line 128) in the file (read only) `internal/mfasvc/service.go`.
- Main has no adapter for the pending sign-in, the recovery codes or the TOTP factor. `mfa.RecoveryCodeStore` (`pkg/mfa/mfa.go:245`), `mfa.PendingStore` (`pkg/mfa/mfa.go:289`) and `stepup.TOTPStore` (`internal/stepup/stepup.go:680`) have no implementation in `internal/store`, and `mfatest.Run` skips each case (`pkg/mfa/mfatest/suite.go:110`).
- Main fails a password sign-in closed with `ReasonSecondFactor` (`internal/localusersvc/signin.go:340`) and has no route that takes a second factor. The password route answers 204 with a session cookie (read only) `internal/api/server/localusers.go`.
- Main counts a TOTP failure on the step-up counter before the time step check: `BeginTOTPAttempt` (`internal/stepup/stepup.go:705`) comes before `AcceptStep` (`internal/stepup/stepup.go:728`), and the comment of `TOTP` (`internal/stepup/stepup.go:634`) says that the sign-in has its own counters.
- Main has the single-use rule of a TOTP code: the store accepts a time step one time (`stepup.ErrUsed` at line 50, `TOTP.LastStep` at line 647, `AcceptStep` at line 728 in the file `internal/stepup/stepup.go`). This change reuses that rule through one new port method and adds no second rule.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (changed), 3 (extended), 6 (extended).
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 369, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273, ADR 370.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "Built-in MFA. An install that signs people in with no SSO identity provider (local users, LDAP) can use built-in MFA."
- Quote: "With an SSO identity provider, MFA is the job of that provider. OpenDustpan adds no MFA step of its own on top (ADR 6)."
- Quote: "What MFA covers. Console sign-in, the creation of access tokens, and the reveal of a secret value (ADR 41)."
- Effect: Built-in MFA applies to local and LDAP sign-in; SSO gets no added step.
- Conflict: none.

**ADR 56: MFA freshness for token creation and secret reveal** (not owner-tagged).

- Decisions that apply: 1 (extended), 5 (extended).
- Changed by: ADR 69 extends; ADR 70 changes decisions 3 and 4; ADR 71 extends; ADR 212 extends decision 1; ADR 222 supersedes decisions 2 and 3; ADR 227 changes decision 4; ADR 227 supersedes decisions 2 and 3; ADR 301 extends decision 5. Chain to the end: ADR 69, ADR 70, ADR 71, ADR 212, ADR 222, ADR 227, ADR 301, ADR 297, ADR 142, ADR 210, ADR 216, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 232, ADR 235, ADR 365, ADR 286, ADR 335, ADR 351, ADR 339, ADR 333, ADR 149, ADR 247, ADR 273, ADR 295, ADR 370, ADR 288.
- Current rule: this ADR, with ADR 69, 70, 71, 212, 222, 227, 301.
- Quote: "Only where MFA applies. The window applies when built-in MFA covers the operation (ADR 46). With MFA off, this ADR adds no step."
- Effect: The fresh step applies only when MFA is on.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does SSO ask for a built-in second factor? No. ADR 46 decision 2.
- Do bearer tokens need MFA on each use? No. ADR 46 decision 8.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
