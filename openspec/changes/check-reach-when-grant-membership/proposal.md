Issue: #609, #610, #616, #622.

## Why

**Persona: site administrator.** A site administrator must share token rights without giving a person more access than the granter approved. An act-as edge can grow after the approval. The server must check cover at the time of a grant, keep later growth, and show each widened edge until a covering person acts. The rules follow the provisional owner decisions of 13 Nov 2026 (queue P18 and P21). Implementation uses them as written. A different owner answer reopens only the flag rule and the token issue rule for protected members. Each of them sits in one service. This change uses the reach function and the protected-group computation of the work it depends on. It does not write them.

The user path this change must make work:

1. **API.** The site administrator grants a role with principal:manage-tokens on a principal, to a principal or to a group. The server checks that the reach of the caller covers the reach of the target at that time, and records the approved reach on the edge.
2. **API.** The administrator adds a member to a group that holds such a grant. The server checks cover for each principal that the group can act as, at that time.
3. **API.** Later growth of a group or a principal beyond the approved reach is kept. The server flags the edge.
4. **API.** The administrator lists flagged edges in the access-review export, and acknowledges or revokes each one.
5. **API.** The administrator creates or rotates a token for a member of a protected group. The server checks that the caller reach covers the reach of each such group.
6. **Docs page.** The administrator reads the page about act-as reach and flagged edges, and retries with a covering grant.
7. **E2e.** The suite drives a refused grant, a refused member add, a flagged widened edge with acknowledge and revoke, and a refused token create and rotation.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A grant of a token right is refused because the caller reach does not cover the target reach at the time of the grant. | 403 with text that names the target principal and the missing actions. The server stores no grant. | `GET /api/dustpan/v1/access-reviews?principal=<subject>` |
| A member add is refused because the caller reach does not cover a target of the group at the time of the add. | 403 with text that names the target principal and the missing actions. The server adds no membership. | `GET /api/dustpan/v1/groups/<id>/members` |
| A token create or rotation is refused for a member of a protected group because the caller reach does not cover the group. | 403 with text that names the principal and the protected group. The server issues no token. | `GET /api/dustpan/v1/groups/<id>/members` |
| A person without cover tries to acknowledge or revoke a flagged edge. | 403 with text that names the edge and the missing actions. The flag stays. | `GET /api/dustpan/v1/access-reviews?flagged=true` |

Evidence for the claims above:

- The grant service lives in pkg/access/service.go and the role catalog in pkg/access/roles.go. Proof: pkg/access/service.go
- The member add lives in pkg/principal/service.go. Proof: pkg/principal/service.go
- The token routes live in internal/api/server/tokens.go. Proof: internal/api/server/tokens.go
- AddMember has many call sites, and the store suites call it. Proof: rg -n -F --glob '!*_test.go' 'AddMember' pkg internal cmd found 99 matches, for example pkg/store/storetest/delegation.go:456
- The role RoleAccountAdministrator is used in the store suites, so a change of its description touches tests that read it. Proof: rg -n -F --glob '!*_test.go' 'RoleAccountAdministrator' pkg internal cmd found 30 matches, for example pkg/store/storetest/delegation.go:415

## What Changes

- The grant service refuses a new grant of a role with principal:manage-tokens when the caller reach does not cover the target reach at that time, and records the approved reach.
- The member add makes the same check for each target of the group at the time of the add.
- Later growth of a target or a group is kept. The server flags the widened edge in the access-review export until a covering person acknowledges or revokes it. Each action writes an audit entry.
- A group with write access to every state is protected by the rule of this change. The grant is kept and the edge is flagged. The reach function and the protected-group computation come from the work that this change depends on.
- Token create and rotation refuse for a member of a protected group when the caller reach does not cover the group.
- The server ends an open flag when its edge ends, and writes an audit entry.
- The description of account-administrator states its token limit.

Out of scope:

- The reach function and the token check of the installation holder. The groups-roles-grants change provides the reach function.
- The trust rule model of a group.
- Break-glass and SCIM paths.
- The move of the caller routes to the me prefix.

Depends on:

