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
- Fresh-window values, the 0 window behavior, and the no-factor refusal, specified in fresh-authentication-window-0-turns
- App-level second factor details beyond the one factor store, specified in app-level-second-factor-totp and first-step-up-key-in

Depends on:

- add-one-generic-settings-api
- fresh-authentication-window-0-turns
- ldap-sign-in
- `add-one-generic-settings-api` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A person who loses all factors waits for an administrator reset.
- A stale fresh step interrupts token creation and forces a new sign-in.
- A person with no administrator uses the break-glass recovery path for access.

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
- Effect: The change requires a fresh step for token creation, token rotation, and secret reveal inside the one window, only where built-in MFA covers the operation, with error code mfa.fresh-step-required. Decisions 2 and 3 of this ADR are superseded by ADR 227; the current window rule and its values live in fresh-authentication-window-0-turns.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2, 3).

- Decisions that apply: 1 (extended), 2 (current), 3 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication. A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71). It is fresh when its age is not above the window."
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes). Each step that an ADR names as "fresh MFA", "a fresh MFA step" or "a fresh authentication" uses it: token creation and rotation, a secret reveal, the changes of ADR 70 decisions 16"
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step. No value turns the check off. The other values are 60 to 900 seconds. A window of 0 is the strictest value, so a change to 0 needs no fresh authentication. A change to a longer window needs one, as ADR"
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3). The error names the path of a new sign-in. An access token never gives a fresh authentication.  Benchmark: the spike stored `freshAuthSeconds` as none, 0"
- Effect: The change uses the one fresh-authentication window and the 403 error for token creation, token rotation, and reveal; the range, the 0 behavior, and the no-factor refusal are specified in fresh-authentication-window-0-turns.
- Conflict: none.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 20 (changed), 21 (changed).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333.
- Quote: "A passkey or TOTP as the second factor. When built-in MFA is on (ADR 46), a password sign-in makes a pending sign-in, not a session (RFC 21 P7). The person then gives a passkey (user verification `preferred`), a TOTP cod"
- Quote: ""Require MFA" applies to the password (owner, through RFC 21 P7). The installation setting `mfa.requireForAll` makes each password sign-in ask for a second factor. While an SSO provider exists and `signIn.localPassword.e"
- Effect: The change makes a password sign-in create a pending sign-in and require a passkey or TOTP second factor when require MFA applies, with user verification preferred and lock counting.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 12 (current), 20 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "One session generation for each person. Each principal has a session generation, a count that starts at 1. Each cookie carries the generation that the issue read. A cookie authenticates and refreshes only while its gener"
- Quote: "Sign-in paths. - **MFA.** A pending sign-in is a sealed cookie (decisions of the owner, item 1). It cannot authenticate a request. The factor lock of the person counts the wrong factors (ADR 285 decision 14). A good fact"
- Effect: The change keeps a pending sign-in unable to authenticate, counts wrong factors under the factor lock, and keeps the session generation check on each cookie.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current), 4 (current), 11 (current), 12 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A factor of OpenDustpan gives the fresh step (owner). For a person who signed in with a plain OAuth 2.0 provider or with the trusted header of a proxy, the fresh authentication of ADR 227 decision 1 is a passkey, a TOTP"
- Quote: "Sign-in stays the same (owner). The sign-in with these two methods asks for no second factor. So ADR 46 decision 2 stays true for sign-in. This ADR narrows it: for these two methods, OpenDustpan asks for its own factor a"
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication. The refusal is `403` with the error code `mfa.fresh-step-required` (ADR 227 decis"
- Quote: "The step belongs to one sign-in (PM). The server records the fresh step in the sealed access cookie of the sign-in that did it, and in no other sign-in. The kind of the step is one of the kinds of ADR 297 decision 1 (`pa"
- Quote: "Only when MFA is on (PM). ADR 56 decision 5 holds. On an installation with MFA off, the server asks for no fresh step, as for every other method. The rules of this ADR apply when MFA is on."
- Effect: The change records the fresh step in the sealed access cookie of the sign-in that did it and refuses a fresh-step operation for an OAuth or trusted-header person with no enrolled factor; the refusal wording lives in fresh-authentication-window-0-turns.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 17).

