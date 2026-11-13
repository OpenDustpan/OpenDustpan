Roadmap: `docs/roadmap.md:201` (Break-glass mode 2).

## Why

**Persona: operator.** Operator sets up an installation that must survive loss of every administrator credential. Operator needs an offline-key recovery path that gives one admin session, refuses forged or reused requests, and leaves a review record.

The user path this change must make work:

1. **Server start.** Set recovery mode to signed-request at first boot and record the offline public key.
2. **API.** Sign a recovery request offline with the operator key and submit it to the recovery route.
3. **Console.** Sign in with the issued one-time admin session from the console and complete recovery.
4. **Docs page.** Follow the docs page to set mode, make a request, submit it, and handle bad-signature and reuse errors.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Recovery request has a wrong signature or wrong key | 401 with code break-glass.bad-signature and message that names the failed check and the command that shows key status. | `opendustpan host recovery-status` |
| Second use of the same recovery request | 409 with code break-glass.reused-request and message that names the reuse and the command that makes a new request. | `opendustpan host recovery-request --help` |
| Recovery attempt on an installation not in signed-request mode | 403 with code break-glass.wrong-mode and message that names the active mode and the command that shows it. | `opendustpan host recovery-status` |

Evidence for the claims above:

- ADR 57 defines mode 2 as a recovery request signed with an offline operator key. Proof: docs/adr/0057-break-glass-recovery.md decision 1
- The milestone step requires one admin session on a signed request, refusal on wrong signature or reuse, and a break-glass review. Proof: docs/roadmap.md:201 step 11 text in context pack

## What Changes

- First-boot recovery mode supports signed-request with pinned offline public key.
- Recovery route accepts an offline-signed request, refuses bad signatures and replays, and issues one admin session.
- Each successful mode 2 use writes an audit entry with mode time and source and opens a break-glass review only when the review opt-in is on; refusals write an audit entry and open no review.
- Success bumps the session generation of the recovery administrator and ends its prior sessions; a valid signed request bypasses the refused-submit throttle.
- E2e and docs turn breakGlassReview.enabled on and show its effective value before the mode 2 use; session lifetime uses the same settings key family as break-glass-sign-in-typed.

Out of scope:

- Mode 1 bootstrap token flow except refusal when mode is signed-request
- Mode 3 restore from backup
- Break-glass account daily use

## Usability

Where the path still costs the person time or surprises them:

- Operator must keep the offline key safe with no in-product guidance past the docs page.
- A replay error needs a new offline signature, which costs a trip to the offline key.
- With the review opt-in off, the use leaves only an audit entry; the docs page warns the operator to turn the review on.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 57: Break-glass recovery when the last administrator loses every factor** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (changed), 5 (current), 6 (current).
- Changed by: ADR 70 changes decision 4; ADR 245 changes decision 2. Chain to the end: ADR 70, ADR 245, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 295, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 333.
- Current rule: this ADR, with ADR 70, 245.
- Quote: "a recovery request signed with an offline operator key (mode 2)"
- Quote: "The operator chooses the mode at first boot."
- Quote: "Every break-glass use writes an audit entry."
- Quote: "The break-glass setting and each recovery step have a REST API and an action (ADR 17)"
- Effect: Implements mode 2 of the three modes: first-boot choice, audit entry with mode time and source on each use, and REST API with action for setting and recovery steps.
- Conflict: none.

**ADR 142: A second-person review of each break-glass by the next business day** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (current), 3 (changed).
- Changed by: ADR 149 extends decisions 12 and 15; ADR 210 changes; ADR 222 changes decision 9; ADR 227 changes decision 9; ADR 297 changes decisions 1, 3 and 9. Chain to the end: ADR 149, ADR 210, ADR 222, ADR 227, ADR 297, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 149, 210, 222, 227, 297.
- Quote: "Two kinds of use."
- Quote: "The opt-in of the installation. The installation setting `breakGlassReview.enabled` is off by default."
- Quote: "A review opens at the use."
- Effect: A successful mode 2 use opens a review only when breakGlassReview.enabled is on, with break-glass.used in the same transaction. Mode 2 needs a new use kind because decision 1 names only freeze-break and account-session, so the mapping needs an RFC.
- Conflict: rfc_needed.

