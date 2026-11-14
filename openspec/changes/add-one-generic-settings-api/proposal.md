Issue: #897, #952.

## Why

**Persona: site administrator.** A site administrator manages installation settings and a namespace administrator manages namespace settings. Today each feature needs its own path, and the temporary administrator lifetime is a fixed 24 hours, so administrators cannot find, script or bound changes in one place.

The user path this change must make work:

1. **API.** List declared settings with `GET /api/dustpan/v1/settings` and read effective values, sources, locked flags and the revision.
2. **CLI.** Change one database value with an Idempotency-Key. Read the new effective value and the new revision.
3. **API.** Try a locked key, an unknown key, a weaker change with no fresh step, a missing Idempotency-Key and a stale revision. Read each refusal.
4. **Console.** Open the settings page of a scope. The console reads the same resource, shows locked keys with no control and shows the reason of a refusal.
5. **Docs page.** Read the settings guide and the note on the temporary administrator session limit.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| PATCH reduces security without a fresh authentication | 403 with code mfa.fresh-step-required. The error names the path of a new sign-in. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH writes a key that the static layer locks | 409 with code settings.locked. The error names the switch and says the operator locked it. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH names an undeclared key | 422 with code settings.unknown-key naming the key. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH writes a sensitive key that only an environment variable or a file can set | 409 with code settings.file-only naming the key. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH sets tempadmin.session-limit outside 1 minute to 24 hours | 422 with code settings.invalid-value naming the key and the range. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH carries a base revision that is not the current revision | 412 with code settings.stale-revision and the current revision. Nothing changes. | `GET /api/dustpan/v1/settings` |
| PATCH has no Idempotency-Key header | The refusal of the idempotency rule of the tool-neutral API. Nothing changes. | `GET /api/dustpan/v1/settings` |
| A retry reuses an Idempotency-Key with a different body | The refusal of the idempotency rule of the tool-neutral API. Nothing changes. | `GET /api/dustpan/v1/settings` |
| The stored session limit is outside the range and the host command refuses to create a temporary administrator | The host command refuses, names the key tempadmin.session-limit and the command that resets it, and writes an audit entry. No record exists. | `opendustpan host create-temp-admin --reset-session-limit` |

Evidence for the claims above:

- Modes bootstrap and signed-request exist on main with the host command in cmd/opendustpan/host.go. Proof: cmd/opendustpan/host.go exists. The rg for bootstrap found 238 matches, including cmd/opendustpan/host.go line 70.
- Temporary administrator domain code lives in pkg/tempadmin and uses a fixed 24-hour lifetime. Proof: pkg/tempadmin exists. Issue 952 states that it uses a fixed 24-hour lifetime. The text of ADR 245 decision 10 says 24 hours.
- The coverage matrix lists the four settings routes with no console and weak audit or refusal cells. Proof: coverage_gaps of the pack lists GET and PATCH on /api/dustpan/v1/settings and on /api/dustpan/v1/namespaces/{namespace}/settings with empty cells.
- The work follows the routes of ADR 212 decision 7. Proof: Issue 897 scope asks for those routes under the tool-neutral prefix with per-setting schema, change log, fresh MFA and the static-layer lock.

## What Changes

- Completes GET and PATCH /api/dustpan/v1/settings and /api/dustpan/v1/namespaces/{namespace}/settings with per-setting schema, atomic merge patch, revision, locks and audit entries.
- Enforces fresh authentication for changes that reduce security.
- Adds the installation setting tempadmin.session-limit with a default of 1 hour. A record and its token end at the shorter of the setting and the token policy maximum. A change applies to new records only.
- Adds a reset option to the host command so an operator can recover from a bad stored limit with no API.

Out of scope:

- Individual new settings beyond the temporary administrator session limit; each feature adds its own key.
- Changes to the recovery modes bootstrap and signed-request.
- The edit of the words of ADR 245 decision 10; that text waits for the owner.
- The console code itself; it lives in the console repository and reads this resource.

## Usability

Where the path still costs the person time or surprises them:

- Two scopes with two paths can confuse people; the docs page shows both paths with commands to copy.
- A refused weaker change can surprise; the error names the path of a new sign-in.
- The words of ADR 245 decision 10 still say 24 hours until the owner edits them; the docs page states the 1 hour setting.
- The console page is built in the console repository; until then people use the API or the CLI.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P12)**, Batch 10: #952 temporary administrator session limit: "1 hour default, as a setting; the record and its token end at the shorter of the setting and the token policy maximum. Reason: the owner's own words (21.6, and ADR 306 d1 "session limit 1 hour, as a setting"); short-lived emergency access is the norm (AWS, Microsoft). ADR 245 d10 (24 h) text waits for the owner."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3, 7).

