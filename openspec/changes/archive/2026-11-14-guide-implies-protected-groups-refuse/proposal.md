Issue: #2975.

## Problem

The guide implies that the identity provider cannot manage the members of a protected group. Main only shows the protected flag. The group-claim sync does not apply the rules for protected groups yet. A site administrator who reads the guide can believe that the server refuses a change that it accepts.

## Evidence

- The guide file exists and holds the text that the issue names near lines 243-245. Proof: docs/guides/principals-and-groups.md
- The group-claim sync file exists. The issue says it does not apply the rules for protected groups near line 190, and names the SQLite and PostgreSQL group_claims adapters too. Proof: pkg/store/internal/groupclaims/groupclaims.go
- The issue reports that the guide implies an enforced rule and that main shows the flag only. Proof: #2975

## Fix boundary

- Change the text of docs/guides/principals-and-groups.md near the sentence that the identity provider can still manage the members of a group that is not protected. State that the flag is shown, and that the identity provider sync does not yet refuse changes to a protected group.
- Add one sentence to the guide: the accepted rule says that a protected group refuses an add by the identity provider, and main does not enforce it yet. The guide does not name a SCIM error code or an audit entry for the refusal.
- Edit any other section of the guide that implies the refusal. Record each edit in the pull request description.

Out of scope:

- Enforcement of the protected-group rules in the group-claim sync (issue #2688)
- The change one-membership-function-identity-provider: it enforces the refusal and changes the same guide sentence to the enforced rule in its own pull request
- The catalog of code rules entry for the enforced rule (it lands with the enforcement)
- Any change to the words of an owner-tagged decision
- The SCIM errors and audit entries for a refused add, and any guide text about them

## Check

- Edit the guide near lines 243-245 to state that the flag is shown, that the sync does not yet refuse changes to a protected group, and that the accepted rule is not enforced yet. Fix the sentence about groups that are not protected. Check: rg -n 'protected' docs/guides/principals-and-groups.md shows the corrected paragraph and no sentence that implies a refusal.
- Find every other section that mentions group sync or identity provider membership. Edit each one that implies a refusal. List each edit in the pull request description. Check: rg -n -i 'identity provider|group claim|scim|sync' docs/guides/principals-and-groups.md lists each hit, and the description lists the edits.
- Compare the new wording with the sync code and the SQLite and PostgreSQL group_claims adapters. Confirm that the sync applies no protected-group rule. Check: rg -n -i 'protected' pkg/store/internal/groupclaims pkg/store/sqlite pkg/store/postgres shows no check of the flag in the sync, and the description records the result.
- Comment on issue #2688: this change lands first, and each pull request that enforces the rule, in part or in full, must rewrite the guide sentence and update the catalog of code rules. Check: The comment on issue #2688 exists and the description links it.
- Run docs lint on the edited guide. Check: make docs-lint passes with zero findings.
- Run the full check. Check: make check passes with zero findings.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 13 (changed), 14 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "The identity provider cannot add a member to a protected group. The server refuses the add."
- Effect: ADR 70 is not owner-tagged as a whole. Decision 14 sets the accepted rule. Decision 13 defines a protected group by its reach. This change does not restate the definition. The guide keeps the flag as it is. This change does not enforce or change decision 14. Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 14).