**ADR 245: Host recovery: database access, recovery modes and break-glass controls** (not owner-tagged).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: ADR 295 changes decision 12. Chain to the end: ADR 295.
- Current rule: this ADR, with ADR 295.
- Quote: "The command respects the recovery mode (product manager)."
- Quote: "The command refuses when the installation chose mode 3 of ADR 57"
- Effect: Follows the stored recovery-mode setting; mode 2 uses signed-request mode and mode 3 refuses recovery.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 1 (extended), 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication. A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71)."
- Quote: "One window everywhere (owner)."
- Effect: The recovery session lifetime and any review verdict that needs a fresh step use the one fresh-authentication window.
- Conflict: none.

**ADR 46: Built-in MFA for installs with no SSO identity provider** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 55 extends decision 3; ADR 56 extends decision 6; ADR 57 extends; ADR 212 extends decision 8; ADR 285 changes decisions 1 and 2; ADR 301 changes decision 2. Chain to the end: ADR 55, ADR 56, ADR 57, ADR 212, ADR 285, ADR 301, ADR 70, ADR 297, ADR 320, ADR 321, ADR 69, ADR 71, ADR 222, ADR 227, ADR 245, ADR 232, ADR 235, ADR 365, ADR 312, ADR 333, ADR 142, ADR 210, ADR 216, ADR 306, ADR 332, ADR 354, ADR 286, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 149, ADR 247, ADR 273.
- Current rule: this ADR, with ADR 55, 56, 57, 212, 285, 301.
- Quote: "What MFA covers. Console sign-in, the creation of access tokens, and the reveal of a secret value (ADR 41)."
- Effect: Console sign-in is in MFA scope. No factor exists at recovery time, so the no-MFA-at-issue path needs an RFC exemption for mode 2.
- Conflict: rfc_needed.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 12 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "One session generation for each person. Each principal has a session generation, a count that starts at 1. Each cookie carries the generation that the issue read. A cookie authenticates and refreshes only while its generation is the stored generation. The server adds 1 to it, in the same transaction"
- Effect: Successful mode 2 recovery ends prior sessions of the recovery administrator by adding 1 to its session generation.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The change opens a break-glass review on every mode 2 use, but ADR 142 decision 2 says the installation setting breakGlassReview.enabled is off by default and that when it is off no review opens. The spec and proposal do not state this opt-in. Quote: 'The opt-in of the installation. The installation setting breakGlassReview.enabled is off by default.' (proposal.md 'What Changes'; specs/break-glass-recovery/spec.md 'Valid signed request gives one admin session'). Resolution: answered: Requirement now opens a review only when breakGlassReview.enabled is on; design and tasks updated.
- R1.2 (adr conflict): The design commits the session issue and the audit entry in one transaction but leaves out the review. ADR 142 decision 3 says the review notification break-glass.used is written in the same transaction as the use. Quote: 'each write the notification break-glass.used in their transaction'. Without this, a session can exist with no review record (design.md 'Layers', Store row). Resolution: answered: Design commits break-glass.used notice in the same transaction; requirement and task cover it.
- R1.3 (missing scenario): The proposal error table requires 403 break-glass.wrong-mode for a recovery attempt on an installation not in signed-request mode, but the spec has no scenario for it. Tasks have no test for it either. Resolution: answered: Added wrong-mode requirement, scenario, and failing test task.
- R1.4 (missing scenario): The spec has no scenario for an expired request, a request for another installation or audience, a request whose issued-at is in the future, or an oversize or malformed request ID. The design layers name these hostile cases, but no requirement or task covers them. Resolution: answered: Added expiry, audience, future issued-at, and malformed ID requirements, scenarios, and tests.
- R1.5 (open question): The issued admin session has no stated lifetime, scope or freshness. ADR 227 decision 2 (owner) says one fresh-authentication window applies to each break-glass review and each fresh step. The change does not say whether the recovery session or the review verdict must be fresh, or how long the session lasts. Resolution: open question: Added open question on session lifetime, scope, and fresh-step rule per ADR 227.
- R1.6 (failure mode): Two valid submissions of the same request ID that reach two nodes, or a restart during the consume, are not covered. The design relies on a compare-and-set, but the spec states no outcome for a concurrent or interrupted consume, and no test covers it. Resolution: answered: Added single-winner concurrent consume requirement, scenario, and test.
- R1.7 (adr conflict): ADR 57 decision 5 requires that each break-glass use entry hold the mode, the time and the source. The spec scenario names only mode and source, so the time is missing from the required audit fields. Resolution: answered: Audit scenario now names mode, time, and source.
- R1.8 (open question): The design asks for a tight expiry and a clock skew bound, but gives no values. The provisional answers name the expiry field but set no maximum lifetime and no skew tolerance, so an implementer cannot build the freshness check. Resolution: open question: Added open question with recommended maximum lifetime and skew bound.
- R1.9 (adr conflict): The change opens a review on refused uses (bad signature, reuse). ADR 142 decision 1 defines a use only as a freeze-break job or an account-session. The proposal does not reconcile a review on a refusal with that definition. Resolution: answered: Refusals now write only an audit entry and open no review, per ADR 142 use definition.
- R1.10 (scope): The task list has no task for the console sign-in step of the user path, although proposal step 3 requires that the issued session works in the console. Resolution: answered: Added console sign-in task for the issued session.
- R1.11 (scope): The design Store layer requires a conformance test for a duplicate request ID and for a mode change after first boot, but tasks.md lists no store adapter test. Resolution: answered: Added store adapter conformance test task for duplicate ID and immutable mode.
- R2.1 (open question): The use of a mode 2 recovery is installation-scoped, but ADR 142 decision 14 requires each break-glass.Store write to carry the fence of its review namespace, and ADR 142 decision 1 defines a use only as a freeze-break job or an account-session. The change does not say which scope and which use kind the recovery review gets. Design.md Store layer and spec.md 'Valid signed request gives one admin session' leave this open. Resolution: open question: Added open question on review scope and use kind; adr_check notes the gap.
- R2.2 (adr conflict): ADR 46 decision 6 says 'What MFA covers. Console sign-in, the creation of access tokens, and the reveal of a secret value.' The change issues a console admin session from a signed request with no MFA step and no decision that exempts mode 2. The change must either apply MFA to the recovery session or cite a decision that allows the bypass. Resolution: open question: Added ADR 46 to adr_check and an open question on the MFA step at issue for the recovery session.
- R2.3 (failure mode): The refused-use path writes one audit entry for each wrong signature, unknown key and malformed request, and the HTTP layer names no rate limit or cap. Anyone who can reach the recovery route can grow the audit log without bound. No requirement covers throttling or a cap on refused-use entries. Resolution: answered: Added bounded refused-use requirement, hostile flood test, and rate-limit guard in design.
- R2.4 (missing scenario): No requirement or scenario covers rotation or revocation of the pinned offline public key after a suspected compromise, or a key change after the mode is set. Design.md pins the key at the constructor, and the risks name key loss, but no path replaces the key. Resolution: open question: Added open question on offline key rotation.
- R2.5 (missing scenario): No scenario covers a recovery request that arrives while an administrator session from an earlier recovery is still live, or while the installation already has working administrators. The change does not say whether a second session is issued or refused. Resolution: open question: Added open question on second live recovery session in session-delivery question.
- R2.6 (missing scenario): The spec does not say how the issued admin session reaches the console (sealed cookie, generation count under ADR 297 decision 12, or a bearer token). The console step of the user path in proposal.md therefore has no defined mechanism. Resolution: open question: Added open question on sealed-cookie versus bearer-token delivery.
- R2.7 (scope): The proposal error table names the commands 'opendustpan host recovery-status' and 'opendustpan host recovery-request --help', and the user path needs an offline signing tool. tasks.md has no task that builds or tests these CLI commands or the offline signing tool; task 2.2 only checks a curl call to the status route. Resolution: answered: Added CLI build and test tasks for host recovery-status, recovery-request, and offline signing.
- R2.8 (wrong claim): The open question 1 benchmark states that 'PCI DSS requires unique authentication with replay resistance' and that practice is Ed25519 with kid, nonce, expiry and audience. The input contains no source for either claim, and the claim is not backed by a fetched reference. Resolution: answered: Removed unbacked PCI claim from benchmarks; benchmarks now name AWS IAM and Google practice only.
- R3.1 (adr conflict): ADR 46 decision 6 says MFA covers console sign-in, and the design issues a console admin session from a signed request. Provisional decision 6 in proposal.md answers 'no MFA step at issue' with no cited decision that exempts mode 2, so the provisional answer contradicts ADR 46 and must be decided before build. Resolution: open question: Marked ADR 46 as rfc_needed in adr_check and kept the no-MFA-at-issue path as an open question that needs an RFC exemption.
- R3.2 (failure mode): The per-address throttle on refused submits (design.md Layers, HTTP adapter; spec.md refused-use-bounded) has no rule for proxies or NAT. Behind a shared address, or with a flood from one address, the throttle also refuses the operator's own valid recovery, so an attacker can deny recovery. Resolution: answered: Design and refused-use-bounded requirement now state a valid signed request bypasses the per-address throttle; hostile test covers flood plus valid request from same address.
- R3.3 (failure mode): Review opt-in is off by default (ADR 142 decision 2), so a stolen offline key yields a 15-minute admin session with no review record. The change has no alert or notice path for a mode 2 use when the opt-in is off, and the proposal does not say so. Resolution: answered: Design risks and docs warn that review opt-in off leaves only the audit entry; what_changes and review-off audit requirement state that refusals and successes always write audit with no review when opt-in is off.
- R3.4 (open question): Open question 8 asks whether a second recovery session may exist while one is live. The provisional answer covers only the delivery mechanism and leaves the second-session rule unanswered, and no requirement covers it. Resolution: open question: Split delivery from concurrency; added a separate open question on a second live recovery session with a recommended refuse-until-end rule.
- R3.5 (adr conflict): Provisional decision 5 maps the recovery use to the ADR 142 use kind account-session, which decision 1 defines as a break-glass account sign-in. A mode 2 recovery session is not that sign-in, so the mapping is not supported by the cited decision. Resolution: open question: Changed recommended review mapping to a new use kind recovery-session in installation scope; adr_check marks ADR 142 as rfc_needed.
- R3.6 (missing scenario): No requirement says whether a successful recovery bumps the session generation of ADR 297 decision 12. Sessions stolen before the recovery may then survive it, and the spec does not say so. Resolution: answered: Added session-generation requirement that success bumps the recovery administrator generation and ends prior sessions, with a scenario and failing test.
- R3.7 (failure mode): The design asks for a restart-safe single-use check, but no scenario covers a crash after the request ID is consumed and before the session is written, or the reverse. The single transaction in design decision 3 is asserted but not tested for partial failure. Resolution: answered: Design states atomic commit semantics for crash before and after commit; added restart-safe scenario and store test task.
- M.17 (consistency): The change opens a break-glass review only when the review opt-in is on, but step 11 says the use opens a review (ADR 142). The session limit differs from the other break-glass session limits. Resolution: answered: Kept ADR 142 opt-in rule and fixed verification: design adds opt-in-on decision, e2e step turns breakGlassReview.enabled on and checks effective value before the use, and session lifetime now uses the same settings key family as break-glass-sign-in-typed.

