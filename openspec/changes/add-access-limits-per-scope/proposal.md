Issue: #906.

## Why

**Persona: namespace administrator.** A namespace administrator grants short-term access and caps risky actions with forbid-only limits. Today a grant has no end time and no per-scope ceiling exists, so stale grants keep access and broad rights cannot be narrowed without removing grants.

The user path this change must make work:

1. **Docs page.** Open the docs page for access limits and grant expiry.
2. **API.** Set expiresAt on a grant and list the access review to see the expired mark.
3. **API.** Save a forbid limit at namespace, collection or unit scope and test a refused call.
4. **Console.** View limits next to grants in the access review.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Call refused by an access limit | 403 with gate.ErrLimited that names the limit ID; read the limit policy and ask an administrator to change the limit. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Grant past expiresAt gives no access | 403 with no access from the expired grant; ask an administrator for a new grant and check the access review in the console for the expired mark. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Save with effect permit refused | 400 with gate.ErrInvalid for effect permit; change the policy to forbid. | `PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id}` |

Evidence for the claims above:

- Issue asks for expiresAt on grants and forbid-only Cedar limits per scope with audit entries. Proof: #906 body: In: an optional `expiresAt` on a grant, enforced at check time and swept; access-limit objects and routes; evaluation of forbid policies in the access check; audit entries.
- Access-limit routes exist for namespace, collection and unit scopes. Proof: GET /api/dustpan/v1/namespaces/{namespace}/access-limits and PUT /api/dustpan/v1/namespaces/{namespace}/collections/{collection}/access-limits/{id} and PUT /api/dustpan/v1/namespaces/{namespace}/units/by-name/{tool}/{name}/access-limits/{id}
- ADR 223 defines the forbid-only per-scope limit model used by this change. Proof: docs/adr/0223-access-limits-cedar-forbid-policies-for-each-scope.md decisions 1, 2, 3 and 9.

## What Changes

- Optional expiresAt on a grant, enforced at check time and swept; access review marks expired rows.
- Access-limit objects and routes at installation, namespace, collection and unit scope with forbid-only Cedar evaluation after grants.
- Refusals write decision records and audit entries; docs page ships in the same pull request.

Out of scope:

- Other grant conditions as stored fields besides expiresAt.
- Change of gate or approval behavior beyond the limit guard on gate configuration.
- Console rendering of limits next to grants; the server ships the list and get routes that the console reads.

Depends on:

