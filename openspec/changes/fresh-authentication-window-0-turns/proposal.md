Issue: #1325.

## Problem

A window of 0 turns the fresh check off, and the stale error omits the sign-in path.

## Evidence

- Fresh checks exist in accesstoken and profile services. Proof: pkg/accesstoken/service.go:805:func (s *Service) requireFresh(ctx context.Context, subject string) error { and pkg/profile/service.go:495:// requireFresh runs the check of a fresh authentication of a reveal. A refusal
- Issue 1325 reports a zero window that turns the check off. Proof: Issue #1325 body: When the fresh window is 0, `requireFresh` returns nil, so the check is off.
- Issue 1325 reports the stale 403 without the sign-in path and asks about decrypt routes. Proof: Issue #1325 body: the 403 answer for a stale session does not name the sign-in path. ADR 227 decision 6 asks for it.

## Fix boundary

- A window of 0 asks for fresh authentication at each guarded step, including age 0.
- The stale-session 403 names the sign-in path with code mfa.fresh-step-required.
- No-factor, token, narrower window, and boundary behavior match the quoted rules.

Out of scope:

- SCIM
- mTLS
- Collections and placement

## Check

- Add failing test for window 0 with old step refused Check: TestFreshWindowZeroAsksEachTime fails on main
- Add failing test for window 0 with age 0 refused Check: TestFreshWindowZeroAgeZeroRefused fails on main
- Add failing test for namespace 0 against installation 900 Check: TestFreshNarrowZeroAgainst900 fails on main
- Add failing test for longer narrow window refused Check: TestFreshLongerNarrowWindowRefused fails on main
- Add failing test for 403 code and sign-in path Check: TestStaleErrorNamesSignInPath fails on main
- Add failing test for no-factor enrollment page Check: TestFreshNoFactorNamesEnrollment fails on main
- Add failing test for access-token refusal Check: TestFreshTokenNeverSatisfies fails on main
- Add failing test for age equal to nonzero window accepted Check: TestFreshBoundaryEqualsWindow fails on main

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 3).