## Open questions

1. What is the exact signed-request format and signature scheme for mode 2? Decides: PM.
   - Options: Ed25519 signed JSON request with kid, request ID, installation ID, audience, issued-at and expiry; X.509 certificate signed request.
   - Recommended: Ed25519 signed JSON request with kid, request ID, installation ID, audience, issued-at and expiry Reason: One scheme keeps offline signing simple and auditable.
2. How long does the server keep a used request ID to refuse replays? Decides: PM.
   - Options: Keep used request IDs until request expiry plus 7 days, then purge; Keep used request IDs forever.
   - Recommended: Keep used request IDs until request expiry plus 7 days, then purge Reason: Bounded retention refuses replays during the risk window without boundless growth.
3. What lifetime and scope does the issued admin session have, and does the review verdict need a fresh step? Decides: PM.
   - Options: One admin session of 15 minutes for recovery only from the same settings key family as break-glass-sign-in-typed, review verdict needs a fresh step per the one window; One admin session of 60 minutes with full administrator scope.
   - Recommended: One admin session of 15 minutes for recovery only from the same settings key family as break-glass-sign-in-typed, review verdict needs a fresh step per the one window Reason: A short recovery-only session from the shared family bounds the effect while the review still checks the use.
4. What maximum request lifetime and clock skew bound does the freshness check use? Decides: PM.
   - Options: Maximum lifetime 15 minutes and skew tolerance 120 seconds; Maximum lifetime 60 minutes and skew tolerance 300 seconds.
   - Recommended: Maximum lifetime 15 minutes and skew tolerance 120 seconds Reason: A short lifetime with a small bound refuses replays without false refusals from normal skew.
