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

- Sessions record auth_time and sign-in requests record max_age, which plain OAuth providers do not supply. Proof: pkg/session/service.go:242:// request, the return path, and the max_age of the authorization request
- The server stores session rows with provider, auth_time and generation for freshness checks. Proof: pkg/store/sqlite/internal/db/queries.sql.go:2438:INSERT INTO sessions (id, principal_id, digest, provider, created_at_ms, last_seen_at_ms, auth_time_ms, source_ip, user_agent, generation)
- No server code path names select_account, the only prompt value GitHub supports per the issue. Proof: rg -n -F --glob '!*_test.go' 'select_account' pkg internal cmd matches 0

## What Changes

- REST API to enroll, list and remove TOTP and passkey factors for the caller
- Step-up check that records a fresh second-factor step in the sealed access cookie of one sign-in
- First enrollment only with an administrator enrollment code or a one-time email code

Out of scope:

- Changes to OIDC max_age, SAML ForceAuthn and CAS renew behavior
- The factor store, admin reset and recovery-code paths, owned by the mfa change
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

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 17: Every feature has a REST API, authorization and policy** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 30 changes; ADR 210 extends; ADR 222 changes; ADR 224 changes; ADR 266 extends; ADR 305 extends; ADR 326 extends. Chain to the end: ADR 30, ADR 210, ADR 222, ADR 224, ADR 266, ADR 305, ADR 326, ADR 35, ADR 38, ADR 39, ADR 41, ADR 42, ADR 151, ADR 218, ADR 248, ADR 281, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 272, ADR 48, ADR 70, ADR 212, ADR 237, ADR 238, ADR 245, ADR 60, ADR 103, ADR 146, ADR 240, ADR 352, ADR 253, ADR 154, ADR 217, ADR 220.
- Current rule: this ADR, with ADR 30, 210, 222, 224, 266, 305, 326.
- Quote: "A REST API for every feature."
- Effect: Enrollment, list, step-up and removal each get a documented REST API with a declared action and object.
- Conflict: none.

**ADR 56: MFA freshness for token creation and secret reveal** (not owner-tagged).

