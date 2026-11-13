Roadmap: `docs/roadmap.md:153` (SCIM).

## Why

**Persona: site administrator.** As a site administrator I provision people and teams from the identity provider so each person has only the access they need. Today SCIM is missing, so I must create users and groups by hand and removals do not reach sessions and tokens.

The user path this change must make work:

1. **API.** Site administrator creates a SCIM bearer token and configures the identity provider with the SCIM base URL.
2. **API.** SCIM client creates a user and a group and adds the user to the group.
3. **CLI.** Namespace administrator grants a role on a collection to the group; the user runs the Pulumi CLI and OpenTofu with an access token.
4. **API.** SCIM client removes the user; sessions and tokens are disabled and kept, or revoked when the setting is on.
5. **Docs page.** Administrator reads the audit export and the docs page for SCIM setup.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| CLI run with a disabled token after SCIM removal fails. | 401 with code `authn.invalid-token` and body {"code":401,"message":"The access token is not valid. Use a valid token."} | `opendustpan token list --principal <subject>` |
| SCIM read of an unknown user or group fails. | 404 with SCIM error {"schemas":["urn:ietf:params:scim:api:messages:2.0:Error"],"status":404} | `curl /api/dustpan/v1/scim/v2/Users/<id>` |
| SCIM create of a user that exists fails. | 409 with code `scim.exists` and message that names the existing user and the list command. | `curl /api/dustpan/v1/scim/v2/Users?filter=userName eq "<name>"` |

Evidence for the claims above:

- No e2e test names SCIM. Proof: Pack evidence SCIM.e2e is [] and SCIM.e2e_count is 0.
- Guides name SCIM only as related material. Proof: Pack evidence SCIM.guides lists docs/guides/access-review.md and docs/guides/principals-and-groups.md.
- The pack quotes a SCIM roadmap step with create user and group, CLI run through group grant, disable on removal with 401, and opt-in revocation. Proof: Pack roadmap.step: A SCIM 2.0 client creates a user and a group, and adds the user to the group with grant, disable sessions and tokens, next CLI fails with 401, revocation setting on revokes them.

## What Changes

- SCIM 2.0 Users and Groups routes with a SCIM-scoped credential.
- Provisioning port with conformance suite; service writes persons, groups, and memberships with stable IDs.
- SCIM DELETE disables sessions and access tokens at once and keeps them; opt-in setting revokes them.
- Audit entries for each SCIM create, change, membership change, disable, and revocation.
- Docs page and e2e step with the real Pulumi CLI and the real OpenTofu CLI.

Out of scope:

- OIDC, LDAP, SAML, CAS, OAuth 2.0, trusted header, passkeys, and MFA.
- SCIM full sync and directory sync schedule.
- Access review export and console views.
- Placement rules and collections behavior.

Depends on:

- groups-roles-grants
- token-life-cycle-expiry-email
- collections-placement
- one-membership-function-identity-provider
- keep-two-working-break-glass
- add-one-generic-settings-api
- `groups-roles-grants` (order of the milestone review)
- `token-life-cycle-expiry-email` (order of the milestone review)
- `collections-placement` (order of the milestone review)
- `one-membership-function-identity-provider` (order of the milestone review)
- `keep-two-working-break-glass` (order of the milestone review)
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A SCIM misconfiguration disables many users; the docs show a dry-run list before DELETE and the re-enable path.
- SCIM error answers use SCIM error shape, while CLI errors keep the tool-neutral shape; the docs show both.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (changed), 4 (changed), 9 (changed), 11 (changed).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "All of these are in the binary in milestone 4. An operator does not need a proxy for them."
- Quote: "Each method is an adapter behind a port. Sign-in methods implement the `Authenticator` port of ADR 6. SCIM implements a provisioning port."
- Quote: "SCIM feeds groups. SCIM 2.0 creates, changes and removes users and groups. Its groups and memberships are the groups of ADR 52."
- Quote: "A removal disables, and revocation is opt-in. When SCIM or LDAP removes a user, the server disables the sessions and the access tokens of the user."
- Effect: The change follows the SCIM provisioning adapter, the port and conformance rule, the group rule, the disable on removal rule, and the API and audit rule.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 19 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A termination applies at once. When the identity provider deactivates a user, or sends a SCIM `DELETE` for it, the server disables the user at once, as ADR 55 decision 9 says."
- Effect: The change disables the user at once on SCIM DELETE with no delay.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A site admin may disable a break-glass principal (owner, 2026-10-20, accepted as written for now). This changes ADR 70 decision 24."
- Effect: The change uses the guard of keep-two-working-break-glass and refuses a SCIM DELETE that leaves no enabled break-glass principal.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 14).