5. Which scope and which use kind does the mode 2 recovery review use? Decides: PM.
   - Options: Installation scope with a new use kind recovery-session for the recovery administrator; Installation scope with use kind account-session naming the recovery administrator.
   - Recommended: Installation scope with a new use kind recovery-session for the recovery administrator Reason: One fixed mapping lets the fence check and the review list work for an installation-scoped use, and a new kind avoids misuse of the break-glass account kind.
6. Does the mode 2 recovery session need an MFA step at issue? Decides: PM.
   - Options: No MFA step at issue because no factor exists; session is short and recovery-only; Require an enrolled MFA factor at issue.
   - Recommended: No MFA step at issue because no factor exists; session is short and recovery-only Reason: No factor exists to check when every administrator lost access, so a short recovery-only session is the usable path.
7. How does the pinned offline public key rotate after compromise? Decides: PM.
   - Options: Pinned key rotates only through a signed rotation request from the current offline key; Pinned key rotates through a site administrator API with a fresh step.
   - Recommended: The operator replaces the pinned key with a host command that needs host access and writes an audit entry. During a planned rotation, two pinned keys may be valid for a short overlap. A request signed with the old key must never rotate the key after a compromise. Reason: An attacker who holds a compromised key could use a signed rotation request to lock the operator out. Host access is a separate trust root.