- #900
- add-gate-configuration-routes
- `add-gate-configuration-routes` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused caller sees the limit ID but not how to request an exception; the docs page lists the one next step.
- Expiry uses UTC timestamps; the review shows local time with the raw value to avoid confusion.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 223: Access limits: Cedar forbid policies for each scope** (owner-tagged: decision 1).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 5 (current), 7 (current), 8 (current), 9 (current), 10 (current).
- Changed by: ADR 240 changes decision 4; ADR 313 extends; ADR 362 changes decisions 4 and 11. Chain to the end: ADR 240, ADR 313, ADR 362, ADR 297, ADR 354, ADR 363, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 313, 362.
- Quote: "Forbid only (owner). An access limit is one Cedar `forbid` policy. A limit set holds `forbid` policies only. The server refuses at save a policy with the effect `permit`, with `gate.ErrInvalid`."
- Quote: "For each scope. An access limit has a scope: the installation, a namespace, a collection or a unit."
- Quote: "After the grants. The `identity.Authorizer` checks the grants first."
- Quote: "Fail closed. A limit that gives a Cedar error refuses the call with `gate.ErrRuleFailed`, never with an allow."
- Quote: "Break-glass accounts are outside the limits. A session of a break-glass account (ADR 70) is not subject to an access limit, so that no limit can block a working path that ADR 70 decision 24 counts."
- Quote: "A refusal writes a decision record. An access limit that refuses a call writes a decision record (ADR 229)."
- Quote: "Changes. A change of a limit needs the action of its scope (ADR 230 decision 2), a fresh authentication (ADR 227), and writes an audit entry."
- Quote: "No limit locks out the gate configuration. The server refuses with `gate.ErrInvalid`: - a limit of a namespace, a collection or a unit that can match the action `installation:manage-gates`."
- Effect: Implements forbid-only limits per scope, check after grants, fail-closed rule errors, break-glass exemption, refusal decision records with 60-second sharing, change controls and the gate-configuration guard.
- Conflict: none.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 10 (extended), 11 (current), 15 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "What a row holds. Each row names the principal or the group, the role, the object and the time of the grant."
- Quote: "API. Each operation on groups, memberships, roles and grants has a REST API and its own action (ADR 17), named in the style of ADR 13 and the actions that exist, such as `unit:read` and `namespace:read-audit`."
- Effect: Adds optional expiresAt to a grant; expired grants give no access and show as expired in the review, pending the short amendment that issue #906 calls for.
- Conflict: rfc_needed.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: Ships the access-limit and grant-expiry docs page in the same pull request.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The proposal uses the route GET /api/dustpan/v1/namespaces/{namespace}/grants for the access review. The code check fails: this route is not in the API description or the route table, so the change is not spec-ready. Use an existing route. Resolution: answered: Replaced the grant-list route with the listed access-limits route from the pack.
- R1.2 (open question): The design adds expiresAt to grants. ADR 52 has no decision for grant expiry and its ADR check says Conflict: none. The issue itself says grant expiry needs a short ADR amendment to ADR 52. No ADR answers this, so the change cannot be built right until that amendment exists. Resolution: open question: Marked ADR 52 as rfc_needed and added the owner open question on the expiresAt amendment.
- R1.3 (wrong claim): The error table uses access.limited, grant.expired and gate.invalid-policy. ADR 223 decision 3 and the spec use gate.ErrLimited and gate.ErrInvalid. The codes in the user path do not match the decision or the spec. Resolution: answered: Error answers now name gate.ErrLimited and gate.ErrInvalid to match ADR 223 and the spec.
- R1.4 (missing scenario): ADR 223 decision 10 refuses a limit that can match installation:manage-gates, or an unconditional namespace:manage-gates. The layer table names a hostile test for this save refusal, and the open-question answer says the spec keeps this guard. No requirement or scenario in specs/access-limits/spec.md covers it. Resolution: answered: Added gate-config-guard requirement and scenario for the decision 10 refusal.
- R1.5 (missing scenario): ADR 223 decision 5 says a limit that gives a Cedar error refuses the call with gate.ErrRuleFailed, never with an allow. The spec has no scenario for a Cedar evaluation error. The fail-closed path is not specified. Resolution: answered: Added fail-closed evaluation-error requirement with gate.ErrRuleFailed.
- R1.6 (missing scenario): ADR 223 decision 8 requires a refusal to write a decision record, with sharing of records for the same principal, action and limit within 60 seconds. The tasks mention decision-record refusal, but no requirement or scenario defines the record or the sharing rule. Resolution: answered: Added refusal-decision-record requirement with the 60-second sharing rule.
- R1.7 (missing scenario): ADR 223 decision 9 and the layer table require that a change without the scope action or without a fresh authentication gets 403 and no write. The spec only has the success audit scenario. The refusal path is missing. Resolution: answered: Added limit-change-refusal requirement for missing scope action or fresh step.
- R1.8 (missing scenario): ADR 223 decision 2 defines limits at the installation, namespace, collection and unit scopes, and narrower scopes add to wider ones. The spec covers only a collection forbid. There is no scenario for namespace, unit or installation limits, or for a limit that adds to another. Resolution: answered: Added scope-addition requirement for installation, namespace, unit and additive scopes.
- R1.9 (open question): The proposal's risk says a broad forbid with no principal condition refuses every caller in scope. ADR 223 decision 10 refuses only the gate-configuration case. No rule, refusal or warning covers a broad limit that locks out a whole namespace or installation. Resolution: open question: Added PM open question on the guard for a broad forbid with no principal condition.
- R1.10 (missing scenario): ADR 223 decision 7 exempts sessions of break-glass accounts from access limits, and says their audit entries state this. The spec and the design do not mention this exemption or its audit entry. Resolution: answered: Added break-glass exemption requirement with its audit statement.
- R1.11 (failure mode): Expiry is enforced at check time and swept, and uses a now function. The spec has no requirement for the sweep, for the clock source, for an expiry set in the past at save, or for a grant changed during a check. Boundary and clock-skew cases are not covered. Resolution: answered: Added grant-sweep and save-time expiry requirements with the clock source.
- R2.1 (adr conflict): specs/grants and design.md add expiresAt to grants, but ADR 52 decision 3 and 5 have no grant-expiry decision and the change's own ADR check marks ADR 52 as rfc_needed; the change must not be built until an accepted ADR amendment to ADR 52 exists (proposal.md ADR check, ADR 52 Conflict: rfc_needed). Resolution: open question: Kept ADR 52 as rfc_needed and kept the owner open question; the change ships only after the expiresAt amendment is accepted.
- R2.2 (open question): Owner must accept the short ADR amendment adding nullable expiresAt to grants of ADR 52; the spec-ready gate stays closed until it is decided (proposal.md Open questions 1). Resolution: open question: Kept the owner open question on the expiresAt amendment as the spec-ready gate.
- R2.3 (open question): A broad namespace or installation forbid with no principal condition can lock out all callers; ADR 223 decision 10 guards only gate configuration. The spec has no requirement for this and the answer is still open (proposal.md Open questions 2; risks in design.md). Resolution: open question: Added broad-forbid-allowed requirement that states the current guard; mitigation stays in the PM open question.
- R2.4 (missing scenario): No requirement covers a grant whose expiresAt is set in the past at save, nor a clock source or clock skew for the now function used by the check and the sweep (design.md Decision 1; specs/grants/spec.md has only the past-expiry and sweep scenarios). Resolution: answered: Added save-past-expiry and check-clock requirements with server time as the clock source.
- R2.5 (failure mode): The sweep and the check race: a grant read during a check while the sweep or an update changes expiresAt, or a restart mid-sweep, is not specified. Atomicity of the grant-expiry mark is not stated. Resolution: answered: Added specs/grants requirement check-independent-of-sweep with scenario check before sweep; it states denial at check time before the sweep marks the row.
- R2.6 (missing scenario): The grant-expiry-review export has no scenario for a history export (ADR 52 decision 10) with a grant that ended by expiry; the end time of an expired grant is not defined. Resolution: answered: Added history-end requirement: a history export shows the expiry time as the end time of an expired grant.
- R2.7 (failure mode): Refusal decision records are shared within 60 seconds, but no requirement covers a write failure of the record or a refusal that happens during an outage; the call must still be refused fail-closed. Resolution: answered: Added record-write-fails-closed requirement: a decision-record write failure still refuses the call.
- R2.8 (missing scenario): A limit that matches a unit label is not covered: ADR 223 decision 10 says a label-reading limit holds only while the label is there and a label change needs unit:edit-labels, but no scenario tests a label removal that lifts or adds a limit. Resolution: answered: Added label-limit requirement and scenario for label removal and the label-edit action.
- R2.9 (wrong claim): The proposal's error table and the usability section say the expired-grant refusal names the expired mark in the access review, but the grants spec says the review marks it expired without a route; the cited route GET .../access-limits is a limits list, not the access review that shows grants. Resolution: answered: Fixed error commands to use a refused data read and point to the access review in the console, not the limits list.
- R2.10 (scope): Tasks 2.2 and 3.1 cover the docs page and console display of limits next to grants (ADR 223 decision 9), but no spec requirement covers the console view; the console part is not specified in the change. Resolution: answered: Moved console rendering to out_of_scope; the server spec covers the list and get routes the console reads.
- R2.11 (failure mode): Break-glass exemption: a session of a break-glass account bypasses limits (ADR 223 decision 7), but no requirement says what happens when a break-glass principal is itself disabled or expired during a call. Resolution: answered: Extended break-glass requirement: exemption holds only for an enabled break-glass principal with a live session.
- M.17 (dependency): add-gate-configuration-routes also specifies the access-limit routes. The gate-configuration guard needs the gate routes. Resolution: answered: Kept the access-limit routes in this change and added depends_on add-gate-configuration-routes for the gate-configuration guard.

