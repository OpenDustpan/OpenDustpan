---
version: 2
---

Issue: #1329.

## Problem

An SSO installation can never get its first step-up key, so admin enrollment cannot start. The round 3 review also left gaps on the lock of AcceptStep, key-add challenge use, challenge limits, the admin reset and the recovery of a person whose only key is suspended.

## Evidence

- The item requires a one-time host-command bootstrap for the first step-up key in SSO installs, with a security audit entry and an email. Proof: Issue 1329: An operator on the host enrolls the first key of the first administrator. The bootstrap works only while no administrator holds a key. It writes a security audit entry and sends an email.
- The item lists minor items on the lock of AcceptStep, challenge use, challenge limits, the admin reset and the sole suspended key. Proof: Issue 1329: AcceptStep relies on its caller to take the lock. No port consumes a key-add challenge. The admin reset has no store method. The RFC does not state the recovery path for a person whose only key is suspended.
- Main has a key-add challenge check and a passkey enrollment e2e test. Proof: Context pack main list: internal/stepup/stepup.go:728 (func CheckKeyAdditionChallenge) and e2e/passkeys_test.go:23 TestPasskeyEnrollment.

## Fix boundary

- Add the new host command `opendustpan host step-up-bootstrap` that starts the enrollment of the first step-up key of one named administrator, guarded by a stored one-time marker
- Add an unexported bootstrap token type with one constructor, and a store method that checks the marker and the key count
- Show the marker as an installation fact in the API, with its authorization action
- Allow the command in recovery modes 1 and 2, only while no administrator holds a key
- Refuse the command in recovery mode none, on a pending migration and on an unknown off-switch value
- Write the key, the marker and the audit entry in one transaction
- Send the email after the commit, log a failed send, and write a second audit entry with the send result
- Move the lock into the AcceptStep port
- Add a store method that consumes each key-add challenge once with an atomic update, and refuses an expired challenge or a challenge of another person
- Count the key-add challenges of an administrator apart from the challenge limit of the person
- Add the store method for the admin reset, reusing the one admin reset route and action of the mfa change, with an audit entry
- State and implement the recovery path of a person whose only key is suspended: the admin reset

Out of scope:

- Changes to D6 and D7 of the RFC
- The full step-up flow outside the first key
- Break-glass passkeys
- A second admin reset route or action: the mfa change owns them
- A retry queue for the email

## Check

- Write a failing test for each scenario of the spec Check: go test of the bootstrap package fails on main
- Write the store tests for the marker, the admin reset and the challenge use Check: go test of the store conformance suite fails on main
- Build the host command, the bootstrap token type and the store method for the marker Check: go test of the bootstrap package passes
- Move the lock into the AcceptStep port Check: TestAcceptStepTakesTheLock
- Add the store method that consumes a key-add challenge Check: store conformance test for challenge use
- Add the admin challenge counter Check: TestAdminChallengeCountIsSeparate
- Add the store method for the admin reset and its audit entry Check: store conformance test for the reset
- Add the marker read to the API Check: route test and API call that returns the marker

Depends on:

- mfa
- app-level-second-factor-totp
- break-glass-mode-2
- `mfa` (order of the milestone review)
- `app-level-second-factor-totp` (order of the milestone review)
- `break-glass-mode-2` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- CheckKeyAdditionChallenge checks a key-add challenge in the step-up package; AcceptStep is listed as an interface method there. (`internal/stepup/stepup.go:728`)
- A test shows that a step-up credential alone gives no fresh authentication. (`internal/api/server/stepup_claims_test.go:25`)
- The audit package has the security audit kind. (`pkg/audit/audit.go:62`)
- A passkey enrollment e2e test exists. (`e2e/passkeys_test.go:23`)
- Passkey enroll, list and remove are tested in the server API. (`internal/api/server/passkeys_test.go:290`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P17)**, Batch 15: RFC 102 first step-up key on an SSO installation: "option A (the ADR 339 route), with D in mode 2, and the RFC's recommendation on Q2-Q10 (ignore break-glass passkeys; mode `none` closes A and D; accept the mode-none dead end), EXCEPT the marker: the PM takes the stricter choice, a one-time marker as in ADR 339 d7, instead of the RFC's "no marker with a last-key guard". Reason: keeps owner decision 4 and the break-glass rules; Okta and Entra use an admin one-time code; the marker fails closed and matches the shape the owner already accepted in ADR 339."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 245: Host recovery: database access, recovery modes and break-glass controls** (not owner-tagged).