- Decisions that apply: 16 (current), 17 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Each method asks for a fresh authentication in its own way (PM). The fresh step of ADR 227 decision 1 uses the parameter of each protocol:  | Method | How the server asks | What the server checks | Fresh kind | | --- | -"
- Quote: "LDAP is a password method (owner, ADR 285 decision 21). For the fresh step, an LDAP person follows ADR 285 decision 21, as a local password user does. A password alone is a fresh authentication only for a person with no"
- Effect: The change asks for the fresh step per method and treats an LDAP person like a local password user for the fresh step.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): specs/mfa/spec.md defines no requirement for the freshAuthSeconds value range. ADR 227 decision 3 (owner) and decision 4 require that only 0 or 60 to 900 seconds are accepted and that other values are refused; the design does not state the refusal, so the change cannot be built right. Resolution: answered: Window range lives in fresh-authentication-window-0-turns; this change reads the one freshAuthSeconds window and refuses stale steps with mfa.fresh-step-required.
- R1.2 (owner decision conflict): The proposal says the fresh-step error for token creation and reveal names a sign-in command, but the change does not cover the case of a person who signed in through OAuth or a trusted header with no factor enrolled. ADR 301 decision 4 (owner) requires a 403 refusal with error code mfa.fresh-step-required, and no spec scenario covers it. Resolution: answered: No-factor refusal lives in fresh-authentication-window-0-turns per ADR 301 decision 4; this change applies the same 403 code through the shared fresh-step check.
- R1.3 (missing scenario): No scenario covers TOTP or WebAuthn enrollment, removal, or lost-factor reset, nor recovery codes shown once at enrollment (ADR 46 decisions 4 and 9). The tasks and the Usability section name these paths, so the change cannot be built without them. Resolution: answered: Covered by enrollment-recovery, recovery-code-signin, and admin-reset requirements with audit and notice scenarios.
- R1.4 (open question): The proposal states Open questions: none, but the design relies on a replay window of one step, a lock threshold and a constant-time comparison that no ADR answers. The spec-ready gate needs an owner or RFC answer for these values. Resolution: answered: This change sets no new replay, threshold, or comparison value and reuses pkg/mfa, pkg/mfa/mfatest, and internal/mfasvc with ADR 285 decision 14 counts; no owner value is needed.
- R1.5 (adr conflict): ADR 46 decision 5 sets MFA off by default and an administrator turns it on. The spec's require-mfa scenarios do not say how mfa.requireForAll is set or what its default is. Resolution: answered: Covered by mfa-off-by-default requirement and settings task that registers mfa.requireForAll through the settings API.
- R1.6 (owner decision conflict): ADR 285 decision 21 (owner) says the SSO-exists condition changes how the local password second factor applies; the sso-no-added-step requirement omits that condition and does not state how SSO and password sign-in interact when both exist. Resolution: answered: Extended sso-no-added-step text and scenario to state the SSO-exists condition for local password sign-in.
- R1.7 (failure mode): No scenario covers pending sign-in expiry, reuse, or use from another client, though ADR 297 decision 20 says a pending sign-in cannot authenticate a request. The hostile test of 100 wrong TOTP codes has no matching lock scenario. Resolution: answered: Covered by pending-signin-limits requirement with expiry, single use, no authentication, and failure counting.
- R1.8 (adr conflict): ADR 46 decision 8 says access tokens stay bearer tokens with no MFA step on each use. The proposal does not state that token use is exempt, so a reader could take the change to require MFA on each token use. Resolution: answered: Covered by token-use-no-step requirement that each use of a valid bearer token needs no MFA step.
- R1.9 (failure mode): No requirement covers a session generation bump during an MFA step or concurrent sign-ins, though ADR 297 decision 12 requires the generation check on each cookie. Resolution: answered: Covered by generation-bump scenario in pending-signin-limits so a bump refuses the old cookie.
- R1.10 (scope): tasks.md 2.2 wires the effective value of freshAuthSeconds in the settings API, which belongs to the settings item of ADR 212 decision 7, and the administrator reset path is missing from both the proposal and the tasks though ADR 46 decision 9 places it here. Resolution: answered: Settings read uses the existing settings resource of add-one-generic-settings-api; administrator reset is wired with its authorization action.
- R2.1 (adr conflict): specs/mfa/spec.md 'pending-signin-limits' and design.md: the change does not state the owner-tagged ADR 285 decision 20 rule that a password sign-in creates a pending sign-in and a passkey, TOTP or second factor is required; the spec's require-mfa scenarios omit user verification and TOTP lock counting from ADR 297 decision 20, so the pending sign-in rules are incomplete. Resolution: answered: Added ADR 285 decision 20 pending sign-in rule, user verification preferred, and factor lock counting to considered, adr_check, and second-factor scenarios; design gate now names user verification.
- R2.2 (owner decision conflict): proposal.md ADR check: ADR 227 decision 2 (owner) requires one freshAuthSeconds window for token rotation and each fresh step, but the spec and tasks cover only token creation and secret reveal; token rotation is missing from the change. Resolution: answered: Added token rotation to the fresh-step requirement text, scenarios, and tasks per owner-tagged ADR 227 decision 2.
- R2.3 (failure mode): design.md Decision 2 stores the last MFA step time on the session record, but ADR 301 decision 11 (owner) requires the fresh step to be recorded in the sealed access cookie of the sign-in that did it and in no other; the design contradicts this and no requirement covers it. Resolution: answered: Changed design to record the fresh step in the sealed access cookie of the sign-in that did it per ADR 301 decision 11, not on a session record.
- R2.4 (wrong claim): proposal.md ADR check says ADR 46 decision 6 is extended and the error table names mfa.fresh-step-required for reveal; but the ADR 56 entry lists decisions 1, 4 and 5 while the spec cites ADR 56 only for token creation. The 'Changed by' chain for ADR 56 lists ADR 222 which supersedes window decisions; the proposal does not state which window decision is current. Resolution: answered: Extended ADR 56 entry with decision 6 and stated that decisions 2 and 3 are superseded by ADR 227 with the one window as current rule.
- R2.5 (missing scenario): specs/mfa/spec.md 'require-mfa-local-signin': no scenario covers a wrong TOTP or failed WebAuthn second factor, which the proposal error table defines with a 401 message naming opendustpan auth mfa verify. Resolution: answered: Added wrong second-factor scenario with 401 and the mfa verify command path.
- R2.6 (missing scenario): specs/mfa/spec.md: no scenario covers the audit entry or notice to the person on each enrollment, removal or reset (ADR 301 decision 10, owner-tagged), although tasks 2.3 names an audit entry. Resolution: answered: Added audit entry and notice scenarios for enrollment, removal, and reset per ADR 301 decision 10.
- R2.7 (failure mode): specs/mfa/spec.md 'pending-signin-limits': no requirement sets an expiry duration or a rule for concurrent pending sign-ins from one client; the 'Expired or reused' scenario uses an undefined expiry. Resolution: answered: Reworded pending sign-in scenario to use no-longer-valid without an invented expiry duration; concurrent pending sign-ins use no new value and follow existing port behavior.
- R2.8 (open question): design.md Risks: TOTP clock drift and the lost-factor reset are named as risks, but no ADR or spec sets the TOTP time-step tolerance or the reset identity check; the proposal states Open questions: none. Resolution: answered: Added considered entry that the change sets no new TOTP tolerance or reset check and uses the existing MFA port behavior; risks stay as risks only.
- R2.9 (scope): tasks.md 2.2 says to read the existing settings resource values, but ADR 212 decision 7 (owner) defines the settings API and the change lists no task for the mfa.requireForAll setting; the separate secret-reveal setting of ADR 46 decision 7 is not covered. Resolution: answered: Added reveal-setting requirement for the separate secret-reveal setting and tasks for mfa.requireForAll and the reveal setting.
- R2.10 (wrong claim): proposal.md ADR check cites ADR 46 decision 2 as stating SSO adds no step, but the change uses ADR 320 decision 16 only via prose; the Open questions section cites 'the existing sign-in failure counters' without a file or ADR reference, so the claim is unsupported. Resolution: answered: Added ADR 320 decisions 16 and 17 to adr_check and cited ADR 285 decision 14 and ADR 320 decision 19 for failure counters.
- R2.11 (missing scenario): specs/mfa/spec.md 'fresh-step-token-and-reveal': no scenario covers a fresh step inside the window that succeeds, nor a window of 0 that always asks, so the strictest value path is untested. Resolution: answered: Added success inside window and window 0 always-asks scenarios to the fresh-step requirement.
- R3.1 (adr conflict): openspec/changes/mfa/design.md Decision 2 says the freshness time is checked against the window, but proposal ADR check cites ADR 56 decision 5 and the spec does not state that the window applies only where built-in MFA covers the operation; the spec's fresh-step requirement omits the MFA-off condition of ADR 56 decision 5 and ADR 301 decision 12, so MFA-off behavior is unspecified. Resolution: answered: Added MFA-off condition to fresh-step requirement text so the window applies only where built-in MFA covers the operation, per ADR 56 decision 5 and ADR 301 decision 12.
- R3.2 (owner decision conflict): The spec and proposal require a fresh step for token creation and reveal but the reveal-setting requirement (ADR 46 decision 7, a separate setting) is not reconciled with the window of ADR 227 decision 2, which makes the window the one window for every step; the change does not state which rule governs a reveal when the separate setting is on. Resolution: answered: Reconciled reveal-setting with one window: the separate setting governs whether a reveal needs a fresh step, the one freshAuthSeconds window governs its age; both requirements now state this split.
- R3.3 (missing scenario): No scenario covers a wrong or replayed TOTP code being rejected after an earlier use of the same code, although the Layers table names a replay hostile test and the design says a replayed TOTP code must fail; the spec does not require single use of a TOTP code. Resolution: answered: Added reused-TOTP-code refusal scenario and single-use guard in service layer.
- R3.4 (open question): The change sets no value for the lock threshold, the TOTP time-step tolerance or the replay window, yet the proposal says Open questions: none and cites existing port behavior without a file or ADR proof; the spec-ready gate requires an owner or RFC answer for these values. Resolution: answered: Added considered entry that this change sets no new lock, tolerance, or replay value and reuses pkg/mfa, pkg/mfa/mfatest, and internal/mfasvc with ADR 285 decision 14 counts; no new value needs an owner decision.
- R3.5 (failure mode): Lost factors and an administrator reset are covered, but no scenario covers a person who loses all factors when no administrator exists or when the only break-glass path is needed; the Usability section says the person waits for an administrator reset with no path given. Resolution: answered: Added out-of-scope entry for no-administrator recovery via break-glass modes and usability note naming the administrator reset and break-glass path.
- R3.6 (wrong claim): The proposal ADR check states ADR 46 decision 2 'stays true for sign-in' and the Open questions cite ADR 320 decision 19 for failure counters, but ADR 320 decision 19 concerns LDAP counts, not the local TOTP counter; the claim is not supported for local second factors. Resolution: answered: Fixed counter citation to ADR 285 decision 14 and ADR 297 decision 20 for local second factors; ADR 320 decision 19 cited only for LDAP sign-in counts.
- R3.7 (failure mode): Concurrency is not covered: two sign-ins or a fresh step and a token creation racing on the same sealed cookie are not specified, and a restart during a pending sign-in has no scenario. Resolution: answered: Added race scenario where a second completion of a pending sign-in fails, and restart scenario where a sealed pending cookie verifies without server state.
- R3.8 (missing scenario): No scenario covers the recovery code path itself: use of a recovery code to sign in when a factor is lost, or the count of recovery codes left, although ADR 46 decision 4 requires recovery codes and the enrollment scenario only shows them once. Resolution: answered: Added recovery-code-signin requirement with use, single-use, and count-left scenarios.
- R3.9 (scope): tasks.md 2.2 wires the settings read and mfa.requireForAll and the reveal setting, which belongs to the settings item of ADR 212 decision 7, while task 2.3 adds the reset path with no matching task for the sign-in service Layer table audit entries. Resolution: answered: Reworded settings task to read existing settings resource and added service audit-entry task for enrollment, use, and reset.
- R3.10 (open question): The design states the sealed access cookie records the fresh step but does not say how a token rotation issued from a cookie of an earlier sign-in is refused, which the owner rule of ADR 301 decision 11 requires. Resolution: answered: Added cross-sign-in cookie refusal scenario per ADR 301 decision 11 and recorded it in cookie-bound design decision.
- M.3 (duplicate): Enrollment, recovery codes, admin reset, the window range and the no-factor refusal are also in app-level-second-factor-totp, first-step-up-key-in and fresh-authentication-window-0-turns. Resolution: answered: Removed duplicate window-range and no-factor requirements to fresh-authentication-window-0-turns, kept one factor store with enrollment recovery codes and one administrator reset route here, guarded POST /api/dustpan/v1/me/tokens, registered require MFA and reveal setting through the settings API, and used code mfa.fresh-step-required.