- groups-roles-grants
- move-caller-routes-api-server
- `groups-roles-grants` (order of the milestone review)
- `move-caller-routes-api-server` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused change names the target and the missing actions, and does not show the full reach sets.
- The access-review export shows each flagged edge with its one next step.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P18)**, Item 16: RFC 128 reach growth and token managers: "Q1 the granter's approval at grant time bounds the edge (ADR 67 d3 unchanged); Q2 (g) keep growth and flag each widened edge until a covering person acknowledges or revokes it; Q10 (iii) both: protect such groups and flag. The FOLLOWS answers as listed. Reason: AWS Access Analyzer, GCP Policy Analyzer; no surveyed product refuses growth. Owner-tagged ADRs touched: ADR 67 d3 (kept), ADR 70 d13 (extended by Q10; text waits)."
- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 67: Who creates and administers a service principal** (not owner-tagged).

- Decisions that apply: 2 (changed), 4 (changed).
- Changed by: ADR 70 changes decisions 2, 3, 4 and 5; ADR 70 extends decision 6; ADR 210 changes; ADR 215 changes decision 7. Chain to the end: ADR 70, ADR 210, ADR 215, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 247, ADR 286, ADR 248, ADR 149, ADR 297, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 70, 210, 215.
- Quote: "**The right to make tokens for a service principal is a grant on that principal.**"
- Quote: "**Only a principal that can manage the access of a service principal can give the right to make its tokens.**"
- Effect: The granter action stays as the first check. The cover check of ADR 70 decision 3 adds to it. The approval of the granter at grant time bounds the edge (Owner decision, 13 Nov 2026, queue P18).
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 13 (changed).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Reach and act-as edges. The reach of a principal is the set of actions on objects that the principal can do."
- Quote: "A grant of an act-as right needs the reach of its target."
- Quote: "The catalog says what account-administrator can do."
- Quote: "No admin role comes from the identity provider by default."
- Effect: Decision 3 is checked at the time of the grant and the member add. Growth after the approval is kept and flagged (queue P18). Decision 13 defines a protected group. Queue P21 reads it to cover write access to every state, and its words wait. This change does not edit them. The token issue check for a protected member follows owner rule P18 Q10: protect and flag.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The change refuses growth that makes a group protected or gives a non-covering act-as path. Owner decision P18 (Q1, Q2 g) keeps the growth and flags each widened edge until a covering person acknowledges or revokes it. The specs in access-grants/spec.md and principal-membership/spec.md contradict this; no flag or acknowledge/revoke scenario exists. Resolution: answered: The specs now keep growth and flag it (owner decision P18).
- R1.2 (owner decision conflict): Owner decision P21 (a) makes such a group protected, and P18 Q10 (iii) protects such groups and flags them. The change treats protection as a trigger for refusal and an open trust question instead of the protect-and-flag rule the owner approved. Resolution: answered: Protect and flag (owner decisions P18 and P21); no refusal for protection.
- R1.3 (open question): The Open questions section reads a provisional answer for the fate of identity provider members on refused trust, yet the change states 'none' in the spec-ready gate. The section is internally inconsistent, and the change cannot be implemented as the gate requires until the question is answered or removed. Resolution: answered: The trust-refusal question is removed. Open questions read none.
- R1.4 (adr conflict): ADR 70 decision 13 is cited as changed and not applying, but the change defines protected groups from it. The protected-group definition is not quoted from decision 13, and owner-tagged ADR 70 decision 13 is extended by P18 Q10 and waits for text. Resolution: answered: ADR 70 decision 13 is quoted and its words are not changed.
- R1.5 (failure mode): The reach check and the write are not required to run under one serializable transaction or lock. Two concurrent grants or member adds can each pass a cover check that the other invalidates, widening act-as reach beyond the granter's cover. Resolution: answered: The check and the write run in one serializable transaction.
- R1.6 (failure mode): The design names the risk that the trust decision is applied outside the grant transaction, but no scenario or hostile test covers a concurrent member add between the protected-state check and the write. Resolution: answered: A concurrent add scenario is added.
- R1.7 (missing scenario): No scenario covers the case where the caller reach shrinks after a token-right grant is stored, the 'maker loses last act-as path' case governed by ADR 67 decision 6; the access review flag is not specified. Resolution: answered: A scenario for the maker that loses the path is added.
- R1.8 (missing scenario): No scenario covers an account-administrator checked against a service principal with a namespace grant, although ADR 67 decision 7 governs namespace principals. Resolution: answered: A namespace principal scenario is added.
- R1.9 (scope): tasks.md item 2.3 e2e steps do not name the refused token rotation path listed in the proposal error table, so not all user-path errors have a task. Resolution: answered: The e2e task names token rotation.
- R1.10 (wrong claim): The ADR check says ADR 67 decision 4 is changed by ADR 70 decision 3, while the effect line says decision 2 keeps its object; the chain statement is inconsistent about which decision changes. Resolution: answered: The ADR 67 effect line is now consistent.
- R2.1 (owner decision conflict): The access-grants spec refuses a non-covering token-right grant, but owner decision P18 Q2 (g) says growth is kept and each widened edge is flagged; the design and proposal still say refuse for grant and member add, contradicting the approved rule. Resolution: answered: Refusal now applies only to a new grant at the time of the grant. Later growth is kept and flagged (queue P18). Design, proposal and specs say the same.
- R2.2 (owner decision conflict): The principal-membership spec refuses a member add that gives a non-covering act-as path, while owner decision P18 keeps the growth and flags the widened edge until a covering person acts. Resolution: answered: The member add checks cover at the time of the add. Growth of the group after that is kept and flagged by a new requirement.
- R2.3 (owner decision conflict): The token spec refuses token create and rotation for members of a protected group without cover; P21 (a) and P18 Q10 (iii) protect such groups and flag them, and the design's 'needs a covering reach' follows the same refusal pattern. Protection and flag, not refusal, is the approved rule. Resolution: answered: The group is protected and flagged (P18 Q10, P21). The new token issue by the caller keeps its cover check, and the design says why.
- R2.4 (adr conflict): ADR 70 decision 3 quoted: 'A grant of an act-as right needs the reach of its target.' The change adds an acknowledge/revoke flow and a protected-group flag, but no scenario covers the flag lifecycle for widened edges created by a grant after approval. Resolution: answered: Scenarios for growth, maker loses the path, acknowledge and revoke now cover the flag lifecycle.
- R2.5 (missing scenario): No scenario covers the flag itself: who sees it, how a covering person acknowledges or revokes it, what the access review shows, and the audit entry. The proposal's error table points to an access-reviews route that no spec defines. Resolution: answered: A requirement with four scenarios covers who sees the flag, acknowledge, revoke, the access review and the audit entry.
- R2.6 (failure mode): The concurrency scenario only covers two member adds. It does not cover a concurrent grant that changes reach between the cover check and the write, nor a revoke that shrinks the granter reach mid-transaction, although the design claims one serializable transaction prevents it. Resolution: answered: Scenarios for a concurrent grant and a revoke during a check are added, with failing tasks.
- R2.7 (open question): The proposal says 'Open questions: none' while the adversarial pass answered several items by reference to owner decisions that still say Provisional; the mapping from provisional answers to implementation rules is not stated. Resolution: answered: The why states that implementation follows the provisional decisions as written and that a different answer reopens only the flag and protect rules.
- R2.8 (wrong claim): The ADR check cites ADR 70 decision 22 as the rule for protected-member token issue, but decision 22 as quoted in the input covers identifying attribute changes, not credential issue to protected members. Resolution: answered: The ADR check no longer cites decision 22 for token issue. It cites decision 13 and the owner rule P18 Q10.
- R2.9 (missing scenario): No scenario covers a token issue or rotation for a principal that is in a protected group and whose caller holds cover only through a namespace grant; the namespace case in access-grants does not reach token issue. Resolution: answered: Two scenarios cover token issue with cover through a namespace grant, and with a namespace cover that is too small.
- R2.10 (scope): Tasks 2.2 adds the flag routes, but the e2e task 3.1 does not name the access-review flag acknowledge or revoke path, so the flagged-edge user path has no e2e check. Resolution: answered: The e2e task now names the acknowledge and the revoke of a flagged edge.
- R3.1 (adr conflict): The change makes the grant refusal at grant time and member add refusal, but ADR 70 decision 3 quoted in the ADR check says 'A grant of an act-as right needs the reach of its target', so the refusal at grant time is consistent; however the ADR check cites ADR 67 decision 4 as changed by ADR 70 while the design's cover check on token issue is not tied to a decision line. Verify the decision 3 quote is an exact line in docs/adr/0070 before implementation. Resolution: answered: The ADR check quotes decision 3 as written in the ADR. The cover check on token issue now ties to ADR 70 decision 2 and owner rule P18 Q10 in the considered list.
- R3.2 (owner decision conflict): The access-token-issue spec refuses token create and rotation for members of a protected group without cover. Owner decision P21 and P18 Q10 (iii) protect such groups and flag them; they do not list a refusal of token issue. The change adds a refusal the owner did not approve, so the rule must be checked against the provisional decision before build. Resolution: answered: The considered list states that the owner decision protects and flags and does not forbid a cover check on a new issue. The rule is marked provisional and sits in one service.
- R3.3 (failure mode): The design says the check and the write run in one serializable transaction with a namespace fence, but no task states the isolation level, the retry on serialization failure, or the behavior when two grants race under a store without serializable support. The store conformance suite is not named for this. Resolution: answered: The design names serializable isolation, a retry of three, and conformance tests on both adapters. A task covers it.
- R3.4 (missing scenario): No scenario covers the flag record lifecycle when the edge is deleted by another path (revoke of the grant, principal deletion, group deletion) while the flag is open. The flag references a stable ID but the cleanup is not specified. Resolution: answered: A requirement with three scenarios ends the flag with its edge. A failing test task covers it.
- R3.5 (open question): The proposal states Open questions: none, yet the owner decisions are provisional and the change says a different owner answer reopens the flag and protect rules. The reopen path (which tasks change, which tests break) is not written down. Resolution: answered: The why and the risks name the reopen path: the flag, protect and token issue services and their tests.
- R3.6 (wrong claim): The proposal says the role description of account-administrator states its token limit, while ADR 70 decision 4 quoted in the ADR check describes the same text; the ADR check does not quote the current catalog text, so the claim that the description is wrong is not shown. Resolution: answered: The evidence list shows RoleAccountAdministrator in the code. The what_changes entry stays as the description change of ADR 70 decision 4.
- R3.7 (missing scenario): No scenario covers an acknowledge or revoke of a flag by a person whose cover is lost between the flag read and the action; the serialization of the acknowledge is not stated. Resolution: answered: A scenario covers lost cover before an acknowledge. The write transaction checks cover again.
- R3.8 (scope): Tasks 2.2 adds flag routes but the proposal error table names access-reviews routes that no spec defines; the route paths and their authorization actions are not in any delta spec. Resolution: answered: A requirement now declares the flag routes with their actions, and the task adds them to the API description.
- M.13 (duplicate): 'Write access protects a group' is also in let-group-trust-identity-provider. The change's own out-of-scope line calls the computation of protected groups blocked work. The 'access review' that holds flags is not named; the access-review export of ADR 52 and the admin access report of ADR 70 d29 are two different reports. Resolution: answered: The out-of-scope line is removed and the protected-group rule stays here. The report is named: the access-review export. The reach function comes from groups-roles-grants. depends_on now lists groups-roles-grants and move-caller-routes-api-server.

## Open questions

none

Questions considered and answered:

- Must a grant of principal:manage-tokens check reach as well as manage-access? Yes, at the time of the grant. ADR 70 decision 3 adds the cover check to ADR 67 decision 4.
- Must the server refuse a group or principal that grows after approval, or a group that becomes protected? No. Owner decision, 13 Nov 2026 (queue P18): keep growth and flag each widened edge until a covering person acknowledges or revokes it. Owner decision, 13 Nov 2026 (queue P21): such a group is protected. Both are provisional.
- Which report shows the flags? The access-review export of ADR 67 decision 6 and ADR 52. The admin access report of ADR 70 decision 29 is a different report and is not changed here.
- Does the token issue refusal for a protected member go beyond the owner decision? The owner decision protects and flags. It does not forbid a cover check on a new credential issue. ADR 70 decision 2 limits a token right by reach. The rule is provisional and sits in one service.
- Does the check race with another write? The check and the write run in one transaction with the namespace fence, with the isolation and retry rules in the design.
- What happens to an open flag when its edge ends? The server ends the flag with the edge and writes an audit entry. The flag holds the stable ID of the edge.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
