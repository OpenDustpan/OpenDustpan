Issue: #624, #628.

## Why

**Persona: site administrator.** As a site administrator I keep last-resort access safe when the identity provider is lost, so I need break-glass sign-in to need a typed reason, raise an alert, end at a time limit that I can set, and remind me to test each account every 90 days.

The user path this change must make work:

1. **Docs page.** Site administrator opens the docs page for break-glass sign-in and starts a sign-in with a typed reason.
2. **API.** The sign-in route receives the reason. The server writes the alert audit entry in the sign-in transaction, issues a session with a time limit from the setting, and then writes a WARN log line.
3. **API.** Each request of the session writes an audit entry that names the session ID and the reason.
4. **API.** The administrator reads the principal response from the principals routes in `internal/api/server/principals.go`. It shows the last test time and a flag for 90 days with no test. These are new fields of an existing response.
5. **Console.** The console shows a banner for the flag with a link to the docs page.
6. **E2e.** An e2e step signs in a break-glass account with a reason, checks the expiry and reads the flag.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Break-glass sign-in with no typed reason, or a reason that is too long or holds a control character. | 400: A break-glass sign-in needs a reason of 1 to 500 characters with no control character. | `POST /api/dustpan/v1/sign-in/passkey/finish` |
| Request with an expired break-glass session. | 401: The break-glass session reached its time limit. Sign in again with a reason. | `POST /api/dustpan/v1/sign-in/passkey/begin` |
| Break-glass sign-in started at the identity provider. | 403: A break-glass sign-in uses a passkey or a hardware key, not the identity provider. | `POST /api/dustpan/v1/sign-in/passkey/begin` |
| The audit write of a request of the session fails. | 503: The server could not record this request. Try again. | `POST /api/dustpan/v1/sign-in/passkey/begin` |

Evidence for the claims above:

- ADR 70 decision 26 says each break-glass sign-in needs a typed reason, raises an alert, and the audit log records each action of the session. It does not state the time limit value. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- The 90-day reminder is the scope of issue 628 only. No ADR text in the pack states it, so this change does not claim an ADR requires it. Proof: internal/api/server/principals.go
- The session package exists and is where issue 624 asks for the time limit. Proof: pkg/session
- The passkey sign-in routes exist in the API and are the sign-in path of a break-glass account. Proof: coverage gap list: POST /api/dustpan/v1/sign-in/passkey/begin and POST /api/dustpan/v1/sign-in/passkey/finish

## What Changes

- A break-glass sign-in without a typed reason is refused. The reason has 1 to 500 characters. Any Unicode control character, DEL and any line or paragraph separator is refused.
- A sign-in writes the alert audit entry in the sign-in transaction. If the audit write fails, the sign-in fails and no session exists. The server writes the WARN line after the commit, with CR and LF removed.
- The session limit is a key of the generic settings API, with the same range and shorter-of rule as the temporary administrator session limit. The session carries its expiry in the sealed access cookie. The server denies the request at or after the expiry, and refuses a cookie with an expiry later than the setting allows.
- Each request of the session writes an audit entry with the session ID and the reason. If that write fails, the request fails with 503 and does no work.
- The principal of a break-glass account records the time of its last successful sign-in as its last test. A refused sign-in is not a test. An account that never signed in shows the flag 90 days after creation.
- The principal response shows the last test time and a flag for 90 days with no test. These are new fields, so no client breaks.
- The change edits the baseline migration of each dialect in place and updates the table list. The store port gets a method to record the test, with a conformance test that both adapters run.
- The console shows a banner for the flag with a link to the docs page.
- A site administrator ends a session early with the session generation of the principal. The session check at each request reads the generation.
- A break-glass sign-in started at the identity provider is refused. No sign-in or session end changes the count of enabled break-glass accounts.

Out of scope:

- Two working break-glass paths and the disable guard (item keep-two-working-break-glass)
- The generic settings API itself (item add-one-generic-settings-api)
- Host recovery and temporary administrators
- The review workflow of a break-glass use beyond the audit entries that feed it
- The notification channel by email

Depends on:

- keep-two-working-break-glass
- add-one-generic-settings-api
- `keep-two-working-break-glass` (order of the milestone review)
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused reason says what to type and the limits.
- An expired session says the limit ended and that the person signs in again with a reason.
- The stale banner links to the docs page of the test sign-in, so the check costs one sign-in.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P19)**, Item 17: the break-glass floor: "the PM reading in build. Disable, delete and sweep stop at the last enabled account (ADR 306 d3); every other change keeps the two-working-paths floor (ADR 70 d24); first setup needs two. Reason: the stricter floor wherever the newer owner text is silent; owner ADRs 70 and 306 both kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 25 (changed), 26 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Break-glass accounts are accounts of last resort."
- Quote: "The server records each use. Each break-glass sign-in needs a reason that the person types. It raises an alert."
- Effect: Follows the typed reason, the alert and the audit of each action. The session ends by denial at the limit, and the limit is a setting. This change adds no path to disable or delete an account, so the floor of two working paths stays.
- Conflict: none.

