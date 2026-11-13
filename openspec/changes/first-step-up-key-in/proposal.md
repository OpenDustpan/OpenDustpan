Issue: #1329.

## Problem

An SSO installation can never get its first step-up key, so admin enrollment cannot start. The round 3 review also left gaps on the lock of AcceptStep, key-add challenge use, challenge limits, the admin reset and the recovery of a person whose only key is suspended.

## Evidence

- The item requires a one-time host-command bootstrap for the first step-up key in SSO installs, with a security audit entry and an email. Proof: Issue 1329: An operator on the host enrolls the first key of the first administrator. The bootstrap works only while no administrator holds a key. It writes a security audit entry and sends an email.
- The item lists minor items on the lock of AcceptStep, challenge use, challenge limits, the admin reset and the sole suspended key. Proof: Issue 1329: AcceptStep relies on its caller to take the lock. No port consumes a key-add challenge. The admin reset has no store method. The RFC does not state the recovery path for a person whose only key is suspended.

## Fix boundary

- Add a host command that enrolls the first step-up key of the first administrator, guarded by a stored one-time marker
- Show the marker as an installation fact in the API, with its authorization action
- Allow the command in recovery modes 1 and 2, only while no administrator holds a key
- Refuse the command in recovery mode none, on a pending migration and on an unknown off-switch value
- Write the key, the marker and the audit entry in one transaction
- Send the email after the commit, log a failed send, and record in the audit entry that the email is due
- Move the lock into the AcceptStep port
- Consume each key-add challenge once in the store, with an atomic update
- Count the key-add challenges of an administrator apart from the challenge limit of the person
- Add the store method for the admin reset, reusing the one admin reset route and action of the mfa change, with an audit entry
- State and implement the recovery path of a person whose only key is suspended: the admin reset

Out of scope:

- Changes to D6 and D7 of the RFC
- The full step-up flow outside the first key
- Break-glass passkeys
- A second admin reset route or action: the mfa change owns them

## Check

- Write a failing test for each scenario of the spec Check: go test of the bootstrap package fails on main
- Write the store tests for the admin reset and the challenge use Check: go test of the store conformance suite fails on main
- Add the store method for the admin reset and its audit entry, called by the existing reset route Check: store conformance test for the reset
- Add the marker read to the API Check: route test and API call that returns the marker
- Wire the host command with its authorization actions Check: route test and `opendustpan host step-up-bootstrap --help`
- Add an e2e step against the real binary Check: `make e2e` passes
- Write the docs page with the sole-key recovery Check: a test runs each command of the page
- Usability check from the docs page alone Check: each error names one command

Depends on:

- mfa
- app-level-second-factor-totp
- break-glass-mode-2
- `app-level-second-factor-totp` (order of the milestone review)
- `break-glass-mode-2` (order of the milestone review)

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
- Quote: "One transaction. The command makes the person, its site-administrator grant on the installation, its record and its token in one store transaction, with the four audit entries. A crash leaves all of them or none."
- Quote: "The command needs a reason: `--reason`, or a line that the operator types at a prompt on a terminal."
- Quote: "The command refuses any other value, so a typo never turns the command on."
- Quote: "No hidden characters. The reason and the creator refuse control characters (Unicode Cc) and format characters (Unicode Cf), such as a direction override, so the text in the audit log shows what the operator typed."
- Effect: The key, the marker and the audit entry go in one store transaction. The command runs in mode 1 and mode 2, refuses mode none, refuses an unknown off-switch value and hidden characters, and needs a reason.
- Conflict: none.

**ADR 339: The first administrator gets a one-time code from the bootstrap token** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The marker is a stored fact (owner, 2026-11-07). The marker is an installation fact with a read in the API."
- Effect: The marker is stored, set in the same transaction as the key, and read through the API. The words of the decision do not change. ADR 339 has no Changed-by chain.
- Conflict: none.