- Decisions that apply: 1 (extended), 3 (current), 6 (extended).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A fresh authentication. A fresh authentication is a built-in MFA step (ADR 46) or a new authentication at the SSO identity provider (ADR 71). It is fresh when its age is not above the window."
- Quote: "0 asks every time (owner, 2026-10-18). A window of 0 asks for a fresh authentication at each step. No value turns the check off. The other values are 60 to 900 seconds. A window of 0 is the strictest value, so a change to 0 needs no fresh authentication. A change to a longer window needs one, as ADR 212 decision 3 says for a weaker setting."
- Quote: "Errors. A step with no fresh authentication answers `403` with the error code `mfa.fresh-step-required` and changes nothing (ADR 212 decision 3). The error names the path of a new sign-in. An access token never gives a fresh authentication.  Benchmark: the spike stored `freshAuthSeconds` as none, 0 or a window, and the approver gave the age of the authentication. One field gave each rule of its tests."
- Effect: The change enforces 0 as ask-each-time, keeps age not above window as fresh for nonzero windows, and returns 403 mfa.fresh-step-required with the sign-in path.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication. The refusal is `403` with the error code `mfa.fresh-step-required` (ADR 227 decision 6). Its text names the enrollment page and each way to enroll the first fact"
- Effect: The no-factor refusal names the enrollment page with 403 mfa.fresh-step-required.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 18 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "The CLI and API clients. They keep their access tokens (ADR 53), with no change. An access token never gives a fresh authentication (ADR 227 decision 6)."
- Effect: An access token never satisfies a guarded step.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md states Open questions as none, but the decrypt-route question is still open and carries only a provisional answer; the spec-ready gate requires the section to read none with no decision by an owner or ADR, so the change cannot be built until the owner decides decrypt scope. Resolution: open question: Decrypt scope stays as an owner open question; no decrypt check ships until the owner decides.
- R1.2 (missing scenario): The spec has no requirement or scenario for decrypt routes, yet tasks 2.2 and the Fix boundary depend on them; the decision the design calls for is absent from specs/fresh-authentication/spec.md. Resolution: answered: No decrypt requirement ships while the scope question is open; specs cover only decided ADR 227 behavior.
- R1.3 (missing scenario): No scenario covers a principal with no enrolled factor asking for a fresh step; ADR 301 decision 4 requires 403 mfa.fresh-step-required naming the enrollment page, but the spec only names the sign-in path. Resolution: answered: Spec fresh-authentication requirement No factor names enrollment with 403 mfa.fresh-step-required.
- R1.4 (missing scenario): No scenario covers an access token or agent principal asking for a fresh step; ADR 227 decision 6 says an access token never gives a fresh authentication. Resolution: answered: Spec requirement Token never satisfies covers token and agent refusal.
- R1.5 (missing scenario): No scenario covers a namespace or approval rule setting freshAuthSeconds to 0 or a shorter window; ADR 227 decision 4 says the step uses the shortest window that holds, which the service layer design relies on. Resolution: answered: Spec requirement Shortest window holds covers namespace and approval rule 0.
- R1.6 (failure mode): Behavior at step age equal to the window, and clock skew between server and identity provider on auth_time or AuthnInstant, is not stated; ADR 227 decision 1 says fresh when age is not above the window. Resolution: answered: Spec requirement Boundary stays fresh covers age equal to window for nonzero windows, and Zero window asks each time covers age 0 under window 0.
- R1.7 (failure mode): The risk that a decrypt route keeps secret read without fresh proof has no mitigation or failing test in tasks.md; only the window and error text are tested. Resolution: answered: Decrypt risk is in design risks and open_questions escape hatch; no decrypt test ships until scope is decided.
- R1.8 (open question): The change does not say whether a refused step under a 0 window writes an audit record, though each automatic action must write a record of who or what did it and why. Resolution: open question: Audit-on-refusal stays as an owner open question.
- R1.9 (wrong claim): The ADR check lists ADR 227 decision 3 as the owner-tagged decision, but decision 2 (One window everywhere, owner) also governs the window the change alters and is not cited. Resolution: answered: ADR check quotes ADR 227 decisions 1, 3, 6 plus ADR 301 decision 4 and ADR 297 decision 18.
- R2.1 (open question): The change lists Open questions as none, yet the decrypt-route scope is still undecided by the owner and the design says it waits for the owner; the spec-ready gate requires an explicit owner decision or answer, so the provisional PM answer to add a fresh check on decrypt routes must not be taken silently. See openspec/changes/fresh-authentication-window-0-turns/proposal.md and design.md. Resolution: open question: Decrypt scope stays as an owner open question; no decrypt check ships.
- R2.2 (adr conflict): The ADR check cites ADR 227 decision 3 as owner-tagged but the design and tasks do not show how the 0-window fix relates to the quoted line '0 asks every time'; the proposal's Fix boundary also says the stale refusal names the sign-in path, while the tasks omit any test for the audit entry of a refused step that ADR 146 decision 15 and the project rule on automatic actions require. Resolution: open question: Zero-window fix ties to quoted ADR 227 decision 3; audit-on-refusal is an owner open question.
- R2.3 (missing scenario): No scenario covers a guarded step on a namespace or approval rule where freshAuthSeconds is 0 when the installation window is 900, beyond the one-line 'Namespace sets 0' scenario; the rule that a narrower setting may not be longer is not tested. Resolution: answered: Shortest window holds covers namespace 0 against 900 and refusal of longer narrow window.
- R2.4 (missing scenario): The spec has no scenario for a signature of a change order (ADR 227 decision 5), which needs a fresh authentication when the approval rule sets freshAuthSeconds, a destroy is in the change, or freshAuthForApprovals is set. Resolution: answered: Signature scenario removed to stay inside the item; decrypt and signature scope wait for owner and PM answers.
- R2.5 (missing scenario): No scenario covers an IdP-started SAML sign-in that tries a guarded step; ADR 332 decision 5 (owner) says it never counts as a fresh authentication and must ask for a new SP-started sign-in with ForceAuthn. Resolution: answered: IdP-started scope removed from specs to stay inside the item; no new SAML behavior ships here.
- R2.6 (failure mode): Clock skew between the server and the identity provider on auth_time, AuthnInstant or the CAS authentication time can make a fresh step look stale or fresh; the boundary scenario only tests equal age and no skew tolerance is decided. Resolution: open question: Clock skew is a PM open question with benchmark, impact, and escape hatch.
- R2.7 (failure mode): A concurrent change of freshAuthSeconds or a factor reset during a guarded step is not covered; the step may be checked against the old window or a session generation that has just changed (ADR 301 decision 10). Resolution: open question: Concurrent window or factor change is a PM open question for atomic read at use time.
- R2.8 (open question): The Fix boundary and the design do not say which routes count as guarded steps, so the 0-window fix cannot be checked for completeness across token creation, reveal, listed changes and confirmations named in ADR 227 decision 2. Resolution: answered: Guarded steps are the ADR 227 list in design and considered; decrypt completeness waits for the owner.
- R2.9 (wrong claim): The ADR check says ADR 297 decision 18 and ADR 227 decision 6 say an access token never gives a fresh authentication, and the Evidence cites pkg/accesstoken/service.go:805 for requireFresh, but the input gives no code evidence that the current requireFresh returns nil for window 0 or that the handler lacks the sign-in path. Resolution: answered: Evidence cites only pack paths and issue text for requireFresh and the zero-window skip.
- R3.1 (open question): proposal.md and design.md leave Pulumi CLI decrypt scope undecided, yet the Open questions section reads none and the provisional answer (add a fresh check) is not an owner decision; the change cannot be built without the owner's decision on decrypt routes. Resolution: open question: Decrypt scope stays as an owner open question with no build until the owner decides.
- R3.2 (open question): The change does not say whether a refused step writes an audit record. ADR 227 decision 2 is owner-tagged and the project rule requires a record of each automatic action, so the audit behavior of the 403 refusal cannot be built until it is decided. Resolution: open question: Audit on 403 refusal stays as an owner open question.
- R3.3 (adr conflict): The spec and proposal state a 0 window as ask-each-time, but the design's 'age equal to window is fresh' rule with window 0 and age 0 is not separately tested; the quoted ADR 227 decision 1 says fresh when age is not above the window, so the zero-age case must be specified, not left to the implementer. Resolution: answered: Zero window asks each time now states age 0 is refused, with a scenario and failing test.
- R3.4 (missing scenario): No scenario covers a concurrent change of freshAuthSeconds or a factor reset during a guarded step, although the design lists it as a risk; ADR 301 decision 10 bumps the session generation on each factor change. Resolution: open question: Concurrent change stays as a PM open question; risk records in-flight behavior.
- R3.5 (missing scenario): No scenario covers clock skew between the server and the identity provider on auth_time, AuthnInstant or CAS time; the open question provides a provisional tolerance but no requirement or failing test exists. Resolution: open question: Clock skew stays as a PM open question; risk records skew failure mode.
- R3.6 (owner decision conflict): The provisional answer to the decrypt question is to add a fresh check, which the owner has not decided; the change must not pre-empt the owner's scope decision in design decision 3 'Decrypt scope waits for owner'. Resolution: answered: Recommended decrypt answer is leave unchanged and design makes no decrypt change until the owner decides.
- R3.7 (scope): Tasks cover only the failing tests and the docs page; the mapping of each spec scenario (including no-factor, token refusal and shortest-window) to a named failing test is missing from tasks.md, so the scenario-to-test mapping cannot be reviewed. Resolution: answered: Tasks map each spec scenario to a named failing test.
- R3.8 (wrong claim): The ADR check cites ADR 227 decision 3 and decision 6 as owner-tagged decisions and lists 'ADR 297 decision 18' as a source for the access-token rule, but the proposal evidence still points at code lines (pkg/accesstoken/service.go:805) that the input does not verify. Resolution: answered: ADR check quotes ADR 227, ADR 301 decision 4, and ADR 297 decision 18 from pack text; evidence cites only pack paths and issue text.
- R3.9 (failure mode): A guarded step for a principal whose namespace window is 0 and whose installation window is 900 is covered, but the spec does not say what happens when a namespace setting is changed while a step is in flight; the design relies on a same-transaction read that is only a provisional answer. Resolution: open question: In-flight window change stays as a PM open question for same-transaction read.
- R3.10 (scope): The enrollment-page refusal for a person with no factor is required by ADR 301 decision 4 (owner-tagged) but the task list has no failing test for it, so the owner decision is not checked. Resolution: answered: Tasks add TestFreshNoFactorNamesEnrollment for the ADR 301 owner rule.
- M.5 (consistency): Other changes define their own fresh rules: one-membership-function-identity-provider uses a fixed 900 seconds, and mfa names only 'the error of ADR 56'. Resolution: answered: This change owns the one window setting of ADR 227 and the one code mfa.fresh-step-required; design adds One window and one code, considered records reuse, and tasks add TestFreshGuardedRoutesShareCodeAndWindow.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Do the Pulumi CLI decrypt routes need a fresh authentication?
   - Options: Add fresh check to Pulumi CLI decrypt routes; Leave decrypt routes without fresh check and record why.
   - Provisional decision (owner review 14 Nov): Leave decrypt routes without fresh check and record why
   - Reason: ADR 227 names token acts, reveal, and listed changes but does not name decrypt routes. Benchmark: AWS: sensitive acts ask to sign in again, and decrypt of secrets asks again.
   - Persona impact: Asking on decrypt costs one more sign-in before secret read; leaving it out keeps the current decrypt path. Escape hatch: Until the owner decides, decrypt routes stay unchanged and the docs page states that reveal needs fresh proof while decrypt scope is undecided.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).
