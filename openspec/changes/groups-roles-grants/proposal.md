---
version: 2
---

Roadmap: `docs/roadmap.md:159` (Groups, roles and grants).

## Why

**Persona: site administrator.** A site administrator shares one installation between teams and CI pipelines. Each team gets only the access it needs on its own collection. Periodic review lists each grant with its holder, role, object, creator and time. Main already has groups, grants and the review routes. The gaps are the viewer refusal and the revocation timing, proved through the real CLIs.

The user path this change must make work:

1. **API.** Create a group, add a member and grant writer on collection A with `POST /api/server/namespaces/{namespace}/collections/{collection}/grants`
2. **CLI.** Run `pulumi up` and `tofu apply` with an access token on a unit of collection A and on a unit of collection B
3. **CLI.** Run `pulumi stack export` and `tofu state pull` with a viewer grant
4. **API.** Revoke the grant with `DELETE /api/server/grants/{id}` and retry the CLI command with the same token
5. **API.** Read the access-review export with `GET /api/server/namespaces/{namespace}/collections/{collection}/access-review`
6. **Docs page.** Follow docs/guides/roles-and-grants.md and docs/guides/access-review.md
7. **E2e.** Run the e2e steps with the real Pulumi CLI and OpenTofu and a non-admin access token

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A writer on collection A writes a unit of collection B | 403 and no change | `GET /api/server/namespaces/{namespace}/collections/{collection}/access-review` |
| A viewer runs pulumi up or tofu apply | 403 and no change | `pulumi stack export` |
| A token is used after its only grant ended | 403 on the next request | `GET /api/server/principals/{principal}/grants` |

Evidence for the claims above:

- E2E files already name holders of grants and the access review Proof: e2e/access_test.go, e2e/access_review_test.go (keyword Holder evidence list)
- Guides describe roles, grants and the access review Proof: docs/guides/roles-and-grants.md, docs/guides/access-review.md
- Group grants and built-in roles have tests on main Proof: pkg/access/access_test.go:385 (TestGroupGrants), pkg/access/access_test.go:587 (TestBuiltInRoleFamilies)
- The collection access review has a test on main Proof: pkg/access/review_collection_test.go:65 (TestReviewOfACollection)
- The pack lists the viewer refusal and the revocation timing as gaps Proof: gaps list of the context pack

## What Changes

- Add e2e steps that drive the real Pulumi CLI and OpenTofu for viewer refusal and revocation timing
- Close the gaps that these steps find in the grant check
- Update the guides with the proved behavior

Out of scope:

- The planner role (planner-role)
- Check of reach when a grant gives an act-as right (check-reach-when-grant-membership)
- IP allowlists (ip-allowlists)
- Placement of new units into collections (collections-placement)
- The console views (console)

## Usability

Where the path still costs the person time or surprises them:

- A refused writer sees the missing grant in the error text and finds it with the access-review export.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Group grants have a test (`pkg/access/access_test.go:385 (TestGroupGrants)`)
- Built-in role families have a test (`pkg/access/access_test.go:587 (TestBuiltInRoleFamilies)`)
- The collection access review has a test (`pkg/access/review_collection_test.go:65 (TestReviewOfACollection)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 52: Groups, roles, grants and the access-review export** (not owner-tagged).

- Decisions that apply: 3 (changed), 5 (changed), 10 (extended).
- Changed by: ADR 67 changes decisions 1, 3 and 4; ADR 67 extends decision 10; ADR 70 changes; ADR 203 extends decision 10; ADR 212 extends decision 6; ADR 215 changes decisions 1 and 2; ADR 216 changes decision 5; ADR 222 supersedes decision 9; ADR 223 extends decision 10; ADR 223 supersedes decision 9; ADR 354 extends decisions 1, 3 and 4. Chain to the end: ADR 67, ADR 70, ADR 203, ADR 212, ADR 215, ADR 216, ADR 222, ADR 223, ADR 354, ADR 210, ADR 142, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 225, ADR 241, ADR 286, ADR 300, ADR 353, ADR 232, ADR 235, ADR 301, ADR 365, ADR 248, ADR 273, ADR 335, ADR 351, ADR 240, ADR 313, ADR 362, ADR 247, ADR 149, ADR 297, ADR 339, ADR 295, ADR 299, ADR 343.
- Current rule: this ADR, with ADR 67, 70, 203, 212, 215, 216, 222, 223, 354.
- Quote: "A grant names one holder, one role and one object."
- Quote: "A principal can do an action on an object when a grant on that object, or on an object that holds it, gives a role with that action."
- Quote: "The export covers every grant."
- Effect: The change proves these decisions with the real CLIs and adds no new grant shape.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Write a failing e2e for viewer read and write with `pulumi stack expor" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Fix each gap that the e2e steps find in the grant check" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: task "Update docs/guides/roles-and-grants.md and docs/guides/access-review.m" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: and 2 more.

Questions considered and answered:

- Does writer on one collection reach units of another collection? No. ADR 52 decision 5: reach flows from a grant on the object or on an object that holds it.
- Does the export hold every grant? Yes. ADR 52 decision 10: the export covers every grant.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