**ADR 295: Break-glass recovery mode 2 over the network** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 9 (current).
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
- Effect: Sign-in MFA stays with the provider. The first key serves only step-up for admin actions.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The owner decision of 13 Nov 2026 (P17) requires a one-time marker as in ADR 339 decision 7, but design.md and specs/step-up-bootstrap/spec.md define no marker, its storage, its read in the API, or how it is set and checked. The change follows the rejected route of no marker with a last-key guard. Resolution: answered: A stored one-time marker with an API read is in the design and spec.
- R1.2 (owner decision conflict): The owner decision requires option A on the ADR 339 route with D in mode 2 and mode none closing the route. The spec never names which recovery modes allow the bootstrap, never cites ADR 339 or the marker, and never states that mode none refuses. Resolution: answered: Mode none refuses and the spec has a scenario.
- R1.3 (adr conflict): ADR 245 decision 7 ("One transaction... with the four audit entries. A crash leaves all of them or none.") is applied to a host bootstrap that writes a key, an audit entry and an email. Email is a side effect that cannot sit in a store transaction. The change does not say how a failed send after the key is written is handled. Resolution: answered: The email follows the commit and a failed send is logged.
- R1.4 (adr conflict): ADR 245 decision 14 says the host command applies no migration and refuses on a pending migration. The change never says whether bootstrap checks for a pending migration before it writes the key. Resolution: answered: The pending migration refusal is in the spec.
- R1.5 (adr conflict): ADR 245 decision 17 bans control (Cc) and format (Cf) characters in the reason text. The change says the host boundary checks reason text but never states this rule, so the check it relies on is undefined. Resolution: answered: The reason text rule is in the spec.
- R1.6 (missing scenario): The spec has no scenario for a sole administrator who holds a suspended key, nor for the recovery path that the proposal and tasks promise. A sole suspended key still locks out every administrator, and the risk is unanswered. Resolution: answered: The sole suspended key scenario is in the spec.
- R1.7 (missing scenario): The spec has no scenario for two concurrent bootstraps, for an administrator who exists but holds no key, for a refused bootstrap in mode none, for a bad or missing reason, or for DUSTPAN_DISABLE_HOST_RECOVERY set. The design's hostile tests name these cases but the spec does not. Resolution: answered: Scenarios cover concurrency, mode none and a bad reason.
- R1.8 (failure mode): A replayed key-add challenge and its single-use state across restarts are named as risks, but no requirement says how a replay is refused or how the consumed state survives a restart. Resolution: answered: The challenge once requirement covers a replay after restart.
- R1.9 (scope): The issue #1329 minor items (AcceptStep lock ownership, key-add challenge consumption, admin challenges counted against the person limit, admin reset store method) appear in the proposal and tasks but have no requirement or scenario in the spec, so the change misses part of its own item. Resolution: answered: The minor items have requirements.
- R1.10 (open question): The proposal states "Open questions: none" while the marker, the mode handling, the one-transaction and email question, and the sole-key recovery path are unanswered. The spec-ready gate must refuse this proposal. Resolution: answered: The questions are answered by the owner decision and the design.
- R1.11 (wrong claim): The ADR check quotes ADR 245 decision 3 with its text cut off mid-word, so the quoted line is not a verifiable decision line. The ADR 46 claim that the bootstrap leaves sign-in MFA with the provider is not checked against decision 2 for a step-up key used for admin actions. Resolution: answered: Quotes now end at a full sentence and ADR 46 is checked.
- R2.1 (adr conflict): ADR 245 decision 7 requires the key, the record, the token and all four audit entries in one store transaction; the proposal and design also say the bootstrap writes the key and the marker in one transaction, but the spec never states that the audit entry and marker are in that transaction, so the one-transaction rule is not checked by any scenario (design.md Decisions 1-2, specs/step-up-bootstrap/spec.md). Resolution: answered: The One transaction requirement and its crash scenario cover key, marker and audit entry.
- R2.2 (owner decision conflict): The owner decision of 13 Nov (P17) binds the marker to ADR 339 decision 7, but the ADR check cites ADR 339 only as 'Conflict: none' without stating how the bootstrap sets the marker and checks it in the store; the spec scenario 'Second run' does not name the marker or the ADR 339 route. Resolution: answered: The ADR check states the marker is set and checked in the store transaction; the Second run scenario names the marker.
- R2.3 (failure mode): The spec gives no scenario for a bootstrap that succeeds in the store but whose process crashes before the commit or after it; the design says the email follows the commit, but no requirement says what a restart after a crash mid-bootstrap leaves behind (key without marker, or marker without key). Resolution: answered: The crash scenario shows a restart leaves all or none.
- R2.4 (missing scenario): The 'Admin reset' requirement covers only the sole suspended key; it has no scenario for the admin reset when the administrator is not the sole holder, when the actor is not an administrator, or the audit of the reset, and the store method's authorization action is not named. Resolution: answered: The reset requirement has scenarios for other keys, a caller without the action and the audit entry.
- R2.5 (missing scenario): The spec has no scenario for the 'no administrator' case the issue names: the bootstrap is allowed only while no administrator holds a key, but the 'First key' requirement only says 'when no marker is set', so an installation with a key already enrolled by another route is not covered. Resolution: answered: First key requires no administrator key; a scenario covers a key from another route.
- R2.6 (open question): The 'Email' requirement says the server logs a failed send but does not say whether the audit entry records the send outcome, or whether the operator is told on the console how to resend. Resolution: answered: The design says the audit entry records the key, not the send; the log shows a failed send.
- R2.7 (wrong claim): The ADR check quotes ADR 245 decision 2 and decision 3 as applying, but ADR 245 decision 3 (break-glass controls, reason) is cited in the design only by its effect; the quoted text for ADR 245 is the recovery mode line, not the reason rule that the spec relies on. Resolution: answered: The ADR 245 quotes now include the reason rule.
- R2.8 (scope): The issue lists the minor item on the sole suspended key as a RFC gap; the proposal moves the admin reset into this change but the tasks list no task for the store method or its test, so the item is only partly covered in the tasks. Resolution: answered: Tasks now include the store method and its test.
- R2.9 (failure mode): The 'Challenge once' requirement refuses a reused key-add challenge but does not say how the store records use across concurrent attempts, or what happens when a challenge is used for the admin path and the person path at the same time. Resolution: answered: The store marks use with one conditional update, and a concurrent use scenario exists.
- R3.1 (adr conflict): specs/step-up-bootstrap/spec.md, Requirement 'First key' and design.md Decision 1: ADR 245 decision 2 requires that the host command refuse mode none with a named error, and the spec only has a Mode none scenario for the bootstrap; the 'Mode none' requirement does not say which of modes 1 and 2 are allowed, so the mode-2 route under D (owner decision P17) is not specified. Resolution: answered: A Recovery mode requirement allows modes 1 and 2 and has a scenario for each.
- R3.2 (owner decision conflict): The owner decision P17 binds the marker to ADR 339 decision 7 as an installation fact with an API read. The spec 'Marker' requirement has no scenario that reads the marker through the API, and no requirement states the marker's storage or its read route with its authorization action, so the binding owner-required API read is missing. Resolution: answered: A Marker read requirement and a task add the API read with its authorization action.
- R3.3 (missing scenario): The 'Mode none' scenario names only the error, not the case where mode is 2 and the signed-request route is needed; design.md layer 'host command' lists a direction override in the reason, but the spec 'Bad reason' scenario does not cover control or format characters beyond one example, so ADR 245 decision 17 is only partly tested. Resolution: answered: Reason text has scenarios for a direction override, a control character and no reason.
- R3.4 (failure mode): Email after commit: a crash after the commit and before the send loses the notification with no retry or record, and the spec 'Send fails' scenario covers only a failed send, not a process crash between commit and send. The owner-tagged audit rule of AGENTS requires a record of the action; the email is not recorded. Resolution: answered: The audit entry says the email is due; a scenario covers a crash before the send.
- R3.5 (missing scenario): The spec has no scenario for DUSTPAN_DISABLE_HOST_RECOVERY set to an unknown value, which ADR 245 decision 5 says must refuse; the design layer table does not list it. Resolution: answered: An Off switch requirement and a layer test cover an unknown value.
- R3.6 (open question): The 'Admin reset' requirement names no authorization action for the store method; the proposal and design say it has its own action, but the spec and tasks do not name it, so the route and action are not defined. Resolution: answered: The action installation:manage-step-up is named in the spec, design and tasks.
- R3.7 (scope): The proposal Evidence cites Issue 1329 text as proof of the 'Open questions: none' gate, but the issue asks for the RFC to state the sole-key recovery path; the spec covers only a reset, not the recovery path for a locked person outside the admin reset. Resolution: answered: The sole-key recovery path is the admin reset, stated in what changes and in the spec.
- R3.8 (wrong claim): The ADR check cites ADR 46 decision 2 as 'changed' and ADR 339 as applying, but ADR 339 decision 7 is quoted as a stored fact with 'owner, 2026-11-07'; the ADR check claims ADR 339 is 'Conflict: none' without citing the 'Changed by' chain for ADR 339, which is empty, so the claim is not checked. Resolution: answered: The ADR check says ADR 339 has no Changed-by chain and the words do not change.
- M.22 (dependency): The first step-up key is a factor of the app-level factor store and runs in recovery mode 2. The admin reset with installation:manage-step-up is a third reset path next to mfa and app-level-second-factor-totp. Resolution: answered: depends_on now names mfa, app-level-second-factor-totp and break-glass-mode-2. The change reuses the one admin reset route and action of mfa and adds only the store method.

## Open questions

none

Questions considered and answered:

- Which route and marker? Owner decision, 13 Nov 2026 (queue P17): the one-time marker of ADR 339 decision 7, with the host route in modes 1 and 2. Mode none closes the route.
- How does an email fit in a transaction? ADR 245 decision 7 covers the store writes. The email follows the commit. The audit entry records the due email, so a crash before the send leaves a record.
- What does a crash leave? All or none of key, marker and audit entry (ADR 245 decision 7).
- Does the command migrate? No. It refuses and names the command that applies migrations (ADR 245 decision 14).
- What is the recovery path of a sole suspended key? The admin reset clears the keys, and the person enrolls a new key. It uses the one admin reset route and action of the mfa change.
- Does this change overlap the factor store and mode 2 work? Yes, so it names mfa, app-level-second-factor-totp and break-glass-mode-2 in depends_on and adds no second reset route.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