- Decisions that apply: 10 (current), 13 (current), 14 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The principal source of each method (PM). ADR 70 decision 12 says that each record keeps its source."
- Quote: "One function applies the group values (PM). `principal.ApplyGroupClaims` applies the group values of one identity provider to the memberships of one principal, through the mapper."
- Quote: "The rules of ADR 70 apply as they are (owner, ADR 70). A protected group refuses the add unless it trusts the identity provider or full sync is on (ADR 70 decisions 14, 16 and 17)."
- Effect: The change keeps stable subjects and routes each membership write through the one membership function with the SCIM mapping and the protected group rule.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "SCIM comes later (owner, 2026-11-07). This ADR adds no SCIM. When SCIM comes, it uses the same mapping, as ADR 320 decision 13 says."
- Effect: The change is the later SCIM that reuses the same mapping and stays unbuilt until the owner clears the timing question.
- Conflict: owner_blocks.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): ADR 332 decision 9 (owner, 2026-11-07) says SCIM comes later and ADR 332 adds no SCIM; the change adds the full SCIM surface now. The owner must confirm SCIM is in this milestone before build (openspec/changes/scim/proposal.md, ADR check). Resolution: open question: Added ADR 332 with conflict owner_blocks and an owner open question; build waits for the owner.
- R1.2 (open question): The proposal says Open questions: none, but two questions carry only provisional answers awaiting owner review on 14 Nov. Under the spec-ready gate the change cannot be implemented until the owner decides the SCIM credential scope and the revocation setting scope (openspec/changes/scim/proposal.md, Open questions). Resolution: open question: Open questions now list the owner SCIM timing question plus credential and revocation scope; no claim of none.
- R1.3 (adr conflict): ADR 70 decision 19 says a termination applies at once, while ADR 306 decision 3 (owner) refuses a disable that leaves no enabled break-glass principal. The spec's Disable requirement does not say what happens when SCIM DELETE targets the last enabled break-glass principal; the two rules conflict and the spec is silent. Resolution: answered: Added break-glass guard requirement and ADR 306 check; SCIM DELETE of last enabled break-glass principal is refused with 409.
- R1.4 (missing scenario): No scenario covers SCIM update (PATCH or PUT), membership removal, or the 409 path for a duplicate create, although the proposal lists change and membership change audit entries and the error table names the 409 answer. Resolution: answered: Added update, membership removal, and duplicate 409 requirements with scenarios.
- R1.5 (failure mode): No requirement covers a partial failure of the single transaction (group created, membership write fails) or the rollback result. The design requires one transaction but the spec does not state the failure answer. Resolution: answered: Added atomic transaction requirement with rollback answer.
- R1.6 (failure mode): No scenario covers concurrent creates of the same subject, or a create racing a DELETE. The design names a unique index and one winner, but the loser's response and its audit record are not specified. Resolution: answered: Added concurrent create requirement; loser gets 409 and no extra audit entry.
- R1.7 (missing scenario): The rate limit on mass removals is named as a risk and as a usability concern, but no requirement, scenario or task implements it. Resolution: answered: Added mass-removal rate limit requirement with 429 and task check.
- R1.8 (missing scenario): No scenario covers SCIM DELETE of a user already disabled or unknown (idempotency), nor the in-flight CLI request between disable and rejection. Resolution: answered: Added idempotent DELETE requirement for unknown and already disabled users.
- R1.9 (failure mode): No requirement says whether the disable or revocation and its audit entry commit in one transaction; a restart between the two is not covered. ADR 55 decision 9 requires the audit entry for each disable and each revocation. Resolution: answered: Added audit atomicity requirement that disable or revocation and its audit entry commit together.
- R1.10 (wrong claim): The proposal cites a roadmap step as accepted, but the roadmap row is still 'discussing' under RFC 21 with ADR 55 design questions open; the claim that the step is settled is not supported by the input. Resolution: answered: Reworded evidence claim to describe the roadmap step without calling it settled.
- R1.11 (scope): Task 2.3 names a 'SCIM job' that the repository evidence does not show exists, and no task covers the audit entries as a separate item, so the audit requirement has no acceptance check. Resolution: answered: Renamed e2e task to plain e2e step and added separate audit export acceptance check.
- R2.1 (owner decision conflict): ADR 332 decision 9 (owner, 2026-11-07) says SCIM comes later and adds no SCIM; the change ships the full SCIM surface in milestone 4 and its open question provisionally answers 'ship' without an owner decision. Build must wait for the owner (openspec/changes/scim/proposal.md, ADR check and Open questions). Resolution: open question: Kept ADR 332 conflict as owner_blocks and kept the owner open question; build waits for the owner decision.
- R2.2 (open question): The 'Open questions' section reads 'none' while three questions still carry only provisional answers; under the spec-ready gate the change cannot be implemented until the owner decides them. The section must not read 'none' (openspec/changes/scim/proposal.md, Open questions). Resolution: open question: Open questions list three undecided items with owner and PM deciders; the change is not spec-ready until decided.
- R2.3 (wrong claim): The roadmap row for the directory sync RFC is 'discussing; accepted in part' with design still needed for the open questions of ADR 55, so the proposal's framing of the SCIM step as settled is not supported by the input (openspec/changes/scim/proposal.md, Why evidence). Resolution: answered: Evidence now quotes the pack roadmap step as a pack quote, not as a settled decision, and notes the RFC remains discussing.
- R2.4 (adr conflict): ADR 55 decision 1 lists SCIM as provisioning adapter in milestone 4, yet decision 4 and the scope text point to ADR 332 deferral; the ADR check cites decisions 2, 4, 9 and 11 but the 'Conflict: none' entry for ADR 55 hides the owner-tagged deferral in ADR 332. The conflict must be recorded against ADR 332 (openspec/changes/scim/proposal.md, ADR check). Resolution: answered: ADR check keeps ADR 55 as none and records the owner deferral as owner_blocks on ADR 332 with decision 9.
- R2.5 (missing scenario): The spec has no scenario for the SCIM create or DELETE with a token whose scope is missing or wrong, although the design's hostile test requires 401 with no row written; the 401 path is absent from the spec requirements (openspec/changes/scim/specs/scim-provisioning/spec.md). Resolution: answered: Added scim-auth-scope requirement with 401 and no row written scenarios.
- R2.6 (missing scenario): No requirement covers an invalid or oversize SCIM body, extra fields, or a bad subject; the design's service layer mandates refusal with no row written, but the spec is silent (openspec/changes/scim/specs/scim-provisioning/spec.md). Resolution: answered: Added scim-invalid-body requirement with extra fields, oversize body, and bad subject scenarios.
- R2.7 (failure mode): The break-glass guard and the revocation setting interact: a SCIM DELETE that revokes (not disables) the last break-glass principal's tokens is not covered; the guard names only the disable path (openspec/changes/scim/specs/scim-provisioning/spec.md, Break-glass guard). Resolution: answered: Break-glass guard now refuses with 409 with no disable and no revocation, on both setting paths.
- R2.8 (failure mode): The rate limit on mass removals has no stated limit value, window or per-client scope, so the 429 requirement cannot be built or tested as written (openspec/changes/scim/specs/scim-provisioning/spec.md, Rate limit removals). Resolution: answered: Rate limit requirement now states 100 DELETEs per 5 minutes per SCIM credential with 429.
- R2.9 (missing scenario): The SCIM DELETE of a user who is a break-glass principal and also holds sessions in flight during a restart is not covered by the audit atomicity scenario, which tests only the restart between removal and audit write (openspec/changes/scim/specs/scim-provisioning/spec.md, Audit atomic). Resolution: answered: Audit atomic requirement adds a scenario for break-glass principal with sessions in flight during restart.
- R2.10 (adr conflict): ADR 55 decision 9 says the server disables sessions and access tokens and keeps them for review; the spec's 'Idempotent delete' scenario answers 204 for an already disabled user, which is not stated in ADR 55 and conflicts with the requirement that each disable writes an audit entry (openspec/changes/scim/specs/scim-provisioning/spec.md, Idempotent delete). Resolution: answered: Idempotent delete clarifies the repeat writes no second disable entry; the first disable already wrote its entry per ADR 55 decision 9.
- R2.11 (scope): Task 2.4 asks for an e2e step with the real Pulumi and OpenTofu CLIs, but the change does not show the docs page or console-free path for the 409 break-glass answer; the task list omits the docs page check for the error answers in the proposal table (openspec/changes/scim/tasks.md, 2.5). Resolution: answered: Docs task now checks that each error answer in the proposal table runs as written on the docs page.
- R3.1 (owner decision conflict): The change builds the full SCIM surface in milestone 4, but ADR 332 decision 9 (owner, 2026-11-07) says SCIM comes later and adds no SCIM. Build must not start until the owner clears this conflict (openspec/changes/scim/proposal.md, ADR check). Resolution: open question: ADR 332 recorded as none with decision 9; the change is the later SCIM with the same mapping and ships only after the owner clears the timing open question.
- R3.2 (open question): The change is not spec-ready: open questions on SCIM timing, the SCIM credential scope and the revocation setting scope carry only provisional answers, and the proposal does not read none (openspec/changes/scim/proposal.md, Open questions). Resolution: open question: Open questions list timing, credential scope, and revocation scope; the change is not spec-ready until decided.
- R3.3 (adr conflict): ADR 70 decision 19 says a termination applies at once and ADR 306 decision 3 (owner) refuses a change that leaves no enabled break-glass principal. The Break-glass guard requirement gives 409 with no disable, but the rule that a break-glass principal is refused by SCIM DELETE while sessions are in flight is only partly covered (openspec/changes/scim/specs/scim-provisioning/spec.md, Break-glass guard). Resolution: answered: Break-glass guard now covers sessions in flight and restart race with an atomic refusal scenario.
- R3.4 (wrong claim): The spec says the 401 answer is returned with an audit entry for the refused call, and the audit requirement does not state which actor is written for a refused call without a valid credential, so the claim that an actor is recorded is not supported by the input (openspec/changes/scim/specs/scim-provisioning/spec.md, SCIM auth scope). Resolution: answered: Auth scope requirement now states the refused call writes an audit entry with an empty actor and the credential fingerprint.
- R3.5 (failure mode): The Opt-in revocation scenario revokes sessions and tokens, but no requirement states whether the revocation setting change itself is audited and how a later re-enable of a revoked user works (openspec/changes/scim/specs/scim-provisioning/spec.md, Opt-in revocation). Resolution: answered: Added revocation setting audit and re-enable requirement for revoked users.
- R3.6 (missing scenario): No scenario covers a SCIM create or PATCH that targets a local user or a person from another sign-in provider, nor the case where a SCIM subject collides with a local user subject; ADR 285 decision 2 fixes the local subject format. Resolution: answered: Added local subject collision requirement that refuses SCIM create or link on a local subject.
- R3.7 (open question): The proposal says the ADR 55 decision 4 groups are the ADR 52 groups, but the spec does not state how a SCIM group membership interacts with a protected group that refuses the add (ADR 320 decision 14, owner, ADR 70 decisions 14, 16 and 17). Resolution: answered: Added protected group requirement that refuses SCIM membership add unless the group trusts the identity provider or full sync is on.
- R3.8 (failure mode): The rate limit of 100 DELETEs in 5 minutes per credential is stated without what happens to a removal that a provider retries after 429, so a retried DELETE could be counted twice or dropped silently. Resolution: answered: Rate limit requirement now states a retried DELETE after 429 counts as a new attempt and a repeated DELETE of an already disabled user stays idempotent.
- R3.9 (missing scenario): The Atomic writes requirement covers the create only; no scenario covers a membership write failing during a PATCH or a membership removal, which the design treats as one transaction. Resolution: answered: Atomic writes requirement now covers create and PATCH membership writes in one transaction.
- R3.10 (scope): Task 2.4 requires an e2e step against the real Pulumi and OpenTofu CLIs, but the task list has no task for the grant on the collection that the path step 3 needs, and that grant belongs to collections-placement and groups-roles-grants (openspec/changes/scim/tasks.md, 2.4). Resolution: answered: Task list uses the grant from groups-roles-grants and collections-placement as a precondition and adds no new grant work.
- R3.11 (failure mode): The design allows a restart between the removal and its audit write under one transaction, but the spec never states how a token that is disabled while a CLI request is in flight is answered, so a request that passed the check before the disable may complete. Resolution: answered: Added in-flight request requirement that refuses each new request after disable while an already checked request completes.
- M.8 (dependency): The 'Protected group' and 'Break-glass guard' requirements duplicate rules that one-membership-function-identity-provider and keep-two-working-break-glass own. The revocation setting needs the settings API. Open question 0 (ADR 332 d9, owner-tagged) blocks the build until the owner decides. Resolution: answered: Added one-membership-function-identity-provider, keep-two-working-break-glass, and add-one-generic-settings-api to depends_on. Membership writes route through the one function, the break-glass guard owns the 409 refusal, the revocation setting comes from the generic settings API, and ADR 332 stays owner_blocks until the owner clears the timing question.

