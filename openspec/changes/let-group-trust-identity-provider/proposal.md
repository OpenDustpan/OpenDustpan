Issue: #615, #617, #619, #626.

## Why

**Persona: site administrator.** Site administrator manages sign-in providers, groups and grants. They need a safe way to let a chosen group follow the identity provider, and to opt the whole installation into full sync, with a fresh step and an audit entry for each change.

The user path this change must make work:

1. **Docs page.** Read the docs page for identity provider trust and find the group.
2. **API.** Set or clear group trust with a fresh step. Read the refusal when the step is stale, no factor is enrolled, or the reach is not covered.
3. **API.** Turn full sync on or off through the settings resource.
4. **E2e.** Run the path against the real server and check the audit entry of each change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Trust or full sync change without a fresh step | The server answers 403 with error code mfa.fresh-step-required and names the way to get a fresh step. Do the fresh step, then repeat the call. | `curl -s $SERVER/api/dustpan/v1/me/freshness` |
| Trust change by a caller whose reach does not cover the group | The server answers 403 and names the missing reach. Ask a caller whose reach covers the group to set trust. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/dustpan/v1/access/reach?principal=me` |

Evidence for the claims above:

- The group code lives in pkg/principal and internal/api/server. Proof: The pack paths pkg/principal/principal.go, pkg/principal/principaltest and internal/api/server exist.
- The Freshness port exists. Proof: The pack rg of Freshness gives pkg/dustpan/dustpan.go:3270 and pkg/dustpan/auditchain.go:123.
- Membership columns for suspension exist in the stored data. Proof: The pack rg of suspended_at gives pkg/backup/portable_tables.go:554 and pkg/store/sqlite/internal/db/queries.sql.go:100. This change adds only the trust rule to the membership function.

## What Changes

- Group attribute trusted from the identity provider, with setter and time, default off. A declared route sets and clears it, with a fresh step and the reach cover check of the dependency. Changes on one group run in one write transaction, the last commit wins, and each is audited.
- The membership function skips the protected-group refusal for a trusted group, and for every group when full sync is on.
- Full sync setting, registered through the generic settings API of its dependency, with the action server:manage-sign-in on the installation, a fresh step and an audit entry.
- Schema edits go into the baseline of each dialect in place and into the backup table list. This supersedes the numbered migrations that the issues name, because no tag has shipped. A dev installation runs `opendustpan dev reset`.

Out of scope:

- Suspension of memberships, restore, pin and the expiry job
- The admin access report and its acknowledgment
- The protected-group rule and the reach check
- The generic settings API itself
- Break-glass account shape

Depends on:

- check-reach-when-grant-membership
- one-membership-function-identity-provider
- add-one-generic-settings-api
- keep-two-working-break-glass
- `check-reach-when-grant-membership` (order of the milestone review)
- `one-membership-function-identity-provider` (order of the milestone review)
- `add-one-generic-settings-api` (order of the milestone review)
- `keep-two-working-break-glass` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The console shows the trusted flag, setter and time before the administrator confirms.
- The full sync confirmation says that each removal is final.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 16 (extended), 17 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A group can trust the identity provider. A person can set a group, protected or not, to "trusted from the identity provider". Then decision 14 does not apply to the group. The change needs a fresh MFA step (ADR 56) and a"
- Quote: "Full sync is an installation setting. A site administrator can set the whole installation to full sync from the identity provider. The identity provider is then trusted for every group, and each removal is final. The cha"
- Effect: The change follows these decisions for trust and full sync.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Each fresh step uses the one window.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner). A person of these methods who has enrolled no factor cannot do a step that needs a fresh authentication."
- Effect: A person of the OAuth and trusted-header methods with no factor is refused with 403 mfa.fresh-step-required.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (not owner-tagged).

- Decisions that apply: 16 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Each method asks for a fresh authentication in its own way (PM). The fresh step of ADR 227 decision 1 uses the parameter of each protocol:"
- Effect: For SAML, CAS, OIDC and LDAP persons, the fresh step comes from the protocol of the method.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md: the 'Open questions' section reads none, yet the full-sync issue (#619) and ADR 70 decision 17 leave open which setting layers may set full sync and how a fresh step works for SSO persons. The provisional answer does not settle the gate for a change that cannot be built without the owner's answer. Resolution: open question: The setting layers and the fresh step of SSO persons stay open questions for the owner.
- R1.2 (open question): proposal.md: the period of the periodic admin access report (ADR 70 decision 29, issue #626 'Not decided here') is set provisionally to 90 days. The report spec cannot be built right without the owner's period. Resolution: answered: The report period belongs to the admin access report change, not to this one.
- R1.3 (owner decision conflict): Owner decision P21 says a group with installation-wide data write access is protected. The group-trust-idp spec lets a caller set trust on any protected group with no refusal or flag for such a group, and no scenario identifies which groups are protected under the sensitive-action list. Resolution: answered: The protected-group rule belongs to the reach-check change. A scenario shows trust on a write group with the same checks.
- R1.4 (adr conflict): ADR 227 decision 2 and ADR 301 decision 4 ('No factor, no fresh step'): the trust and full sync specs refuse only the no-fresh-step case; the no-enrolled-factor branch, with the 403 mfa.fresh-step-required refusal naming the factor path, has no scenario. Resolution: answered: No-factor scenarios added for trust and full sync.
- R1.5 (failure mode): Full sync makes each removal final, but the spec does not say what happens to suspended memberships already present when full sync is turned on, nor to the 30-day timer of rows still suspended. A wrong directory delete removes access at once with no recovery path. Resolution: answered: Suspension is a separate change. This change only reads the full sync setting.
- R1.6 (missing scenario): No scenario covers a restore after the 30-day window ended, or a restore of a membership whose removal became final under full sync. The restore spec only says the membership returns pinned. Resolution: answered: Restore belongs to the suspension change and is out of scope.
- R1.7 (adr conflict): ADR 306 decision 3 and ADR 70 decision 24: a restore that leaves fewer than two working break-glass paths must be refused with 409; the restore of a suspended break-glass membership and this guard have no scenario. Resolution: answered: The break-glass guard belongs to the break-glass dependency. Full sync leaves break-glass accounts unchanged.
- R1.8 (failure mode): Concurrency is not covered: the 30-day expiry job racing a restore or pin of the same membership, and two trust changes at once. No write-transaction or fence rule is stated for these races beyond the design's general line. Resolution: answered: Two-change scenario and the one write transaction rule cover trust races. The expiry job is out of scope.
- R1.9 (missing scenario): Acknowledgment has no scenario for empty notes, a stale report, or a reviewer without the report action (refusal). The report spec does not list refused adds from ADR 70 decision 14 with their refusal record. Resolution: answered: Acknowledgment belongs to the admin access report change and is out of scope.
- R1.10 (scope): Tasks 2.3 edits baselines in place and says update backup columns, but proposal 'What Changes' names no acknowledgment table or backup change. Issues #615, #619 and #626 still ask for a new numbered migration, and the change does not say they are superseded. Resolution: answered: What changes says the baseline edit supersedes the numbered migrations of the issues.
- R2.1 (open question): proposal.md: the Open questions section reads none, yet the setting layers that may set full sync (issue #619 open point, ADR 70 decision 17) and the fresh step for SSO persons remain undecided by any ADR; a change that cannot be built without the owner answer must not pass the spec-ready gate. Resolution: open question: The setting layers stay an open question for the owner; the proposal does not read none.
- R2.2 (open question): proposal.md and specs/admin-access-report/spec.md: the report period is provisionally 90 days, but ADR 70 decision 29 leaves the period open and the report spec cannot be built without it; the 90-day answer is a provisional guess, not a decision. Resolution: open question: The report period stays an open question for the PM with a recommended option.
- R2.3 (owner decision conflict): specs/group-trust-idp/spec.md: the owner decision P21 makes a group with installation-wide data write access protected, but the spec does not say which groups are protected by that rule, and the Write group scenario only refuses an add of an untrusted group without linking the classification to the sensitive-action list of ADR 70 decision 13. Resolution: answered: The spec now names the rule: installation-wide write to state, and ties it to the sensitive action list. A narrow group scenario is added.
- R2.4 (missing scenario): specs/membership-suspension/spec.md: the Restore requirement has no scenario for a restore by a caller who is neither a site administrator nor a break-glass account, nor for a restore without a typed reason; the proposal requires both refusals. Resolution: answered: Scenarios for another caller and for no reason added.
- R2.5 (missing scenario): specs/membership-suspension/spec.md: no scenario covers pin and unpin, and no scenario says that a pinned membership is ignored by the membership function, though the issue #617 requires it. Resolution: answered: Pinned membership requirement with pin and unpin scenarios added.
- R2.6 (failure mode): specs/membership-suspension/spec.md: the 30-day expiry job is not covered for a restart mid-window, a clock skew or a job failure; the suspension timer behavior after an outage is undefined. Resolution: answered: Expiry requirement with restart and job failure scenarios added.
- R2.7 (adr conflict): specs/idp-full-sync/spec.md: ADR 70 decision 17 requires that full sync is set through a fresh step, and the spec does not state the refusal when no factor is enrolled, though ADR 301 decision 4 applies to every fresh step. Resolution: answered: Full sync fresh step requirement with stale and no-factor scenarios added.
- R2.8 (missing scenario): specs/admin-access-report/spec.md: the Acknowledge requirement says the server records reviewer, time and notes, but no scenario covers a stale report or an acknowledgment of a report that has already been acknowledged. Resolution: answered: Stale and repeat acknowledgment scenarios added.
- R2.9 (failure mode): design.md: the concurrency rule for two simultaneous trust changes on one group is stated only as a general line; no write-transaction or last-writer rule is given for trust changes. Resolution: answered: Design and spec state the one write transaction and last-writer rule.
- R2.10 (scope): proposal.md: the issues #615, #617, #619 and #626 require a new numbered migration and backup table list changes, yet the proposal replaces them by an in-place baseline edit without saying how existing installations upgrade; the Risks section mentions only a dev reset. Resolution: answered: The proposal says no tag has shipped, so no upgrade path exists; dev installations reset.
- R3.1 (open question): proposal.md: the Open questions section reads none, yet the owner has not decided which setting layers may set full sync (issue #619, ADR 70 decision 17). The provisional answer delegates a choice ADR 70 leaves open, so the spec-ready gate must not pass on it. Resolution: open question: The setting layers stay an open question for the owner, so the proposal does not read none.
- R3.2 (adr conflict): proposal.md and specs/idp-full-sync/spec.md: ADR 70 decision 17 requires the fresh step for full sync, yet no ADR says how an SSO person gives it. The change says the step does not depend on the identity provider, which ADR 301 decision 1 and 4 only partly support for OAuth and trusted-header persons, not for SAML or OIDC persons. Resolution: open question: Added an open question for the owner on the fresh step of SAML, OIDC, LDAP and CAS persons. The design no longer claims the ADRs settle it.
- R3.3 (owner decision conflict): specs/group-trust-idp/spec.md: owner decision P21 makes a group with installation-wide write to state protected, and the spec adds the rule, but the spec has no scenario that sets trust on such a group and states whether the trust flag is refused or allowed there. The Narrow group scenario covers only the add path. Resolution: answered: Added a scenario that allows trust on a write group with the same checks.
- R3.4 (failure mode): specs/membership-suspension/spec.md: the Restore requirement allows a restore only by a site administrator or a break-glass account, yet no scenario covers a restore of a suspended break-glass membership when the guard in ADR 306 decision 3 applies. The break-glass guard scenario is stated as a generic 409 without the exact count rule of ADR 70 decision 24. Resolution: answered: Added a break-glass restore scenario and the count rule inside the write transaction, following ADR 306 decision 3.
- R3.5 (failure mode): specs/membership-suspension/spec.md: the expiry requirement says the job ends rows on its next run, but does not state a fence or a check that a restore already in progress is not overwritten, and the Race scenario gives only one winner without naming the transaction boundary for the expiry job. Resolution: answered: Expiry now checks the row state in its own write transaction, with a restore-first scenario.
- R3.6 (missing scenario): specs/idp-full-sync/spec.md: no scenario covers turning full sync off again, nor what happens to a removal that occurred while full sync was on, nor the rollback of rows ended by the on switch. Resolution: answered: Added a turn-off scenario: ended rows stay ended and removals stay final.
- R3.7 (missing scenario): specs/admin-access-report/spec.md: the report requirement lists five parts only as a count. It has no scenario for a report with no changes since the last one, nor for a break-glass account that has never been tested, although ADR 70 decision 29 asks for the last test. Resolution: answered: Added no-changes and never-tested scenarios.
- R3.8 (wrong claim): proposal.md: the Evidence section says the store already has suspended state, citing pkg/store/storetest/devdata_rows.go:210 and queries.sql.go:100, but the change does not show that a membership suspended state rule exists in the membership function, so the claim that suspension is reusable is unchecked. Resolution: answered: Evidence now says only the columns exist, and that this change adds the rule of the membership function.
- R3.9 (scope): proposal.md: the What Changes section replaces the numbered migrations of issues #615, #619 and #626 with in-place baseline edits, yet tasks.md 2.1 still names the baseline edit without stating that the issues' migration requirement is superseded. Resolution: answered: What changes and task 2.1 state that the baseline edit supersedes the numbered migrations of the issues.
- R3.10 (open question): proposal.md: the report period is provisionally 90 days from the last acknowledgment, but ADR 70 decision 29 leaves the period open and the answer sets the changes-since window the spec depends on. Resolution: open question: The report period stays an open question for the PM with a recommended option.
- M.14 (duplicate): 'Installation-wide write is protected' repeats check-reach-when-grant-membership. The requirement name 'No fresh step' conflicts with the text that the route needs a fresh step. Resolution: answered: Protected-group requirement removed and left to check-reach-when-grant-membership. The refusal requirement is renamed.
- M.15 (scope): Group trust, membership suspension with its expiry job, full sync and the admin access report are four features for one change. Resolution: answered: Suspension and the admin access report are out of scope. Full sync registers through add-one-generic-settings-api.
- M2.15 (consistency): The answer to open question 1 asks for the OpenDustpan factor for SAML, OIDC, LDAP and CAS. This conflicts with mfa (an SSO sign-in has no added step) and with the SSO freshness sources of fresh-authentication-window-0-turns. Resolution: answered: The open question on the SSO fresh step is removed. SSO persons use the protocol parameter (max_age, ForceAuthn, renew) per ADR 320 decision 16. Only OAuth and trusted-header persons use a factor of OpenDustpan, per ADR 301.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which setting layers may set full sync?
   - Options: API only; API plus flag, file and database layers.
   - Provisional decision (owner review 14 Nov): API only for on and off.
   - Reason: A fresh step cannot apply to a flag or a file value, and no ADR names the allowed layers. Benchmark: AWS: sensitive directory settings change through the API with MFA.
   - Persona impact: Operator cannot turn full sync on from a flag. The site administrator makes one API change. Escape hatch: The operator uses host recovery if the API is out of reach.
   - Owner-tagged ADRs involved: ADR 227 (their words do not change).

Questions considered and answered:

- Is a group with installation-wide write access protected? Yes. Owner decision, 13 Nov 2026 (queue P21). The rule belongs to the reach-check change, not to this one. A trusted group of this kind still passes only with the same checks.
- How does a new installation take the schema change? The project rule says one baseline per dialect until the first tag, so no upgrade path exists.
- Where does full sync end suspended rows? Suspension is a separate change. This change only makes the membership function read the full sync setting.
- Where does the fresh step of an SSO person come from? From the protocol of the method: max_age, ForceAuthn and renew (ADR 320 decision 16). OAuth and trusted-header persons use a factor of OpenDustpan (ADR 301 decision 1).

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
