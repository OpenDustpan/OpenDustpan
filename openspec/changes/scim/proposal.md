---
version: 3
---

Roadmap: `docs/roadmap.md:153` (SCIM).

## Why

**Persona: site administrator.** As a site administrator I want to provision people and teams from my identity provider, so that each person has only the access they need. Today I create users and groups by hand, and a removal in the identity provider does not reach sessions and access tokens.

The user path this change must make work:

1. **API.** The site administrator creates a SCIM credential through a NEW route and gives the SCIM base URL and the credential to the identity provider.
2. **API.** The SCIM client creates a user and a group and adds the user to the group.
3. **CLI.** A namespace administrator grants a role on a collection to the group. The user runs the Pulumi CLI and OpenTofu with an access token.
4. **API.** The SCIM client removes the user. The server disables the sessions and access tokens and keeps them. With the revocation setting on, it revokes them.
5. **Docs page.** The administrator reads the SCIM docs page and the audit export for each entry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A CLI command with a token of a removed user fails. | 401 from the server. | `pulumi up` |
| A SCIM request has no SCIM credential or a wrong one. | 401 in the SCIM error shape and an audit entry. | `GET /api/dustpan/v1/scim/v2/ServiceProviderConfig` (NEW route) |
| A SCIM create repeats a stored subject. | 409 in the SCIM error shape. | `GET /api/dustpan/v1/scim/v2/Users` (NEW route, with a filter on the user name) |
| A burst of removals passes the limit. | 429 in the SCIM error shape. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- No e2e test names SCIM. Proof: Pack evidence SCIM: e2e is empty and e2e_count is 0.
- Two guides name SCIM. Proof: Pack evidence SCIM guides: docs/guides/access-review.md and docs/guides/principals-and-groups.md.
- The roadmap step asks for create, group grant, disable on removal, 401 and opt-in revocation. Proof: Pack roadmap.step text for item m4-step-03.
- Group membership and disable races have tests on main. Proof: pkg/access/race_test.go:169 and pkg/access/race_test.go:382.

## What Changes

- NEW routes under /api/dustpan/v1/scim/v2: Users, Groups, ServiceProviderConfig, Schemas, ResourceTypes.
- NEW routes for the SCIM credential: create, rotate, revoke.
- A provisioning port with a conformance suite, and a service that writes persons with source scim, groups and memberships.
- A SCIM removal disables the person, adds 1 to the session generation and disables the access tokens. The revocation setting revokes them.
- Audit entries for each SCIM write, disable and revocation.
- A docs page and an e2e step with the real Pulumi CLI and OpenTofu.

Out of scope:

- OIDC, LDAP, SAML, CAS, OAuth 2.0, trusted header, passkeys and MFA.
- Directory sync schedule and full sync.
- Access-review export and console views.
- Placement rules and collections.

Depends on:

- groups-roles-grants
- collections-placement
- one-membership-function-identity-provider
- keep-two-working-break-glass
- add-one-generic-settings-api
- `groups-roles-grants` (order of the milestone review)
- `collections-placement` (order of the milestone review)
- `one-membership-function-identity-provider` (order of the milestone review)
- `keep-two-working-break-glass` (order of the milestone review)
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A mass disable is costly: the docs show a list step before DELETE and the re-enable path.
- SCIM errors use the SCIM shape and the docs show it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A race test covers a grant after a membership removal. (`pkg/access/race_test.go:169`)
- A race test covers a grant after a disable. (`pkg/access/race_test.go:382`)
- Group grant checks read the access limits. (`pkg/access/group_limits_test.go:91`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 2 (changed), 4 (changed), 9 (changed).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "SCIM implements a provisioning port."
- Quote: "SCIM feeds groups. SCIM 2.0 creates, changes and removes users and groups."
- Quote: "A removal disables, and revocation is opt-in."
- Effect: SCIM is an adapter behind a provisioning port. It disables on removal and revokes only when the setting is on.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 19 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A termination applies at once."
- Effect: A SCIM DELETE disables the person with no delay.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 14).

- Decisions that apply: 10 (current), 13 (current), 14 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The principal source of each method (PM)."
- Quote: "One function applies the group values (PM)."
- Effect: The person has the source scim and each membership goes through the one membership function.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "SCIM comes later (owner, 2026-11-07). This ADR adds no SCIM."
- Effect: The roadmap puts SCIM in milestone 4. No SCIM route ships until the owner clears the timing.
- Conflict: owner_blocks.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server refuses only a change that leaves no enabled break-glass principal."
- Effect: A SCIM removal of the last enabled break-glass principal gives 409.
- Conflict: none.

**ADR 297: Console sessions are sealed cookies, not stored sessions** (owner-tagged (all decisions)).

- Decisions that apply: 12 (current).
- Changed by: ADR 301 extends decision 1; ADR 333 extends decision 4. Chain to the end: ADR 301, ADR 333.
- Current rule: this ADR, with ADR 301, 333.
- Quote: "Each principal has a session generation, a count that starts at 1."
- Effect: A removal adds 1 to the session generation in the same transaction.
- Conflict: none.

## Open questions

1. Does the owner allow SCIM in milestone 4 despite ADR 332 decision 9? Decides: owner.
   - Options: Allow SCIM in milestone 4 with the same mapping; Defer SCIM and keep this change unbuilt.
   - Recommended: Allow SCIM in milestone 4 with the same mapping. Reason: The roadmap lists SCIM in milestone 4 and the decision says only that SCIM comes later. Only the owner can clear an owner-tagged decision.
2. What is the scope of the removal revocation setting? Decides: PM.
   - Options: One installation setting for SCIM and LDAP; One setting for each SCIM provider.
   - Recommended: One installation setting for SCIM and LDAP. Reason: ADR 55 decision 9 names one setting. One value is simple and shows in the API.
3. What is the limit of SCIM removals? Decides: PM.
   - Options: 100 DELETEs per 5 minutes for each credential; 1000 DELETEs per 5 minutes for each credential.
   - Recommended: 100 DELETEs per 5 minutes for each credential. Reason: A mass disable needs a bound that shows as a setting.

Not spec-ready: the change conflicts with an owner-tagged decision of ADR 332.

Questions considered and answered:

- Does SCIM need a new group model? No. ADR 55 decision 4 says SCIM groups are the groups of ADR 52. ADR 332 decision 9 says SCIM uses the same mapping.
- What happens to sessions and tokens on removal? ADR 55 decision 9 and ADR 70 decision 19: disable at once and keep. ADR 297 decision 12 gives the session generation.
- Can SCIM remove the last break-glass principal? No. ADR 306 decision 3 refuses it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
