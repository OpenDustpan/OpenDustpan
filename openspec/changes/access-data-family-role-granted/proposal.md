---
version: 1
---

Issue: #2924, #2928.

## Problem

A problem: a group with a data-family role on the installation covers every namespace, yet it reads protected false. The API does not say why a group is protected, so a site administrator cannot find the grant to remove. The sensitive-action rule is copied in three places.

## Evidence

- The protected rule, the role check and installation-wide coverage exist on main. Proof: pkg/access/reach.go:520 (func Protected), pkg/access/service.go:1027 (func checkRoleOn), pkg/access/authorizer.go:475 (case ObjectInstallation)
- IsSensitive has no caller outside tests, and SensitiveActions is used only by accesstest. Proof: Pack code facts: rg for IsSensitive finds 2 matches, both in pkg/access/access.go; rg for SensitiveActions finds pkg/access/accesstest/reach.go:1124 and its definition at pkg/access/access.go:257.
- Reach.protected exists as a method; the pack rg for its name finds 0 matches. Proof: Pack main fact: pkg/access/reach.go:306 (func Reach.protected); code fact rg 'Reach.protected' 0 matches.
- The group routes and the guide exist. Proof: internal/api/server/principals.go, docs/guides/principals-and-groups.md

## Fix boundary

- Reach.protected in pkg/access/reach.go calls one shared helper built on IsSensitive. A sensitive action on the installation protects the group, whatever its family.
- The group read returns the causes with the flag: the grant ID, the action and the object, or the act-as edge by principal IDs. It lists only causes on objects that the caller can read, at most 20, sorted by grant ID, and gives the count of the causes it hides.
- The guide table is tested against the output of SensitiveActions.
- The guide says the identity-provider limit for protected groups is not yet enforced.

Out of scope:

- Enforcement of the identity-provider limit for protected groups, and the suspension of members
- Single-walk cost of a page of 1000 groups
- New roles and new actions

## Check

- Add cases to testGroupProtected in pkg/access/accesstest/reach.go: each of namespace:manage-access, namespace:manage-principals and namespace:manage-principal-tokens on the installation gives protected true. A read-only data role on the installation and the same actions on one namespace give false. A grant that ends returns the flag to false. Run both dialects. Check: testGroupProtected fails on main for the three actions.
- Add cases to TestProtectedReadsTheObjectOfEachAction in pkg/access/protected_internal_test.go for the object of each action. Check: TestProtectedReadsTheObjectOfEachAction fails on main for the new cases.
- Add a test in pkg/access/access_test.go that the guide table equals SensitiveActions. Check: The test fails on main while the guide has its own list.
- Add a test in pkg/access/access_test.go that a group with 30 causes returns 20 causes and a hidden count of 10 for a caller who reads 20 cause objects. Check: The new test fails on main.
- Add a hostile test in the NEW file internal/api/server/principals_causes_test.go: a caller who can read the group but not the object of a cause gets the count and no grant ID, action or object of that cause. Check: The hostile test fails on main.
- Make Reach.protected in pkg/access/reach.go call the helper built on IsSensitive in pkg/access/access.go and remove the family copy at pkg/access/reach.go:183. Check: The tests of the first section pass.
- Return the readable causes and the hidden count in the group read in internal/api/server/principals.go and in the API description. Check: A group read returns the grant ID, the action and the object for the test group.
- Edit docs/guides/principals-and-groups.md: build the table from SensitiveActions and mark the identity-provider limit as not yet enforced. Check: The guide test passes.

Depends on:

- `check-action-self-service-source` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- IsSensitive reports whether an action is sensitive. It has no caller outside tests. (`pkg/access/access.go:282 (func IsSensitive)`)
- SensitiveActions returns every sensitive action, sorted. (`pkg/access/access.go:257 (func SensitiveActions)`)
- Protected computes the flag from reach. (`pkg/access/reach.go:520 (func Protected)`)
- Reach.protected holds the family rule. (`pkg/access/reach.go:306 (func Reach.protected)`)
- checkRoleOn refuses a role on an object kind. (`pkg/access/service.go:1027 (func checkRoleOn)`)
- The conformance suite covers protected groups. (`pkg/access/accesstest/reach.go:1435 (func testGroupProtected)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 13 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13; ADR 369 changes decision 9; ADR 372 changes. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 370, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369, 372.
- Quote: "A group is protected when its reach holds a sensitive action that is not confined to one namespace."
- Effect: A role with an access-management or principal-management action granted on the installation covers every namespace, so it is not confined to one namespace and the group is protected. The change reads decision 13 by this sentence, as the owner decision of 13 Nov 2026 (queue P21) says.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Is a group with a data-family role on the installation protected, or is the role refused? Owner decision, 13 Nov 2026 (queue P21): such a group is protected. Provisional; the words of ADR 70 decision 13 wait and this change edits none.
- Should the sensitive rule have one copy? Yes. The pack shows SensitiveActions, the repeated rule in reach.go and the guide table. Reach calls the one helper and a test checks the guide.
- What happens to current identity-provider members of a newly protected group? Nothing in this change. Enforcement is not built, so no member is suspended. The guide says the limit is not yet enforced.
- Which cause list is returned? The causes on objects the caller can read, sorted by grant ID, at most 20. A hidden count covers the rest. An act-as cause names the edge by principal IDs.
- Who may read the causes? A cause shows its object only when the caller can read that object. The caller gets a count for the others, with no ID, action or object. This follows the way the reach refusal hides its count.
- How is an unclassified action treated? It is not sensitive, so it protects nothing. TestUnclassifiedActionIsDenied covers denial on main.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