**ADR 142: A second-person review of each break-glass by the next business day** (not owner-tagged).

- Decisions that apply: 1 (changed), 3 (changed).
- Changed by: ADR 149 extends decisions 12 and 15; ADR 210 changes; ADR 222 changes decision 9; ADR 227 changes decision 9; ADR 297 changes decisions 1, 3 and 9. Chain to the end: ADR 149, ADR 210, ADR 222, ADR 227, ADR 297, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 301, ADR 320, ADR 339, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 149, 210, 222, 227, 297.
- Quote: "A review opens at the use."
- Effect: The review opens from the sign-in notification. This change adds the session ID and the per-request audit entries that the review covers. It does not change the review.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "each disable or delete by the sweep writes an audit record."
- Effect: No change to the words. This change adds no disable, delete or sweep path.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 20 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "Sign-in paths."
- Effect: The expiry lives in the sealed cookie. The passkey step keeps the pending sign-in cookie as it is today.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "IdP-started SAML is never a fresh sign-in (owner, 2026-11-07)."
- Effect: A sign-in at the identity provider never gives a break-glass session.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): proposal.md: the change does not state that break-glass session or sign-in must keep at least two working paths; the 13 Nov owner decision (P19) keeps the two-working-paths floor (ADR 70 d24), and the spec and design are silent on it. Resolution: answered: The floor of two paths is a requirement and a design point. The change adds no disable path (queue P19).
- R1.2 (open question): proposal.md Open questions reads 'none' while the 60-minute session limit and the 90-day window are provisional PM decisions; the spec-ready gate is not met until the owner decides. Resolution: answered: The session limit is now a setting, so no fixed value needs an owner answer. The 90-day window is the scope of issue 628.
- R1.3 (missing scenario): specs/break-glass-sign-in/spec.md: no requirement covers disabling or deleting the last enabled break-glass account, which ADR 306 decision 3 refuses with 409 and the 13 Nov decision keeps. Resolution: answered: Disable and delete of the last account belong to keep-two-working-break-glass, named in depends_on and out_of_scope.
- R1.4 (failure mode): specs/break-glass-sign-in/spec.md: the sealed-cookie session expiry cannot be revoked early, yet What Changes promises revocation at expiry; the spec does not say whether expiry is computed from the cookie or stored, nor what happens across a restart or clock skew. Resolution: answered: The expiry is in the sealed cookie, checked at each request. Early end uses the session generation.
- R1.5 (missing scenario): specs/break-glass-sign-in/spec.md: no scenario when the audit write or the WARN log fails during sign-in; the spec does not say whether sign-in succeeds, fails or rolls back. Resolution: answered: An audit failure fails the sign-in with no session. A scenario covers it.
- R1.6 (missing scenario): specs/break-glass-sign-in/spec.md: no requirement covers the reason input limits (length, control characters, log injection in the WARN line) or a break-glass sign-in started at the identity provider (ADR 332 d5). Resolution: answered: The reason has limits and CR LF is refused. A requirement refuses an identity provider start.
- R1.7 (open question): proposal.md: the spec does not say whether a site administrator can end a break-glass session early, although the proposal names an escape hatch of principal disable; this is stated only in the provisional answers. Resolution: answered: A requirement and a scenario cover the early end by the session generation.
- R1.8 (adr conflict): proposal.md ADR check: ADR 297 is marked 'Conflict: none', yet ADR 297 decision 20 requires the sealed pending sign-in cookie and thumbprint, which the spec does not state; ADR 216, 332 and 354 (owner ADRs) are relied on but not listed as touched. Resolution: answered: ADR 297 decision 20 stays as is. ADR 332 is listed. ADR 216 and 354 are not touched by this change.
- R1.9 (scope): tasks.md 2.2 and 3.3: the 90-day stale banner and its link to the test runbook have no implementation task; the console banner required by issue #628 is missing from the tasks. Resolution: answered: A task adds the console banner with the docs link.
- R1.10 (scope): tasks.md 2.2: the last-test column migration is not planned under the one-baseline-migration rule (edit in place per dialect), and the backup table list is not updated, as issue #628 step 4 asks. Resolution: answered: A task edits the baseline in place and updates the table list.
- R1.11 (open question): proposal.md: the change does not say whether per-request break-glass audit entries feed the ADR 142 second-person review, or whether the stale flag appears in the admin access report of ADR 70 decision 29. Resolution: answered: The per-request entries feed the review. The stale flag stays out of the access report in this change.
- R1.12 (wrong claim): proposal.md Evidence: the claim that ADR 70 defines the typed reason, alert and time limit is cited, but decision 25 is marked 'changed' by ADR 306 and the time limit is not stated in the ADR; the proof does not support the stated 60-minute limit. Resolution: answered: The evidence says the ADR does not state the limit. The limit is a setting.
- R2.1 (open question): proposal.md states Open questions: none, yet the 60-minute session limit is a PM provisional decision that ADR 70 decision 25 and ADR 306 do not state; the spec-ready gate needs a decided limit before the spec can be built, and the owner has not answered it. Resolution: open question: The session limit stays an owner question with a recommended value. The change is not spec-ready until the owner answers.
- R2.2 (owner decision conflict): The 13 Nov decision P19 keeps the two-working-paths floor, and the spec sets 'Floor of two paths kept' only for sign-in and session counts. The spec does not state that a sign-in or session never lowers the count below two working paths, and the design of the expiry and the session ends does not check it. Resolution: answered: A requirement and a scenario keep the count unchanged for a sign-in, a session end and two sign-ins at once. Design adds no disable path (queue P19).
- R2.3 (adr conflict): ADR 70 decision 25 says the server revokes the elevated session when the time limit ends. The design says the expiry is only checked at each request in the sealed cookie, with no stored revocation; the spec 'Expiry' scenario only answers 401 at a later request and never states the revocation. Resolution: answered: The expiry requirement now says the server denies the request and writes an audit entry at the limit. Considered states how this meets revocation.
- R2.4 (failure mode): The clock-skew rule says skew above 5 seconds is not accepted, and the design says skew can end a session early. No scenario covers a clock skew or a session whose expiry lies in the future of the server clock, and the rule's effect on a valid session is not stated. Resolution: answered: A future-expiry requirement and scenario cover the skew. The clock-skew rule of 5 seconds is removed.
- R2.5 (missing scenario): The reason limits are 1 to 500 characters with no control character, and the spec covers only CR and LF. Tab, other C0 controls, DEL, Unicode line separators and a 501-character reason have no scenario, although the design layer table names the 501-character case. Resolution: answered: Scenarios cover tab, DEL, line separator and 501 characters.
- R2.6 (failure mode): The alert is written in the sign-in transaction, but the WARN log line is not in the transaction. A failure after the commit of the log line, or a failed log write after the audit write, leaves an alert without a session or a session without a WARN line; the spec does not state the order or the rollback. Resolution: answered: The audit entry commits first. The WARN line follows after the commit. A failed log write does not undo the session.
- R2.7 (failure mode): Concurrency is not covered: two break-glass sign-ins at once, or an early end by session generation during a request, have no scenario. The spec does not state which request wins or whether a per-request audit write fails the request. Resolution: answered: Scenarios cover two sign-ins at once and the end during a request.
- R2.8 (missing scenario): Per-request audit: the spec requires one audit entry per request, but the scenario does not say what happens when that audit write fails. The design layer table says the audit write failure gives no session at sign-in only, so a failing per-request write is unspecified. Resolution: answered: A per-request audit failure gives 503 and no work. A requirement and scenarios cover it.
- R2.9 (open question): The 90-day window is taken from ADR 70 decision 26, but the flag rule uses 'no sign-in for 90 days' while the principal stores only the last test time. The spec does not say whether a failed or refused sign-in counts as a test, and no ADR answers it. Resolution: answered: Only a successful sign-in counts as a test. A scenario covers a refused sign-in.
- R2.10 (wrong claim): The proposal says ADR 70 decision 26 requires the 90-day reminder. The input shows decision 26 is about the typed reason, alert and audit, and the 90-day reminder is in issue 628 only; the cited proof does not support the claim. Resolution: answered: The evidence now says the 90-day reminder is the scope of issue 628, not a claim of ADR 70 decision 26.
- R2.11 (scope): tasks.md 1.3 asks for a conformance test for the last-test time on both store adapters, but the change does not state its store port or the new method, and the issue 624 'Done when' asks for conformance tests for each new port method, which the tasks do not name. Resolution: answered: The change names a store method that records the test, and a conformance test that both adapters run.
- R3.1 (open question): proposal.md: the 60-minute session limit is a provisional PM decision that no ADR states (ADR 70 d25 gives no value), yet the proposal reads 'Open questions: none'; the spec-ready gate is not met and the limit cannot be built right without the owner's answer. Resolution: open question: The session limit stays an owner question with a recommended value. The change is not spec-ready until the owner answers.
- R3.2 (wrong claim): proposal.md Evidence and ADR check: the claim that ADR 70 decision 26 requires the 90-day test reminder is unsupported; decision 26 covers the typed reason, alert and audit only, and the 90-day window is named only by issue 628. Resolution: answered: The evidence and the ADR check no longer claim that an ADR requires the 90-day reminder. It is the scope of issue 628.
- R3.3 (adr conflict): design.md Decision 1 and spec 'Expiry' scenario: ADR 70 decision 25 says the server revokes the elevated session when the time limit ends; the change only denies at the next request with no stored revocation, and the spec does not state how a session at the limit is ended. Resolution: answered: The ADR check and considered now state that the server ends the session by denial at the limit with an audit entry, and why no stored revocation is needed.
- R3.4 (failure mode): design.md Risks: clock skew between processes can end a session early or extend it; the spec 'Future expiry' scenario refuses expiries beyond the maximum limit but gives no bound for the skew, and the cookie cannot be revoked before expiry except through the session generation, which is not stored per cookie. Resolution: answered: Risks state the skew and the expiry bound that limits it. The generation check at each request gives the early end.
- R3.5 (missing scenario): spec.md 'Typed reason required': no scenario for a reason that is accepted at the limits (1 and 500 characters) or for Unicode characters beyond the listed controls, so the boundary of the constructor is untested. Resolution: answered: A scenario covers 1 and 500 characters and non-ASCII letters.
- R3.6 (failure mode): spec.md 'Alert on sign-in' and 'Audit failure': the WARN log line is written after commit, but no scenario covers a crash between commit and the log write, nor a failed log write, so the alert can exist without its WARN line. Resolution: answered: A scenario covers a lost log write, and risks name the crash case. The audit entry is the record.
- R3.7 (missing scenario): spec.md 'Test marker' requirement: the 90-day flag is set only by a successful sign-in, but the spec has no scenario for a break-glass account that never signed in (no last test time) or for the time of the first setup. Resolution: answered: A scenario and a task cover an account that never signed in, counted from creation.
- R3.8 (open question): proposal.md What Changes: the early end by session generation is said to deny each session of the principal, but the sealed-cookie design stores no generation check path; the change does not say where the generation is read at each request. Resolution: answered: The design now says the session check reads the generation at each request.
- R3.9 (scope): tasks.md 1.3 and 2.2: the store method and the conformance test are not named in the design, and the sqlc diff check does not cover the adapter that records the test time. Resolution: answered: The design names the store method and the tasks run the conformance test on both adapters.
- R3.10 (adr conflict): proposal.md ADR check: ADR 142 decision 1 is marked changed and used for the review of account-session uses, but the change feeds per-request audit entries into the review without a stated link to decision 3 (review opens at the sign-in notification). Resolution: answered: The ADR check links the review to decision 3, the notification at sign-in, and to decision 1 for the session ID.
- M.16 (consistency): The session limit is an open value here (60 min), mode 2 proposes 15 minutes, and add-one-generic-settings-api sets tempadmin.session-limit to 1 hour as a setting (ADR 306 d1). Resolution: answered: The session limit is now a key of the generic settings API with the range and shorter-of rule of the temporary administrator session limit. add-one-generic-settings-api is in depends_on. The open question is removed.

## Open questions

none

Questions considered and answered:

- Does a break-glass sign-in need a typed reason, an alert and per-request audit? Yes. ADR 70 decision 26 requires them. ADR 142 decision 1 uses the session ID for the review.
- Does the last-test column need a numbered migration? No. The baseline migration of each dialect is edited in place until the first tag.
- Does this change keep the floor of two working paths? Yes. Owner decision, 13 Nov 2026 (queue P19): every change other than disable, delete and sweep keeps the floor. This change adds no way to disable or delete an account.
- Where does the session limit value come from? A setting in the generic settings API, with the range and the shorter-of rule of the temporary administrator session limit.
- How does the session end at the limit? The sealed cookie holds the expiry. The server denies each request at or after it and writes an audit entry. Early end uses the session generation.
- Does a failed sign-in count as a test? No. A test is a real, successful sign-in.
- What if the alert audit write fails? The sign-in fails closed with no session, because the audit entry is the alert record.
- What if the process stops between the commit and the WARN line? The audit entry exists and is the alert of record. The WARN line can be lost.
- Does an account that never signed in show the flag? Yes, 90 days after its creation.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