- Decisions that apply: 3 (changed), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Quote: "Fresh MFA for a change that reduces security, and for approvals.  **Settings.** A change of an installation setting or of a namespace setting through the API needs a fresh MFA step when it makes the installation or the namespace less secure."
- Effect: Implements the two settings resources, the merge patch, and the fresh step for weaker changes.
- Conflict: none.

**ADR 245: Host recovery: database access, recovery modes and break-glass controls** (not owner-tagged).

- Decisions that apply: 10 (current).
- Changed by: ADR 295 changes decision 12. Chain to the end: ADR 295.
- Current rule: this ADR, with ADR 295.
- Quote: "One record lifetime. A record and its token end at the same time: 24 hours after the create, or the maximum of the token policy when that is shorter."
- Effect: The change ends a record at the shorter of the setting (default 1 hour) and the token policy maximum. This differs from the 24 hours in decision 10. Decision 10 is not owner-tagged. Owner decision, 13 Nov 2026 (queue P12, provisional) sets the 1 hour setting and says the text of decision 10 waits for the owner. The change does not edit those words. Code that ships the 1 hour default waits until the owner confirms P12 and amends decision 10. Until then, only the settings API ships and the limit key stays undeclared.
- Conflict: rfc_needed.

**ADR 219: A lock in the static layer can turn a feature off (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A lock can set a switch to off (owner). The static layer can lock each declared switch to on or to off, also a security switch."
- Quote: "A lock is visible. The settings resource of each scope (ADR 212 decision 7) answers the switch with `locked: true` and `source: static`."
- Effect: A read of each scope shows a locked key as locked with source static. A write of it names the lock.
- Conflict: none.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 6 (current), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).**"
- Quote: "One JSON style guide. Each tool-neutral route follows these rules."
- Effect: Both PATCH routes accept the Idempotency-Key and follow the JSON style guide.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 3).