## Open questions

1. Does the owner allow SCIM to ship in milestone 4 despite ADR 332 decision 9? Decides: owner.
   - Options: Ship SCIM in milestone 4 with the mapping of ADR 320 decision 13; Defer SCIM to a later milestone per ADR 332 decision 9.
   - Recommended: Ship SCIM in milestone 4 with the mapping of ADR 320 decision 13. Reason: An owner-tagged decision says SCIM comes later, so only the owner clears the conflict.
2. What credential calls the SCIM routes? Decides: PM.
   - Options: Bearer token with a SCIM-only scope on one SCIM route prefix; Reuse of a site administrator access token with no SCIM scope.
   - Recommended: Bearer token with a SCIM-only scope on one SCIM route prefix. Reason: Least privilege fits the SCIM adapter and keeps admin tokens out of the identity provider.
3. What scope has the removal revocation setting? Decides: PM.
   - Options: Revocation is an installation setting that applies to SCIM and LDAP removals; Revocation is a per-provider setting on each SCIM provider.
   - Recommended: Revocation is an installation setting that applies to SCIM and LDAP removals. Reason: ADR 55 decision 9 names a setting with no scope; one installation setting from the generic settings API is simple and predictable.

Not spec-ready: the change conflicts with an owner-tagged decision of ADR 332.

Questions considered and answered:

- Does SCIM need a new group model or mapping? ADR 55 decision 4 says its groups and memberships are the groups of ADR 52, so no new group model. ADR 332 decision 9 says the later SCIM uses the same mapping of ADR 320 decision 13.
- What happens to sessions and access tokens on SCIM removal? ADR 55 decision 9 says disable and keep for review, revoke only when the setting is on, with an audit entry for each disable and each revocation. ADR 70 decision 19 says apply at once.
- Does any current e2e step cover SCIM? No. The pack shows e2e_count 0 and e2e []. The step needs e2e with a SCIM client, the real Pulumi CLI, and the real OpenTofu CLI.
- Can SCIM DELETE remove the last enabled break-glass principal? No. ADR 306 decision 3 refuses a change that leaves no enabled break-glass principal. That guard belongs to keep-two-working-break-glass and this change calls it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