## Open questions

none

Questions considered and answered:

- Does an SSO sign-in ask for a built-in second factor? ADR 46 decision 2 and ADR 55 decision 10 say MFA belongs to the SSO provider, so the server adds no second factor to SSO sign-in; ADR 320 decisions 16 and 17 state how each method asks for the fresh step and that LDAP follows the local password rule with the SSO-exists condition of ADR 285 decision 21.
- Which operations need a fresh MFA step? ADR 46 decision 6 covers console sign-in, token creation, and secret reveal; ADR 56 decisions 1 and 5 with ADR 227 decisions 1 and 2 and ADR 301 decision 12 set the one window for token creation, token rotation, and reveal only where built-in MFA covers the operation; ADR 46 decision 8 exempts each use of a bearer token. The window values and the no-factor refusal are specified in fresh-authentication-window-0-turns.
- Which second factors satisfy the sign-in? ADR 46 decision 3 ranks WebAuthn and TOTP the same; ADR 285 decision 20 requires a passkey with user verification preferred or a TOTP code; ADR 297 decision 20 says the factor lock of the person counts wrong factors. This change owns the one factor store, enrollment, recovery codes, and the one administrator reset route; app-level-second-factor-totp and first-step-up-key-in reuse it and add no second store.
- What happens to a local password sign-in when require MFA is on? ADR 285 decisions 20 and 21 make a password sign-in create a pending sign-in when require MFA is on; ADR 297 decision 20 says it cannot authenticate a request.
- Where do the window values, the zero window, and the no-factor refusal live? This change registers mfa.requireForAll and the separate secret-reveal MFA setting through the settings API of add-one-generic-settings-api and guards POST /api/dustpan/v1/me/tokens with the fresh-step check; the window range, the 0 behavior, and the no-factor refusal live in fresh-authentication-window-0-turns and are not repeated here.
- How do enrollment, recovery codes, and lost factors work? ADR 46 decisions 4 and 9 require recovery codes shown once at enrollment and an administrator reset with an audit entry; ADR 301 decision 10 adds a notice to the person and a session generation bump on enrollment, removal, and reset.
- Which replay, lock, and comparison values does this change set? This change sets no new lock threshold, TOTP tolerance, or replay window. It reuses the existing MFA port in pkg/mfa with its suite pkg/mfa/mfatest and the service internal/mfasvc, and the existing failure counts of ADR 285 decision 14 for passwords and second factors. No ADR in this pack states a new numeric value for this change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
