---
version: 2
---

Roadmap: `docs/roadmap.md:212` (Console).

## Why

**Persona: site administrator.** A site administrator tries to give each person and each pipeline only the access they need. They add a member, make a group, give and revoke a grant, review the access-review export, see a banner for a token that expires soon, and read the reason that the server gives for a locked section.

The user path this change must make work:

1. **Console.** Open the members, groups and grants pages. Add a member, make a group, give a grant on a collection and revoke it.
2. **Console.** Download the access-review export and read the banner of a token that expires soon.
3. **Console.** Open a section without the grant and read the server reason for the locked section.
4. **Docs page.** Follow the docs page commands against the real server over TLS.
5. **E2e.** Run the console browser test job against the suite server and check the audit entries for each step.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A console section that the principal cannot open stays locked. | The section shows the reason that the access endpoint answers. Run the browser job again. | `make e2e CONSOLE=1` |
| The browser session expires in the middle of a flow. | The session ended. Sign in again in the browser and run the step again. | `make e2e CONSOLE=1` |

Evidence for the claims above:

- The roadmap step requires browser tests that add a member, make a group, give and revoke a grant, download the access-review export, show the expiring-token banner and show the server reason for a locked section. Proof: The browser tests of the console repository run against the server of the suite. They add a member, make a group, give and revoke a grant, and download the access-review export. They show the banner of a token that expires soon. A section that the principal cannot open shows the reason that the server gives.
- The console scope covers members, groups, grants and the access-review export, with locked sections under a draft RFC. Proof: | console | [RFC 19](rfc/0019-users-teams-roles.md) Users, teams and roles               | members, groups, grants, access-review export | discussing; groups, grants and export accepted (ADR 52) |

## What Changes

- Browser-test wiring of the console repository against the real server over TLS, with no new server route.
- Docs page for the console access views with copy-paste commands.

Out of scope:

- Member, group, grant and access-review export routes, actions, row fields and audit entries (groups-roles-grants).
- Token warning field and banner threshold (token-life-cycle-expiry-email changes).
- Caller access route path (move-caller-routes-api-server).
- Reach check on act-as grants and same-answer redaction for unseen objects (check-reach-when-grant-membership).
- Sign-in pages, MFA pages and SCIM pages.
- Console layout and CSV escaping, owned by the console repository.

Depends on:

- groups-roles-grants
- token-life-cycle-expiry-email-warning-email-queue
- token-life-cycle-expiry-email-expiry-warning-display
- move-caller-routes-api-server
- check-reach-when-grant-membership
- `groups-roles-grants` (order of the milestone review)
- `move-caller-routes-api-server` (order of the milestone review)
- `planner-role` (order of the milestone review)
- `check-reach-when-grant-membership` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A failed browser test costs the administrator one switch between the server repository and the console repository.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Race test covers a grant after a membership removal. (`pkg/access/race_test.go:169`)
- Group grant tests cover grants to groups. (`pkg/access/access_test.go:385`)
- The access-review e2e test covers the export. (`e2e/access_review_test.go:103`)
- Console control path test exists. (`pkg/dustpan/console_control_test.go:14`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 10 (extended), 12 (current), 13 (current), 14 (current).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "An endpoint answers the access of a caller. One endpoint answers what a principal can do on one object, and which grants give it. The console uses this endpoint to show what a person can do. The console does not compute access in the browser (ADR 17)."
- Quote: "Formats. The REST API answers the export in JSON. The console also offers it as CSV, made from the same API response (ADR 17)."
- Quote: "Its own action. The export needs its own action, for example `namespace:read-access`."
- Effect: The browser tests use the export and the caller access endpoint without change. The console never computes access in the browser.
- Conflict: none.

**ADR 215: The namespace is the tenant, and OpenDustpan Cloud is multitenant (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current), 10 (current).
- Changed by: ADR 248 changes decision 21. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248.
- Quote: "Each principal and each group has a home (owner). The home of a principal or a group is the installation or one namespace."
- Quote: "A setting chooses the home of new principals, never a build flag."
- Effect: The console makes members and groups in the home that the setting names. No home rule changes.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "A namespace action takes the area `namespace:` and has the namespace as its object."
- Effect: The browser tests call routes with the action of each scope. No action changes.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Can the console compute what a person can do in the browser? No. ADR 52 decision 14 answers it. The console calls the caller access endpoint.
- Does this change define grant routes, export actions or the banner threshold? No. The depends_on changes own them. This change defines no route, no action and no threshold.
- What is the home of a new member? ADR 215 decision 9 and the setting of decision 10 answer it.
- Who owns locked-section layout and CSV escaping? The console repository owns both. This change owns the wiring and the docs page.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