- Decisions that apply: 3 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "A window of 0 is the strictest value, so a change to 0 needs no fresh authentication. A change to a longer window needs one, as ADR 212 decision 3 says for a weaker setting."
- Effect: The fresh step uses the one window. A longer tempadmin.session-limit is a weaker direction and needs the step. A shorter one needs none.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The settings reference page and the limit note ship in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): ADR 245 decision 10 still reads '24 hours after the create, or the maximum of the token policy when that is shorter'; the change ships a 1 hour setting by amendment before the owner amends that text, which the owner decision of 13 Nov says waits for the owner. Specs and design treat it as amended. Resolution: answered: The ADR check keeps rfc_needed. The limit capability ships only after the owner confirms P12 and amends decision 10. The settings API does not wait. The change does not edit the words.
- R1.2 (owner decision conflict): The owner decision says the record and its token end at the shorter of the setting and the token policy maximum; design.md Decision 3 and the spec scenario 'end 60 minutes after creation' omit the cap, so a 60 minute record can outlive the token policy maximum. Resolution: answered: The Default limit requirement and design use the shorter of the setting and the token policy maximum, with a cap scenario. The 60 minute scenario assumes a maximum of 24 hours or more.
- R1.3 (missing scenario): No requirement covers a stored tempadmin.session-limit value that is unparseable or outside 1 minute to 24 hours, nor the refusal or default fallback when the creator reads it; design names a parse-error risk with no behavior. Resolution: answered: The Bad stored value requirement fails closed, audits, and names the null patch and the host reset as recovery.
- R1.4 (missing scenario): No scenario covers PATCH of tempadmin.session-limit: range check, 422 answer, and whether a longer limit needs the fresh MFA step under ADR 212 decision 3. Resolution: answered: The Range requirement covers 422, the fresh step for a longer limit, a stale revision and null.
- R1.5 (missing scenario): The user path requires 422 settings.unknown-key and the file-only sensitive key refusal (ADR 212 decision 2 gives 409 for a SMTP password PATCH), but the spec has no scenarios for unknown keys, file-only sensitive keys or the SMTP password. Resolution: answered: Requirements cover unknown keys and file-only keys, including the SMTP password.
- R1.6 (failure mode): Idempotency-Key is required by ADR 210 decision 6 (owner-tagged) but no scenario defines replay with the same key and a different body, or retry after partial failure under the all-or-none rule. Resolution: answered: The Idempotent retry requirement covers replay, a changed body and a replay during the first request. A failed patch changes nothing, so a retry runs the whole patch again.
- R1.7 (failure mode): No requirement covers concurrent PATCHes with a stale base revision or a PATCH during a reload that moves the revision; no stale-revision refusal is defined. Resolution: answered: The Stale revision requirement covers an old revision and a patch after a reload.
- R1.8 (open question): ADR 212 decision 7 says null deletes the database value and reverts to default; the spec does not say how null behaves for the session limit, or how a reload that changes static values interacts with the all-or-none rule and audit entries. Resolution: answered: A null scenario for the limit gives the default of 1 hour. A reload entry names old and new source and value.
- R1.9 (missing scenario): The namespace settings path (GET and PATCH under /api/dustpan/v1/namespaces/{namespace}/settings) and its authorization have no scenario; tasks name only installation actions. Resolution: answered: A namespace read scenario and a refusal scenario exist. Tasks name both actions.
- R1.10 (open question): Behavior of a running temporary administrator record when the token policy maximum is lowered is not stated, and the spec does not say what the limit applies to beyond new records. Resolution: answered: A running record keeps its stored end time when the token policy maximum is lowered; a scenario says so.
- R1.11 (wrong claim): The proposal says ADR 227 decision 3 makes 0 ask each time while the ADR check and the adversarial pass disagree on the fresh step window; the proposal also cites ADR 219 decision 3 for a locked and source static answer that the spec does not require for every scope. Resolution: answered: The locked and source static answer is now a scenario for the read of each scope. The ADR 227 quote states the window rule.
- R2.1 (wrong claim): proposal.md ADR check claims ADR 245 decision 10 is 'Current' and the change follows the owner decision, but ADR 245 decision 10 quoted text still says 24 hours with no setting; the change ships a 1 hour setting while the ADR conflict is listed as 'none'. The conflict with ADR 245 decision 10 must be recorded as an adr_conflict until the owner amends it. Resolution: answered: The ADR check for ADR 245 now records the conflict as rfc_needed and says the words wait for the owner.
- R2.2 (adr conflict): ADR 245 decision 10 says 'One record lifetime... 24 hours after the create, or the maximum of the token policy when that is shorter.' The change ends records at 1 hour by default and at a configurable limit, which contradicts that decision line. The ADR check states 'Conflict: none', which is wrong. Resolution: answered: Same entry: the conflict is no longer none. Decision 10 is not owner-tagged, so it is rfc_needed.
- R2.3 (owner decision conflict): The owner decision P12 requires the shorter of the setting and the token policy maximum. design.md Decision 5 and the spec scenario 'Default expiry' end the record at 60 minutes with no cap; the cap appears only in a later scenario. The design and default requirement should state the cap as the rule. Resolution: answered: Design decision 5 and the Default limit requirement state the cap as the rule at every value.
- R2.4 (missing scenario): The spec has no scenario for a PATCH that writes tempadmin.session-limit while a fresh step is present but the value is invalid, and no scenario for the 412 refusal of the limit key. The Range requirement covers only the 25 hour value and the no-fresh-step case. Resolution: answered: Scenarios added: valid fresh step with an invalid value, and 412 for the limit key.
- R2.5 (failure mode): Concurrent temporary administrator creation during a settings PATCH that lowers the limit is not specified: which limit value a create reads (before or after the transaction commit) is undefined, so a record may use a stale limit. Resolution: answered: The creation reads the limit in its own transaction; a scenario and a considered item cover it.
- R2.6 (failure mode): The fail-closed rule for a bad stored limit blocks all temporary administrator creation, including break-glass in an emergency. No scenario states a recovery path other than fixing the database row by hand, and the command that helps is not named in the error table. Resolution: answered: A recovery scenario and an error row name the null patch as the command that helps.
- R2.7 (missing scenario): No scenario covers the restart or reload path: a reload that changes the effective limit moves the revision and writes an audit entry (design Decision 3), but no spec scenario tests it. Resolution: answered: A reload scenario is added to Read effective settings, with a test in the task list.
- R2.8 (failure mode): Idempotency replay is specified only for the same key and body. The case where the first PATCH is still in flight when the retry arrives (same key, concurrent) is not covered. Resolution: answered: A scenario covers a replay during the first request.
- R2.9 (scope): tasks.md names the installation action only in the route task for the namespace scope but 2.1 lists both actions; the namespace read scenario has no test task in section 1 (1.1 names only the installation and namespace GET tests without a namespace-authorization refusal test). Resolution: answered: A namespace refusal scenario and test task are added.
- R2.10 (open question): ADR 212 decision 3 says the fresh step uses the window of ADR 227; ADR 227 decision 3 window of 0 needs no step. The change does not say which window applies when the limit is raised from 0 to a value, leaving the step rule for 0 open. Resolution: answered: The window of 0 is a value of another setting; a raise from 0 to a longer window needs the step, as ADR 227 decision 3 says. The limit key has no 0 value.
- R3.1 (adr conflict): ADR 245 decision 10 says a record ends '24 hours after the create, or the maximum of the token policy when that is shorter'; the change ends records at a configurable limit defaulting to 1 hour. The change records this as rfc_needed but still ships the behavior; the owner decision says the decision 10 text waits for the owner, so the change cannot be built until an RFC or the owner amends decision 10. Mark spec-ready gate as blocked. Resolution: answered: The ADR check states the conflict as rfc_needed and that implementation waits for the owner to confirm P12 and amend decision 10. The change does not edit the words.
- R3.2 (owner decision conflict): The owner decision P12 is recorded as 'Provisional' in the input, while the proposal lists it as approved on 13 Nov; the change treats it as binding without noting the provisional status. Confirm the owner decision is final before implementation. Resolution: answered: The ADR check and considered now state that P12 is provisional and that implementation waits for its confirmation.
- R3.3 (failure mode): Fail-closed on a bad stored limit blocks all temporary administrator creation, including break-glass in an emergency. The recovery path depends on a PATCH by another administrator, which may be unavailable, so the operator has no path via the host command. The error table names a null PATCH but the host command path is not defined for this case. Resolution: answered: The host command gets a reset option that deletes the bad value with an audit entry, so recovery needs no API. A requirement, scenario, task and error row cover it.
- R3.4 (missing scenario): No scenario covers a PATCH whose null or value for tempadmin.session-limit is sent without any Idempotency-Key header, although ADR 210 decision 6 (owner) requires the header on every mutating route. The idempotency requirement assumes the key is present. Resolution: answered: A requirement and scenario refuse a patch with no Idempotency-Key header.
- R3.5 (failure mode): Audit entries for the session limit are written inside the PATCH transaction, but no scenario says what happens when the audit write fails: the all-or-none rule and the audit-entry rule are not tied together for the failure of the audit sink. Resolution: answered: The audit entry is written in the same transaction; a scenario and test show that an audit failure fails the whole patch.
- R3.6 (missing scenario): The fresh-step rule treats a PATCH as weaker when one key is weaker, but no scenario covers a PATCH that mixes a weaker and a stronger key, or a PATCH that only lowers the limit while the default equals the stored value. Resolution: answered: A mixed weaker and stronger patch scenario and a shorter-limit scenario are added.
- R3.7 (open question): The reload rule moves the revision for a source change, but the design does not say whether a reload that changes only the source (not the value) writes an audit entry with old and new values, which the audit requirement needs. Resolution: answered: The design says a reload entry names old and new source and value; the scenario says so.
- R3.8 (wrong claim): The proposal claims ADR 219 decision 3 requires locked and source static for every scope, but ADR 219 decision 3 only names the settings resource of each scope in the example; the spec applies it to the locked key read only, not to the source field of a namespace write refusal. Resolution: answered: The claim is narrowed: locked and source static apply to the read of each scope, and a write refusal names the lock.
- R3.9 (scope): Tasks name the idempotency test but the change also changes the browse of the settings routes in the console path; the console is listed in the user path of the proposal, yet the tasks contain no console step and the coverage gap for the console cell is not closed. Resolution: answered: The console code is out of scope here; a task checks that the API gives the console what it needs and links an issue in the console repository.