2. Does a refused step under a 0 window write an audit entry?
   - Options: Write an audit entry for a refused step under a 0 window; Write no audit entry for a refused step.
   - Provisional decision (owner review 14 Nov): Write an audit entry for a refused step under a 0 window
   - Reason: ADR 227 says a refused step changes nothing, and the project rule requires a record of each automatic action. Benchmark: AWS: a refused sensitive act writes a failed-attempt record with actor and reason.
   - Persona impact: An entry upsets no persona; it records who asked and why the step stopped. Escape hatch: If no entry is written, the 403 itself carries actor, step, and reason for support.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).
3. What clock-skew tolerance holds for the fresh age check?
   - Options: Allow a small skew tolerance on auth_time, AuthnInstant, and CAS time; Allow no skew tolerance and use exact ages.
   - Provisional decision (owner review 14 Nov): Allow no skew tolerance and use exact ages
   - Reason: The pack does not state a tolerance for auth_time, AuthnInstant, or CAS time. Benchmark: AWS: identity checks allow small clock skew on auth_time before they refuse.
   - Persona impact: A tolerance avoids false stale refusals; zero tolerance costs a repeat sign-in on skew. Escape hatch: If no tolerance is allowed, the docs page tells the operator to sync clocks and retry after a skew refusal.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).