## Open questions

1. Does the owner accept the short ADR amendment that adds optional expiresAt to the grant of ADR 52 before this change ships? Decides: owner.
   - Options: Add expiresAt as a nullable grant field by amendment to ADR 52; Hold grant expiry until a full grant-conditions RFC ships.
   - Recommended: Add expiresAt as a nullable grant field by amendment to ADR 52 Reason: Issue #906 says grant expiry needs a short ADR amendment to ADR 52, and no accepted decision yet names expiresAt.
2. What guards a broad forbid with no principal condition that would refuse a whole namespace or installation beyond the gate-configuration guard? Decides: PM.
   - Options: Refuse or warn on a namespace or installation forbid with no principal condition; Allow any broad forbid with only the gate-configuration guard of decision 10.
   - Recommended: Allow any broad forbid but show the matched principals and actions before save Reason: ADR 223 decision 10 guards only gate configuration, so a broad limit can still lock out normal work with no warning.

Not spec-ready: the change conflicts with ADR 52: an RFC comes first.

Questions considered and answered:

- Which grant conditions ship as grant fields? 19.4 starts with expiry only; other conditions come through Cedar forbid-only limits per ADR 223 decisions 1 and 2.
- How to keep limits from locking out gate configuration? ADR 223 decision 10 refuses limits that match installation:manage-gates or unconditional namespace:manage-gates; the spec keeps that guard.
- Where does limit evaluation sit in the access check? ADR 223 decision 3: grants first, then limits in one Cedar request; refusal names the limit ID with gate.ErrLimited.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
