---
version: 2
---

Issue: #728.

## Why

**Persona: developer.** A Developer signs in with a plain OAuth provider such as GitHub and then creates an access token for the Pulumi CLI and OpenTofu. The provider round trip proves only a live provider session, so token creation and secret reveal need a fresh step from an app-level TOTP or passkey factor.

The user path this change must make work:

1. **Docs page.** Read the sign-in and freshness docs page for plain OAuth limits and the recommended second factor
2. **Console.** Enroll a TOTP or passkey factor in the console with an enrollment code from the administrator
3. **API.** Do a fresh second-factor step-up through the API when token creation or secret reveal needs it
4. **CLI.** Create an access token and run the Pulumi CLI and OpenTofu with that token

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Token creation with an OAuth session past the freshness window and no fresh app-factor step fails | 403 with error code mfa.fresh-step-required that names the factor step, or for a person with no factor the enrollment page | `GET /api/dustpan/v1/me/factors` |
| First enrollment with a session alone and no enrollment code or email code fails | 403 with error code mfa.enrollment-required with the enrollment page path | `POST /api/dustpan/v1/me/factors/totp/enroll` |
| Step-up with a wrong TOTP code fails | 400 with error code totp.invalid-code and the retry stays inside the rate limit | `POST /api/dustpan/v1/me/factors/step-up` |

Evidence for the claims above:

- Plain OAuth providers return no auth_time and the pending sign-in request carries no max_age. Proof: internal/oauth/plain.go:162:// no max_age: a plain OAuth provider returns no auth_time to check it
- The server stores session rows with provider and auth_time for freshness checks. Proof: pkg/store/sqlite/internal/db/queries.sql.go:2438:INSERT INTO sessions (id, principal_id, digest, provider, created_at_ms, last_seen_at_ms, auth_time_ms, source_ip, user_agent, generation)
- No server code path names select_account. Proof: rg -n -F --glob '!*_test.go' 'select_account' pkg internal cmd matches 0

## What Changes

- New route `GET /api/dustpan/v1/me/factors` lists enrolled factors for the caller with action me:read-factors
- New route `POST /api/dustpan/v1/me/factors/totp/enroll` enrolls a TOTP factor with an enrollment code or a one-time email code with action me:manage-factors
- New route `POST /api/dustpan/v1/me/factors/passkeys/enroll` enrolls a passkey factor with an enrollment code or a one-time email code with action me:manage-factors
- New route `POST /api/dustpan/v1/me/factors/step-up` records a fresh second-factor step in one sign-in with action me:manage-factors
- New route `DELETE /api/dustpan/v1/me/factors/{id}` removes a TOTP or passkey factor for the caller with action me:manage-factors and needs a fresh step
- New route `POST /api/dustpan/v1/principals/{id}/enrollment-code` issues a 128-bit single-use one-hour enrollment code with action server:manage-sign-in
- Installation setting `require-second-factor-for-plain-oauth` off by default through `PATCH /api/dustpan/v1/settings` with action server:manage-sign-in
- First enrollment only with an administrator enrollment code or a one-time email code; existing /me/passkeys routes stay and share one code path

Out of scope:

- Changes to OIDC max_age, SAML ForceAuthn and CAS renew behavior
- The factor store, admin reset and recovery-code rotation paths, owned by the mfa change
- The zero-window behavior, owned by the fresh-authentication-window-0-turns change
- Trusted-header proxy allowlist management, owned by the sign-in-methods change
- Break-glass mode 2 recovery flow

Depends on:

