---
version: 2
---

Issue: #2975.

## Problem

The guide implies that the identity provider cannot manage the members of a protected group. Main only shows the protected flag. The group-claim sync does not apply the rules for protected groups yet. A site administrator who reads the guide can believe that the server refuses a change that it accepts.

## Evidence

- The guide file exists and holds the text that the issue names near lines 243-245. Proof: docs/guides/principals-and-groups.md
- The group-claim sync file exists. The issue says it does not apply the rules for protected groups near line 190, and names the SQLite and PostgreSQL group_claims adapters too. Proof: pkg/store/internal/groupclaims/groupclaims.go
- The issue reports that the guide implies an enforced rule and that main shows the flag only. Proof: #2975
- A conformance test for group claims exists on the PostgreSQL adapter. Proof: TestGroupClaimsConformance at pkg/store/postgres/group_claims_test.go:158

## Fix boundary

- Change the text of docs/guides/principals-and-groups.md near the sentence that the identity provider can still manage the members of a group that is not protected. State that the flag is shown, and that the identity provider sync does not yet refuse changes to a protected group.
- Add one sentence to the guide: the accepted rule says that a protected group refuses an add by the identity provider, and main does not enforce it yet.
- Add one safe step to the guide: until the rule is enforced, do not map an identity provider group to a group whose reach holds a sensitive installation action.
- Edit any other section of the guide that implies the refusal. Record each edit in the pull request description.

Out of scope:

- Enforcement of the protected-group rules in the group-claim sync (issue #2688)
- The change one-membership-function-identity-provider: it enforces the refusal and changes the same guide sentence to the enforced rule in its own pull request
- The catalog of code rules entry for the enforced rule (it lands with the enforcement)
- Any change to the words of an owner-tagged decision
- The SCIM errors and audit entries for a refused add, and any guide text about them

## Check

- Write the check first: a command that fails on the old guide text. Check: rg -n -i 'can still manage' docs/guides/principals-and-groups.md finds the old sentence on main and finds nothing after the fix.
- Edit the guide near lines 243-245 to state that the flag is shown, that the sync does not yet refuse changes to a protected group, and that the accepted rule is not enforced yet. Add the safe step. Fix the sentence about groups that are not protected. Check: rg -n 'protected' docs/guides/principals-and-groups.md shows the corrected paragraph and no sentence that implies a refusal.
- Find every other section that mentions group sync or identity provider membership. Edit each one that implies a refusal. List each edit in the pull request description. Check: rg -n -i 'identity provider|group claim|scim|sync' docs/guides/principals-and-groups.md lists each hit, and the description lists the edits.
- Compare the new wording with the sync code and the SQLite and PostgreSQL group_claims adapters. Confirm that the sync applies no protected-group rule. Check: rg -n -i 'protected' pkg/store/internal/groupclaims pkg/store/sqlite pkg/store/postgres shows no check of the flag in the sync, and the description records the result.
- Comment on issue #2688: this change lands first, and each pull request that enforces the rule, in part or in full, must rewrite the guide sentence and update the catalog of code rules. Check: The comment on issue #2688 exists and the description links it.
- Run docs lint on the edited guide. Check: make docs-lint passes with zero findings.
- Run the full check. Check: make check passes with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The group-claims adapter conformance suite runs on PostgreSQL (TestGroupClaimsConformance). (`pkg/store/postgres/group_claims_test.go:158 (func TestGroupClaimsConformance)`)
- The shared group-claims code applies no protected-group rule, as the issue reports near line 190. (`pkg/store/internal/groupclaims/groupclaims.go:190 (named in the issue)`)
- The principals and groups end-to-end test exists. (`e2e/principals_test.go:40 (func TestPrincipalsAndGroups)`)
- A test covers how the protected check reads the object of each action. (`pkg/access/protected_internal_test.go:49 (func TestProtectedReadsTheObjectOfEachAction)`)

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
- Effect: ADR 70 is not owner-tagged as a whole. Decision 14 sets the accepted rule. This change does not enforce or change it. The owner decision of 13 Nov 2026 (queue P21) reads decision 13 as owner-tagged in its text and is provisional; this change keeps the protected flag and changes no word of decision 13. Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 14).

- Decisions that apply: 14 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The rules of ADR 70 apply as they are (owner, ADR 70)."
- Effect: This decision is owner-tagged. It confirms that the rule of ADR 70 stands. The guide states that rule as accepted and says that main does not enforce it yet. The guide changes no word of the rule. Current rule: this ADR, with ADR 332.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a guide that says the sync does not refuse yet weaken the protected classification? No. Owner decision, 13 Nov 2026 (queue P21), is provisional: such a group is protected, and the text waits. The guide keeps the flag as it is.
- Does this change enforce the refusal? No. ADR 70 decision 14 and ADR 320 decision 14 set the rule. The enforcement is a code change in issue #2688.
- Is an open question needed because the P21 text waits? No. The guide states a fact of main and points to the accepted rule of ADR 70 decision 14. No owner-tagged word changes.
- What is the order against the change that enforces the rule? This change lands first. The enforcing change rewrites the same guide sentence. A task notes this on issue #2688.
- Is a scenario needed? The change is short form and docs-only, so it has no spec and no behavior scenario. The check of the guide text is the task that maps to the issue: it fails on the old sentence on main. A scenario that cannot be written as a behavior test is covered by this check, and no open question remains.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