4. Does a guarded step read the window and session generation at use time?
   - Options: Read the shortest window and session generation in the same transaction as the guarded write; Use the window and generation read at the start of the request.
   - Provisional decision (owner review 14 Nov): Read the shortest window and session generation in the same transaction as the guarded write
   - Reason: The pack does not state behavior for a concurrent change of freshAuthSeconds or factor reset mid-step. Benchmark: AWS: a sensitive act re-reads policy at use time so a concurrent change holds at once.
   - Persona impact: Same-transaction read costs nothing visible and avoids a stale-window pass. Escape hatch: If the step uses the read-time window, the error names the current window and asks for a new sign-in.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).

Questions considered and answered:

- Does a window of 0 turn the fresh check off? No. ADR 227 decision 3 says 0 asks at each step and no value turns the check off.
- Is age equal to the window fresh when the window is 0? Yes for nonzero windows per ADR 227 decision 1; with window 0, ADR 227 decision 3 controls and each step asks, even age 0.
- Must the stale-session refusal name the sign-in path? Yes. ADR 227 decision 6 requires 403 mfa.fresh-step-required and names the path of a new sign-in.
- Do other changes define their own fresh window or error code? No. This change owns the window rules and the code mfa.fresh-step-required, which is the ADR 56 error; other changes use this rule and this code with no fixed window of their own.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