- mfa
- sign-in-methods
- fresh-authentication-window-0-turns
- `mfa` (order of the milestone review)
- `sign-in-methods` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A person with no factor meets a refusal that names the enrollment page, not a bare 403.
- Enrollment shows a TOTP secret one time; a lost secret needs the administrator enrollment code path.
- The console offers passkey and TOTP first and keeps SSH signature as fallback only.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests that a second factor accepts no user verification in the WebAuthn adapter. (`pkg/mfa/gowebauthn/gowebauthn_test.go:375 (TestSecondFactorAcceptsNoUserVerification)`)
- Main tests that WebAuthn gates use the resolved source address. (`pkg/dustpan/passkey_source_test.go:104 (TestWebauthnGatesUseTheResolvedSourceAddress)`)
- Main tests the freshness hook for token creation. (`pkg/accesstoken/accesstoken_test.go:779 (TestFreshnessHook)`)
- Main sweeps and disables inactive tokens and accounts. (`pkg/accesstoken/accesstoken_test.go:1217 (TestSweepDisablesInactiveTokensAndAccounts)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 17: Every feature has a REST API, authorization and policy** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 30 changes; ADR 210 extends; ADR 222 changes; ADR 224 changes; ADR 266 extends; ADR 305 extends; ADR 326 extends. Chain to the end: ADR 30, ADR 210, ADR 222, ADR 224, ADR 266, ADR 305, ADR 326, ADR 35, ADR 38, ADR 39, ADR 41, ADR 42, ADR 151, ADR 218, ADR 248, ADR 281, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 272, ADR 48, ADR 70, ADR 212, ADR 237, ADR 238, ADR 245, ADR 60, ADR 103, ADR 146, ADR 240, ADR 352, ADR 253, ADR 154, ADR 217, ADR 220.
- Current rule: this ADR, with ADR 30, 210, 222, 224, 266, 305, 326.
- Quote: "A REST API for every feature."
- Effect: Enrollment, list, step-up, removal and the two settings each get a documented REST API with a declared action and object.
- Conflict: none.

**ADR 56: MFA freshness for token creation and secret reveal** (not owner-tagged).

- Decisions that apply: 1 (extended), 4 (changed), 5 (extended), 6 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 3 and 4; ADR 71 extends; ADR 212 extends decision 1; ADR 222 supersedes decisions 2 and 3; ADR 227 changes decision 4; ADR 227 supersedes decisions 2 and 3; ADR 301 extends decision 5. Chain to the end: ADR 69, ADR 70, ADR 71, ADR 212, ADR 222, ADR 227, ADR 301, ADR 297, ADR 142, ADR 210, ADR 216, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 232, ADR 235, ADR 365, ADR 286, ADR 335, ADR 351, ADR 339, ADR 333, ADR 149, ADR 247, ADR 273, ADR 295, ADR 288.
- Current rule: this ADR, with ADR 69, 70, 71, 212, 222, 227, 301.
- Quote: "A freshness window. Token creation and secret reveal need an MFA step that is younger than the freshness window."
- Effect: The app factor counts as a step younger than the window of decision 1; decision 4 as changed by ADR 70 governs the zero window; decision 5 limits the rule to MFA on; decision 6 gives each setting a REST API.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 11 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "`server:manage-sign-in` gates the sign-in settings."
- Effect: The installation require-second-factor setting and the enrollment-code issue action use the sign-in settings action of decision 11; per-person factor routes use a self action on the caller.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current), 4 (current), 5 (current), 6 (current), 8 (current), 11 (current), 12 (current), 13 (current), 14 (current), 15 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A factor of OpenDustpan gives the fresh step (owner)."
- Quote: "No factor, no fresh step (owner)."
- Quote: "One code path."
- Quote: "The step belongs to one sign-in (PM)."
- Quote: "Only when MFA is on (PM)."
- Quote: "The refusal names the factor step (PM)."
- Effect: This change builds only the owner-decided factor flow for plain OAuth and trusted-header persons with one code path, first enrollment with an independent proof, refusal with no factor when MFA is on, and the step bound to one sign-in.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: test `TestTotpEnrollWithCode` (.tasks[0].items[0].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: test `TestReplayedCodeFails` (.tasks[0].items[1].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: test `TestSshSignatureFreshStep` (.tasks[0].items[2].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: and 8 more.

Questions considered and answered:

- Does step-up happen at sign-in or later? Later. ADR 301 decisions 1, 3 and 4 keep sign-in without a second factor and add the fresh step later from an app factor, with 403 mfa.fresh-step-required when no factor exists and MFA is on.
- Does this reuse the local-user MFA code and need a new WebAuthn library? Yes. ADR 301 decision 5 (owner-tagged ADR) says the passkey and TOTP code is the code of the local-user MFA. This change reuses it with no new WebAuthn dependency.
- Is an RFC needed before code for storage and library choices? No RFC. ADR 301 decisions 5, 6, 14 and 15 already decide one code path and the enrollment-code proof, and design reuses the sealed MFA store of the mfa change.
- Where does the trusted-header factor requirement live? This change owns the trusted-header factor rule. The sign-in-methods change owns the proxy allowlist and leaves the factor step here.
- Who owns admin reset, recovery codes and the zero window? The mfa change owns the factor store, admin reset and recovery-code rotation; the fresh-authentication-window-0-turns change owns the zero window. This change adds only the OAuth and trusted-header step-up, the first-enrollment gate, the require setting and the enrollment-code issue path.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