- Decisions that apply: 14 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The rules of ADR 70 apply as they are (owner, ADR 70)."
- Effect: This decision is owner-tagged. It confirms that the rule of ADR 70 stands. The guide states that rule as accepted and says that main does not enforce it yet. The guide changes no word of the rule. Current rule: this ADR, with ADR 332.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The change writes the guide to say sync does not refuse changes to a protected group, but owner decision P21 (13 Nov 2026, item 19) binds that such a group is protected and cites ADR 70 decision 13 (owner). The change does not reconcile its wording with that binding decision. File: openspec/changes/guide-implies-protected-groups-refuse/design.md. Resolution: answered: The design and considered cite Owner decision, 13 Nov 2026 (queue P21) and keep the protected classification. Only the enforcement claim changes.
- R1.2 (adr conflict): ADR 70 decision 14 says the identity provider cannot add a member to a protected group and the server refuses the add. The ADR check says No ADR is touched and quotes no decision, so this claim is unchecked. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md. Resolution: answered: The ADR check quotes ADR 70 decision 14 and says the change does not enforce it.
- R1.3 (wrong claim): The ADR check reads No ADR is touched, but the owner decision it cites rests on ADR 70 decision 13 (owner) and decision 14 is in the same ADR. The check does not quote them or follow the Changed by chain. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md. Resolution: answered: The ADR check lists decisions 13 and 14 and gives the Current rule chain.
- R1.4 (open question): The Open questions section reads none, but the wording of the guide before enforcement is not answered by any ADR or owner decision. Who decides the text that describes a protected flag as not yet a rule is not named. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md. Resolution: answered: Considered now holds an entry that explains why no open question is needed: the guide states a fact of main and the accepted rule, and no owner-tagged word changes.
- R1.5 (missing scenario): The check reads only the corrected paragraph. The guide text near lines 243-245, which the issue names, and the sentence about the identity provider managing non-protected groups have no scenario. File: openspec/changes/guide-implies-protected-groups-refuse/tasks.md. Resolution: answered: The change is docs-only and has no spec scenario. Considered now has an entry for the paragraph near lines 243-245 and the sentence on groups that are not protected. Two tasks check them with rg on the guide, and a third checks the sync code.
- R1.6 (scope): The issue asks that the guide and the catalog of code rules are updated when #2688 lands. The change records no follow-up item or link to #2688, so the update can be lost. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md. Resolution: answered: Out of scope names #2688, and a task comments on it so the guide and catalog update is not lost.
- R1.7 (failure mode): No task checks the new wording against the code path in pkg/store/internal/groupclaims/groupclaims.go, so a later edit can again state a rule that main does not enforce. File: openspec/changes/guide-implies-protected-groups-refuse/tasks.md. Resolution: answered: A task compares the wording with groupclaims.go and the adapters, and records the result in the pull request.
- R2.1 (wrong claim): The ADR check says ADR 70 decision 14 is extended and quotes it, but ADR 70 is not owner-tagged in its decisions while decision 14 is governed by ADR 320 decision 14 (owner, ADR 70); the change does not state which owner-tagged decision it must respect, so the owner-tagged status of the conflict is not recorded. Resolution: answered: The ADR check now lists ADR 320 decision 14, which is owner-tagged (ADR 70), and says the guide changes none of its words.
- R2.2 (owner decision conflict): The owner decision of 13 Nov (P21) states that a protected group is protected and is only provisional on the text; the change's sentence that the server does not refuse yet is consistent, but the design says the classification is kept only by implication and does not cite the 'text waits' provisional status, so the guide could be read as weakening the binding classification. Resolution: answered: Considered and design cite Owner decision, 13 Nov 2026 (queue P21) as provisional, with the text waiting. The guide keeps the classification.
- R2.3 (open question): The Open questions section reads none, but the owner decision P21 is marked provisional ('text waits'); the wording of the guide that states a protected group as a target rule before the owner accepts the text is not answered by an ADR, and the PM is named as decider without owner review. Resolution: answered: Considered explains why no open question is needed: the guide states a fact of main and quotes no P21 text. No owner-tagged word changes.
- R2.4 (missing scenario): No scenario covers an SCIM request or a group claim at sign-in that tries to add a member to a protected group; the guide wording on what the user sees (SCIM error or audit entry) is not checked against ADR 70 decision 14 behavior. Resolution: answered: Out of scope now names the SCIM errors and audit entries as part of the enforcement change. The guide does not describe them before then.
- R2.5 (failure mode): A guide that says the sync does not yet refuse changes gives no warning to a site administrator who reads it after a partial enforcement lands; no task requires a re-check of the guide when #2688 merges, so the stale wording can persist. Resolution: answered: A task adds a note to #2688 that its pull request updates the guide and the catalog. Risks name the stale wording.
- R2.6 (scope): The proposal names the catalog of code rules as out of scope but the guide also describes the identity provider sync; no task checks the other guide sections that mention group sync for the same implied rule. Resolution: answered: A task checks every other guide section that mentions group sync.
- R2.7 (wrong claim): The Evidence section cites pkg/store/internal/groupclaims/groupclaims.go as holding the sync, but the check in tasks.md points only to line 190 and does not cover the SQLite and PostgreSQL group_claims adapters named in issue #2975. Resolution: answered: The evidence and a task now cover the SQLite and PostgreSQL group_claims adapters through an rg check.
- R3.1 (wrong claim): The ADR check says ADR 320 decision 14 is owner-tagged, and the design claims the rule stays by implication, but the ADR check lists ADR 70 as not owner-tagged while decision 13 (the protected-group definition) is cited as owner-tagged via P21 only; the proposal never quotes ADR 70 decision 13 text, so the protected classification it relies on is unchecked. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md Resolution: answered: The pack text of ADR 70 decision 13 is cut, so the change no longer relies on its words. It keeps the flag and quotes decision 14 only.
- R3.2 (adr conflict): ADR 70 decision 14 says the server refuses the add, and ADR 320 decision 14 (owner) says the rules of ADR 70 apply as they are. The change states that main does not refuse yet but the guide will name this as the target; the fix boundary to add a sentence naming the target rule as stated future behavior is not reconciled with the owner-tagged text of ADR 320 decision 14. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md Resolution: answered: ADR check for ADR 320 decision 14 now explains that the guide states the accepted rule as it is and reports only the gap in main.
- R3.3 (missing scenario): No scenario covers a SCIM request or a sign-in group claim that adds a member to a protected group, nor the user-visible result the guide should state (SCIM error per RFC 7644, or no membership plus audit entry per ADR 70 decision 14). The guide wording on the refusal path is undefined. File: openspec/changes/guide-implies-protected-groups-refuse/tasks.md Resolution: answered: The guide describes no refusal path before enforcement. Considered and out of scope say so. The paths land with #2688.
- R3.4 (open question): The Open questions section reads none, but the proposal names the PM as decider of guide wording about the provisional P21 classification, which the owner delegated only as provisional text waiting for owner review on 14 Nov. Who approves the guide sentence that states the protected rule as target is not named as the owner. File: openspec/changes/guide-implies-protected-groups-refuse/proposal.md Resolution: answered: Considered says no owner-tagged word changes, so no owner approval is needed. The guide quotes no P21 text.
- R3.5 (failure mode): Risk section names stale wording after partial enforcement, but no task makes the guide re-check itself when a partial enforcement (for example only SCIM, or only sign-in claims) lands; the re-check is tied only to #2688 merging. File: openspec/changes/guide-implies-protected-groups-refuse/tasks.md Resolution: answered: The #2688 comment task now covers partial enforcement.
- R3.6 (scope): The check says the PR description records that no other guide section implies refusal, but the fix boundary also asks to remove wording in other sections while the tasks file lists only a grep; the boundary between editing and reporting is unclear. File: openspec/changes/guide-implies-protected-groups-refuse/tasks.md Resolution: answered: The author edits implied-refusal text and lists each edit in the pull request description. The task and the design say so.
- R3.7 (wrong claim): The ADR check states that ADR 70 is not owner-tagged while the design says the decision is owner-tagged via decision 13 (owner) in the P21 record; the owner tag on decision 13 is in the ADR text as changed, so the tag status of decisions 13 and 14 is inconsistent across the files. File: openspec/changes/guide-implies-protected-groups-refuse/design.md Resolution: answered: ADR check says ADR 70 is not owner-tagged as a whole and ADR 320 decision 14 is. The design no longer claims an owner tag on ADR 70.
- R3.8 (open question): The design says the guide states the target rule as not yet enforced, but no decision says whether the guide may name the SCIM error code or the audit entry before enforcement; this is left to the enforcement change without an explicit answer. File: openspec/changes/guide-implies-protected-groups-refuse/design.md Resolution: answered: Design decision states that the guide names no SCIM error code or audit entry before enforcement.
- M.23 (order): one-membership-function-identity-provider enforces the protected-group refusal in the same milestone, so the 'not enforced yet' text becomes wrong after it lands. Resolution: answered: This change lands first. Out of scope, design and a task say that the enforcing change rewrites the same sentence in its pull request, and the task comments on issue #2688.

## Open questions

none

Questions considered and answered:

- Does a guide that says the sync does not refuse yet weaken the protected classification? No. Owner decision, 13 Nov 2026 (queue P21), is provisional: such a group is protected, and the text waits. The guide keeps the flag as it is and quotes no P21 text.
- Does this change enforce the refusal? No. ADR 70 decision 14 and ADR 320 decision 14 set the rule. The enforcement is a code change in issue #2688.
- Is an open question needed because the P21 text waits? No. The guide states a fact of main and points to the accepted rule of ADR 70 decision 14. No owner-tagged word changes.
- What is the order against the change that enforces the rule? This change lands first. The enforcing change (one-membership-function-identity-provider) changes the same guide sentence to the enforced rule in its pull request. A task notes this on issue #2688.
- Which refusal paths does the guide describe? None before the enforcement. ADR 70 decision 14 names a SCIM error and a sign-in audit entry, but the guide does not describe them until the enforcement lands.
- Is a scenario needed for the paragraph near lines 243-245 and the sentence on groups that are not protected? The change is docs-only and has no spec. Two tasks check the paragraph and the sentence on groups that are not protected with an rg command on the guide, and a third task checks that main has no flag check in the sync.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