## Open questions

none

Questions considered and answered:

- What is the shape of the generic settings API? ADR 212 decision 7: two resources, effective value and source, one revision, merge patch that applies all or none. ADR 219 decision 3 adds the locked flag.
- When does a settings write need a fresh step? ADR 212 decision 3: the whole PATCH needs the step when one key reduces security. ADR 227 decision 3 sets the window rule.
- What ends a temporary administrator record? Owner decision, 13 Nov 2026 (queue P12, provisional): 1 hour default as a setting; the record and its token end at the shorter of the setting and the token policy maximum.
- Does ADR 245 decision 10 block the limit? Yes, until the owner confirms P12 and amends decision 10. The temp-admin-session-limit capability waits for that. The settings API does not wait, and this change does not edit the words.
- What does a null do? ADR 212 decision 7: a null deletes the database value and the effective value goes to the default.
- What happens to a running record when the setting or the token policy maximum changes? The record keeps its stored end time. Only new records read the new value.
- How does a retry behave and what if the header is missing? ADR 210 decision 6: the same key and body replays the first answer; another body is refused; a request with no key is refused by the same rule.
- What if the audit write fails? The audit entry is written in the same transaction as the change. If it fails, the whole PATCH fails and nothing changes.
- Does a reload that changes only the source write an audit entry? Yes. The entry names the key, the old and new source and the old and new effective value.
- How does an operator recover from a bad stored limit? The range check stops a bad value at the write. For a bad row, the host command opens the database and resets the key with an option, so recovery needs no API. The reset writes an audit entry.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