8. How does the issued admin session reach the console? Decides: PM.
   - Options: Deliver the session as the standard console sealed-cookie sign-in for one recovery administrator; Deliver the session as a one-time bearer token that the console exchanges once.
   - Recommended: Deliver the session as the standard console sealed-cookie sign-in for one recovery administrator Reason: One standard delivery keeps the console path and the generation check unchanged.
9. May a second recovery session exist while one recovery session is live? Decides: PM.
   - Options: A second valid request while one recovery session is live ends the first session and issues a new one; A second valid request while one recovery session is live is refused until the first ends.
   - Recommended: A second valid request ends the live recovery session (the session generation goes up) and gives a new one. Only one recovery session is live at a time, and both uses are audited and reviewed. Reason: This matches the rule of the change that success ends prior sessions. It does not lock out an operator who lost the first session.

Not spec-ready: the change conflicts with ADR 142: an RFC comes first; the change conflicts with ADR 46: an RFC comes first.

Questions considered and answered:

- When is mode 2 selected? Yes. ADR 57 decision 2 says the operator sets the mode when the installation starts for the first time, and ADR 245 decision 6 holds it in the recovery-mode setting; first boot sets signed-request and later boots keep it.
- Does a mode 2 use write audit and review records? Yes for success. ADR 57 decision 5 requires an audit entry with mode, time and source on every mode 2 use; ADR 142 decisions 2 and 3 open a review with break-glass.used in the same transaction only when breakGlassReview.enabled is on. Refusals write an audit entry and open no review.
- Do the ADRs fix signature scheme and replay handling? No. ADR 57 names a recovery request signed with an offline operator key and one admin session, but names no signature scheme, request fields, lifetimes, or replay store; left as open questions.
- Does MFA apply to the recovery session at issue? No. ADR 46 decision 6 puts console sign-in in MFA scope, but no ADR exempts a recovery session when no factor exists; left as an open question that needs an RFC.
- Which review scope and use kind does a mode 2 recovery get? No. ADR 142 decision 1 names only freeze-break and account-session uses, so an installation-scoped recovery needs a new use kind; left as an open question that needs an RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
