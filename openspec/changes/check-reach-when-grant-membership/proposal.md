---
version: 3
---

Issue: #609, #610, #616, #622.

## Why

**Persona: site administrator.** A site administrator must share token rights without giving a person more access than the granter approved. An act-as edge can grow after the approval. The server checks cover at the time of a grant, keeps later growth, and shows each widened edge until a covering person acts. The rules follow the provisional owner decisions of 13 Nov 2026 (queue P18 and P21). A different owner answer reopens only the flag rule and the token issue rule for protected members. This change uses the reach function and the protected-group computation of the work it depends on. The flag routes are NEW routes of this change.

The user path this change must make work:

1. **API.** The site administrator grants a role with principal:manage-tokens on a principal, to a principal or to a group. The server checks that the reach of the caller covers the reach of the target, and records the approved reach on the edge.
2. **API.** The administrator adds a member to a group that holds such a grant. The server checks cover for each principal that the group can act as.
3. **API.** The administrator grants a role to a group that becomes protected while it has members from the identity provider. The server refuses unless the request carries a trust decision for the group.
4. **API.** The server flags an edge when a write widens it. The administrator lists flags with the NEW route GET /api/dustpan/v1/flagged-edges and acknowledges or revokes each one.
5. **API.** The administrator creates or rotates a token for a member of a protected group. The server checks that the caller reach covers the reach of each such group.
6. **Docs page.** The administrator reads the page about act-as reach and flagged edges, and retries with a covering grant.
7. **E2e.** The suite drives a refused grant, a refused member add, a refused protecting grant, a flagged edge with acknowledge and revoke, and a refused token create and rotation.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A grant of a token right is refused because the caller reach does not cover the target reach. | 403 with text that names the target principal and the missing actions. The server stores no grant. | `GET /api/server/principals/{id}/access-review` |
| A member add is refused because the caller reach does not cover a target of the group. | 403 with text that names the target principal and the missing actions. The server adds no membership. | `GET /api/server/principals/{id}/access-review` |
| A grant is refused because it makes a group with identity provider members protected. | 409 with text that names the group and the number of identity provider members. The server stores no grant. | `GET /api/server/groups/{id}/members` |
| A token create or rotation is refused for a member of a protected group because the caller reach does not cover the group. | 403 with text that names the principal and the protected group. The server issues no token. | `GET /api/server/principals/{id}/access-review` |
| A caller without cover tries to acknowledge or revoke a flagged edge. | 403 with text that names the edge and the missing actions. The flag stays. | `GET /api/dustpan/v1/flagged-edges` |

Evidence for the claims above:

- The grant service lives in pkg/access/service.go and the role catalog in pkg/access/roles.go. Proof: pkg/access/service.go
- The member add lives in pkg/principal/service.go. Proof: pkg/principal/service.go
- The token routes live in internal/api/server/tokens.go. Proof: internal/api/server/tokens.go
- AddMember has many call sites, so a signature change touches the store suites. Proof: code fact: rg -n -F --glob '!*_test.go' 'AddMember' pkg internal cmd, 99 matches, first pkg/store/storetest/delegation.go:456

## What Changes

- The grant service refuses a new grant of a role with principal:manage-tokens when the caller reach does not cover the target reach, and records the approved reach.
- The member add makes the same check for each target of the group.
- The grant service refuses a grant that makes a group protected while it has members with source scim or claim, unless the request carries a trust decision for the group. The decision applies in the same transaction.
- The server detects widening at the write of each source: a grant, a role change, a member add and a principal grant. It compares the new reach with the approved reach and flags the edge. A flag on an acknowledged edge that grows again is new.
- NEW routes: GET /api/dustpan/v1/flagged-edges, and acknowledge and revoke routes under NEW /api/dustpan/v1/flagged-edges/{id}. Each declares its action and object. The list shows only edges that the caller reach covers. The access-review export also lists flags.
- Token create and rotation refuse for a member of a protected group when the caller reach does not cover the group.
- The server ends an open flag when its grant, principal, group or membership ends, and writes an audit entry.
- The description of account-administrator states its token limit.

Out of scope:

- The reach function and the protected-group computation. The groups-roles-grants change provides them.
- The trust rule model of a group beyond the trust decision field of a grant.
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
- The flagged-edge list shows each flagged edge with its one next step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- AddMember is a method of the principal service. (`pkg/principal/service.go:496`)
- The store port declares AddMember. (`pkg/principal/store.go:173`)
- The role RoleAccountAdministrator is a constant of the role catalog. (`pkg/access/roles.go:51`)
- A test checks that a grant on a service principal needs the reach of the granter. (`pkg/access/reach_grant_test.go:153`)
- A test checks that service tokens need manage-tokens. (`pkg/accesstoken/accesstoken_test.go:1050`)

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
- Effect: The granter action stays as the first check. The cover check adds to it. The approval of the granter at grant time bounds the edge (Owner decision, 13 Nov 2026, queue P18).
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 13 (changed), 14 (extended), 16 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Reach and act-as edges. The reach of a principal is the set of actions on objects that the principal can do."
- Quote: "A grant of an act-as right needs the reach of its target."
- Quote: "The catalog says what account-administrator can do."
- Quote: "No admin role comes from the identity provider by default."
- Effect: Decision 3 is checked at grant and member add. Growth is kept and flagged (P18). Decision 13 defines a protected group, and P21 reads it to cover write access to every state. Its words wait and this change does not edit them. A protecting grant with identity provider members needs a trust decision (decisions 14 and 16).
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must a grant of principal:manage-tokens check reach as well as manage-access? Yes, at grant time. ADR 70 decision 3 adds the cover check to ADR 67 decision 4.
- Must the server refuse growth after approval? No. Owner decision, 13 Nov 2026 (queue P18): keep growth and flag each widened edge. Owner decision, 13 Nov 2026 (queue P21): such a group is protected. Both are provisional.
- What happens to identity provider members when a grant protects a group? The grant is refused unless the request decides trust (ADR 70 decisions 14 and 16). A request that decides no trust keeps the refusal.
- When does the server detect widening? At the write of each source, in the same transaction. No later scan exists.
- How is the protected state of a group computed? By the computation of the groups-roles-grants dependency, at each check.
- What does the edge store as approved reach? A sorted set of action and object pairs of the target at approval time. The check is a subset test.
- Does the token issue refusal go beyond the owner decision? The owner decision protects and flags. It does not forbid a cover check on a new issue. ADR 70 decision 2 limits a token right by reach.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