- Decisions that apply: 2 (current), 3 (current), 5 (current), 7 (current), 17 (current).
- Changed by: ADR 295 changes decision 12. Chain to the end: ADR 295.
- Current rule: this ADR, with ADR 295.
- Quote: "The command respects the recovery mode (product manager). The command refuses when the installation chose mode 3 of ADR 57 ("no break-glass"). The error names the mode and the setting to change."
- Quote: "The command needs a reason: `--reason`, or a line that the operator types at a prompt on a terminal."
- Quote: "The command refuses any other value, so a typo never turns the command on."
- Quote: "No hidden characters. The reason and the creator refuse control characters (Unicode Cc) and format characters (Unicode Cf), such as a direction override, so the text in the audit log shows what the operator typed."
- Effect: The key, the marker and the audit entry go in one store transaction. The email follows the commit, because it is not a store write. The command runs in mode 1 and mode 2, refuses mode none, refuses an unknown off-switch value and hidden characters, and needs a reason.
- Conflict: none.

**ADR 339: The first administrator gets a one-time code from the bootstrap token** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The marker is a stored fact (owner, 2026-11-07). The marker is an installation fact with a read in the API."
- Effect: The marker is stored, set in the same transaction as the key, and read through the API. The words of the decision do not change. A later loss of every administrator goes through recovery, so this route does not reopen after the marker is set.
- Conflict: none.

**ADR 295: Break-glass recovery mode 2 over the network** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The controls of ADR 245 apply. A temporary administrator from this route keeps these controls of ADR 245."
- Effect: Mode 2 keeps the controls of the host command. This change adds no network route.
- Conflict: none.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 2 (changed).
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "With an SSO identity provider, MFA is the job of that provider. OpenDustpan adds no MFA step of its own on top (ADR 6)."
- Effect: The first key serves only step-up for admin actions. It adds no step to the sign-in, so the provider keeps the MFA of the sign-in.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: threat 1 (A second bootstrap makes a backdoor key ): no guard and hostile test for encode or contain, limit; a code check fails: threat 2 (A replayed or stolen key-add challenge e): no guard and hostile test for encode or contain, authorize at point of use, fail closed; a code check fails: threat 3 (A crash after the commit hides that the ): no guard and hostile test for boundary check, encode or contain, limit, authorize at point of use.

Questions considered and answered:

- Which route and marker? Owner decision, 13 Nov 2026 (queue P17): the one-time marker of ADR 339 decision 7, with the host route in modes 1 and 2. Mode none closes the route.
- How does an email fit in a transaction? ADR 245 decision 7 covers the store writes. The email follows the commit. A second audit entry records the send result.
- Does the command migrate? No. It refuses and names the command that applies migrations (ADR 245 decision 14).
- Who is the first administrator, and what is the key material? The operator names one enabled site administrator by subject. The host cannot run a browser ceremony, so the command makes a one-time enrollment code and the administrator makes the passkey in the console.
- What if the marker is set and no administrator has a key? Owner decision, 13 Nov 2026 (queue P17) accepts the dead end of mode none. ADR 339 decision 7 sends the loss of every administrator to recovery (ADR 57, ADR 245). The docs page says this.
- What is the recovery path of a sole suspended key? The admin reset clears the keys, and the person enrolls a new key. It uses the one admin reset route and action of the mfa change.
- What limits the admin challenges? A separate counter of 10 for each administrator and person pair in each hour, fixed in code and listed in the catalog of code rules.
- What happens after a restore of an older backup? The marker is part of the restored data, so an older backup restores the older marker. The restore writes its own audit entries. The docs page states this.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