- Decisions that apply: 1 (extended), 4 (changed), 5 (extended), 6 (current).
- Changed by: ADR 69 extends; ADR 70 changes decisions 3 and 4; ADR 71 extends; ADR 212 extends decision 1; ADR 222 supersedes decisions 2 and 3; ADR 227 changes decision 4; ADR 227 supersedes decisions 2 and 3; ADR 301 extends decision 5. Chain to the end: ADR 69, ADR 70, ADR 71, ADR 212, ADR 222, ADR 227, ADR 301, ADR 297, ADR 142, ADR 210, ADR 216, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 232, ADR 235, ADR 365, ADR 286, ADR 335, ADR 351, ADR 339, ADR 333, ADR 149, ADR 247, ADR 273, ADR 295, ADR 288.
- Current rule: this ADR, with ADR 69, 70, 71, 212, 222, 227, 301.
- Quote: "A freshness window. Token creation and secret reveal need an MFA step that is younger than the freshness window."
- Effect: The app factor counts as a step younger than the window of decision 1; decision 5 limits the rule to installations with MFA on; decision 6 gives each setting a REST API.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 11 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "`server:manage-sign-in` gates the sign-in settings."
- Effect: Factor management uses the sign-in settings action of decision 11.
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

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): specs/second-factor/spec.md 'Enroll TOTP factor' accepts only 'a valid enrollment proof' without defining it, but ADR 301 decisions 6, 14 and 15 require an administrator enrollment code or the one-time email code for first enrollment; a stolen session could enroll the first factor. Resolution: answered: Specs enroll-first-totp and enroll-first-passkey now require an administrator enrollment code or the one-time email code for first enrollment.
- R1.2 (owner decision conflict): specs/second-factor/spec.md 'Remove writes entry' omits the ADR 301 decision 10 rule that each enrollment, removal and reset adds 1 to the session generation and sends a notice; the change is not built right without it. Resolution: answered: Spec factor-change-notice now requires one audit entry, one notice and session generation plus 1 for each enrollment, removal and reset.
- R1.3 (adr conflict): design.md decision 3 makes the refusal optional behind requireSecondFactorForWeakMethods, off by default, but ADR 301 decision 4 states 'No factor, no fresh step (owner)' for every plain OAuth and trusted-header person when MFA is on. Resolution: answered: Design decision 3 and spec refuse-without-factor now refuse unconditionally when MFA is on per ADR 301 decision 4; the optional require setting is removed.
- R1.4 (missing scenario): No scenario covers the recovery-code path (ADR 301 decision 9), which proposal and tasks 1.2 name; required behavior is one replacement factor, single use, audit entry, and never a fresh step. Resolution: answered: Spec recovery-code-only-replaces now requires one replacement factor, single use, audit entry and never a fresh step.
- R1.5 (missing scenario): No requirement covers the administrator reset returning the person to no factor and requiring a new enrollment code (ADR 301 decision 7). Resolution: answered: Spec admin-reset-to-no-factor now requires return to no factor with a new enrollment code for the next enrollment.
- R1.6 (open question): open-questions section reads 'none' but lists two open questions (secret storage and WebAuthn library); the WebAuthn dependency needs owner approval per project.md, and the passkey requirement cannot be built without that answer. Resolution: answered: Open questions are now none; storage and library answers moved to design and considered per ADR 301.
- R1.7 (missing scenario): No scenario covers the ADR 301 decision 13 refusal shape: with a factor the 403 must name the factor step; with no factor it must name the enrollment page. The spec states only the error code. Resolution: answered: Spec refusal-shape now requires the 403 to name the factor step when a factor exists and the enrollment page when none exists.
- R1.8 (failure mode): No requirement covers concurrent step-up with one TOTP code or a restart between the cookie write and the check; replay protection named in the layers table has no spec scenario. Resolution: answered: Spec single-use-challenge now requires rejection of a replayed TOTP code or assertion across concurrent step-up and restart.
- R1.9 (failure mode): Sealed TOTP secrets depend on the key hierarchy; key rotation, a lost key-encryption key and restore of a backup (ADR 41, ADR 151) are not covered. Resolution: answered: Design decision 3 now requires sealed storage that survives key rotation and restore; rotation failure modes stay in the MFA key-rotation change.
- R1.10 (scope): Tasks 2.2 names pkg/mfa and the design names internal/mefactorsvc, while the proposal and design do not state where the unexported factorService type lives; the task paths are not consistent. Resolution: answered: Design structure and all tasks now name internal/mfasvc factorService consistently.
- R1.11 (missing scenario): The SSH signature factor (ADR 301 decisions 8, 16, 17) appears in the proposal's console ordering but has no requirement or scenario in the spec or tasks. Resolution: answered: Spec ssh-signature-fresh-step now covers the SSH signature factor and its fallback order.
- R1.12 (wrong claim): ADR check for ADR 56 quotes decision 1 and cites decision 4 as changed, but does not record the ADR 70 change to decision 4 that governs window configuration; the proposal's claim that the factor 'restarts the same window' is therefore not checked against the current rule. Resolution: answered: ADR check for ADR 56 now quotes decisions 1, 4, 5 and 6 and records the ADR 70 change to decision 4.
- R2.1 (owner decision conflict): specs/second-factor/spec.md 'Enroll first TOTP factor' and 'Enroll first passkey' accept an administrator enrollment code or a one-time email code without requiring the email code to be one-time and first-enrollment only, nor the 128-bit code and one-hour life of ADR 301 decision 14; the factor is still enrolled from a bare proof with no expiry or single-use rule. Resolution: answered: Enroll specs now require 128-bit single-use enrollment code inside one-hour life and a one-time first-enrollment-only email code.
- R2.2 (adr conflict): design.md decision 3 says sealed storage 'survives key rotation and restore' and defers rotation failure modes to another change, but ADR 301 decision 11 and ADR 56 decision 1 need the fresh step bound to the sealed access cookie; a lost key-encryption key or a restore into a new installation leaves no defined outcome for factors or for the cookie-bound step. Resolution: answered: Design decision 3 now defines the lost-key and new-installation outcome: factors unreadable and every fresh step invalid until re-enrollment.
- R2.3 (missing scenario): No scenario covers the enrollment code's expiry, single use, or reuse after a failed attempt (ADR 301 decision 14 gives a default life of one hour); the spec only states the success path. Resolution: answered: New enrollment-code-expiry requirement covers expiry, single use and reuse after a failed attempt.
- R2.4 (missing scenario): No scenario covers the rate limit on TOTP and enrollment-code attempts, though the proposal's error table promises that retries stay inside the rate limit; brute force of the 6-digit TOTP or the enrollment code is not covered. Resolution: answered: New rate-limit requirement covers TOTP and enrollment-code retries and brute-force limits.
- R2.5 (failure mode): The 'Single-use challenge' scenario requires rejection after restart, but no requirement states where the used-code record is stored or how it survives a restart with a sealed cookie from another sign-in; the ADR 301 decision 11 binding is untested for a stolen cookie. Resolution: answered: New used-code-durable requirement stores used codes durably across restart and binds the step to one sign-in, tested with a stolen cookie.
- R2.6 (wrong claim): ADR check for ADR 56 quotes decision 1 as 'extended' and cites ADR 70 as changing decision 4, but the Current rule in the input lists ADR 56 decision 4 as changed by ADR 70 only for decisions 3 and 4; the spec scenario 'Refuse OAuth token creation' relies on decision 4 of ADR 301 without a window-zero case when the window is set to 0. Resolution: answered: ADR 56 check now records decision 4 as changed by ADR 70 and the design plus a window-zero requirement cover a zero window.
- R2.7 (open question): The open-questions section reads none, but the issue #728 names an installation setting for requiring a factor and an RFC for storage and WebAuthn library; the proposal drops the setting by citing ADR 301 decision 4, while the WebAuthn library dependency still needs owner approval under project.md and no ADR answers it. Resolution: answered: Considered records that reuse of the local-user MFA code path adds no new WebAuthn dependency and the setting is answered by ADR 301 decision 4.
- R2.8 (scope): tasks.md 2.3 and 3.2 require an e2e step with a passkey or TOTP through the real CLI and the real directory, but tasks.md 3.2 names 'weak-method step' on SQLite and PostgreSQL over TLS, a claim the proposal does not state; the e2e scope against the issue's SSH and passkey paths is not mapped. Resolution: answered: Tasks now map TOTP, passkey and SSH-signature paths and state the SQLite and PostgreSQL over-TLS scope plainly.
- R2.9 (missing scenario): No scenario covers a factor enrolled by a person whose sign-in is a trusted-header proxy session after the allowlist changes, nor the case where MFA is turned off and back on while a fresh step is held in a cookie. Resolution: answered: New trusted-header and MFA-toggle requirement covers allowlist change and MFA off and on while a step is held.
- R2.10 (failure mode): The 'Factor change notice' scenario sends a notice to the notification channel of the person, but no requirement says what happens when no notification channel is configured or delivery fails; the audit entry and generation bump must still occur. Resolution: answered: Factor-change-notice requirement now still writes the audit entry and bumps generation when no channel exists or delivery fails.
- R3.1 (owner decision conflict): specs/second-factor/spec.md 'Refuse without factor' and design.md decision 3 are checked against ADR 301 decision 4, but the proposal's 'Errors' table and the ADR check quote ADR 301 decision 4 as current while the 'Changed by' and 'Current rule' of ADR 56 and ADR 70 are not reconciled; the spec does not state how a window of zero interacts with plain OAuth persons who hold a fresh step from an earlier sign-in. Resolution: answered: Window-zero requirement now states that a fresh step from an earlier sign-in or earlier operation never passes when the window is zero; ADR 56 effect no longer claims a restart and cites decision 4 as changed by ADR 70.
- R3.2 (open question): The proposal says 'Open questions: none' but the issue #728 asks for an RFC for factor storage and encryption and for the WebAuthn library, and project.md says a new dependency needs the owner; the change records 'no owner approval for a dependency' without citing an owner decision for the library choice. Resolution: answered: Considered now cites ADR 301 as owner-tagged for the one-code-path library choice with no new dependency, and design reuses the sealed MFA store for storage.
- R3.3 (failure mode): design.md decision 3 says a lost key-encryption key or a restore into a new installation leaves factors unreadable and every fresh step invalid; no requirement or scenario in specs/second-factor/spec.md covers the restore path, and tasks.md has no test for it. Resolution: answered: New restore-leaves-factors-unreadable requirement and a restore task cover the restore-into-new-installation path.
- R3.4 (missing scenario): The spec has no scenario for a person who holds a factor and whose factor is the only one when an administrator resets factors while the person is mid step-up. Resolution: answered: Admin-reset requirement now has a mid-step-up scenario where the in-flight check fails and the person returns to no factor.
- R3.5 (wrong claim): The proposal's ADR check for ADR 56 quotes decision 1 as 'extended' and says ADR 70 changes decision 4, but the chain entry for ADR 70 'changes decisions 3 and 4' is cited for ADR 56 decision 4 only; the claim that the window 'restarts the same window' is not supported by the quoted lines. Resolution: answered: ADR 56 effect now says the factor counts as a step younger than the window instead of restarts the same window, matched to the quoted decision 1 line.
- R3.6 (scope): tasks.md 3.2 claims 'weak-method step' on PostgreSQL over TLS and 2.3 names an OAuth test provider; the proposal does not state the dialect scope, and the SSH-signature path lacks a task of its own in section 1. Resolution: answered: Tasks state SQLite and PostgreSQL over TLS scope in each e2e check and section 1 has a separate SSH-signature failing test.
- R3.7 (failure mode): Concurrent enrollment: two requests with the same enrollment code arriving together are not covered by the 'Reuse or expiry fails' scenario, which tests only sequential reuse. Resolution: answered: New concurrent-enrollment requirement rejects the second of two concurrent uses of one enrollment code.
- M.4 (duplicate): 'admin-reset-to-no-factor', the recovery-code path and 'window-zero-asks-each-time' repeat mfa and fresh-authentication-window-0-turns. Resolution: answered: Removed admin-reset-to-no-factor, recovery-code-only-replaces and window-zero-asks-each-time; they now live in depends_on as mfa and fresh-authentication-window-0-turns. This change keeps only the OAuth and trusted-header step-up and the first-enrollment gate and takes over the trusted-header factor rule.

## Open questions

none

Questions considered and answered:

- Does step-up happen at sign-in or later? Later. ADR 301 decisions 1, 3 and 4 keep sign-in without a second factor and add the fresh step later from an app factor, with 403 mfa.fresh-step-required when no factor exists and MFA is on.
- Does this reuse the local-user MFA code and need a new WebAuthn library? Yes. ADR 301 decision 5 says the passkey and TOTP code is the code of the local-user MFA. This change reuses it with no new WebAuthn dependency.
- Is an RFC needed before code for storage and library choices? No RFC. ADR 301 decisions 5, 6, 14 and 15 already decide one code path and the enrollment-code proof.
- Where does the trusted-header factor requirement live? This change owns the trusted-header factor rule. The sign-in-methods change owns the proxy allowlist and leaves the factor step here.
- Who owns admin reset, recovery codes and the zero window? Out of scope here. The mfa change owns the factor store, admin reset and recovery codes, and the fresh-authentication-window-0-turns change owns the zero window. This change calls them.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
