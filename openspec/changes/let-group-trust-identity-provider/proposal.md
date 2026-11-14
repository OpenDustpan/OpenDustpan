---
version: 3
---

Issue: #615, #617, #619, #626.

## Why

**Persona: site administrator.** Site administrator manages sign-in providers, groups and grants. They need a safe way to let a chosen group follow the identity provider, and to opt the whole installation into full sync, with a fresh step and an audit entry for each change.

The user path this change must make work:

1. **Docs page.** Read the docs page for identity provider trust and find the group.
2. **API.** Set or clear group trust with PUT or DELETE on the new route /api/server/groups/{id}/trust, with a fresh step. Read the refusal when the step is stale, no factor is enrolled, or the reach is not covered.
3. **API.** Turn full sync on or off through the settings resource.
4. **E2e.** Run the path against the real server with a site administrator access token, and check the audit entry of each change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Trust or full sync change without a fresh step | The server answers 403 with error code mfa.fresh-step-required and names the way to get a fresh step. Do the fresh step, then repeat the call. | `curl -s $SERVER/api/dustpan/v1/me/freshness` |
| Trust change by a caller whose reach does not cover the group | The server answers 403 and names the missing reach. Ask a caller whose reach covers the group to set trust. | `curl -s -H "Authorization: Bearer $TOKEN" $SERVER/api/server/caller/access` |

Evidence for the claims above:

- The group code lives in pkg/principal and internal/api/server. Proof: The pack paths pkg/principal/principal.go, pkg/principal/principaltest and internal/api/server exist.
- A Freshness type exists in the access token package. Proof: The pack main list gives type Freshness at pkg/accesstoken/service.go:50.
- Membership columns for suspension exist in the stored data. Proof: The pack rg of suspended_at gives pkg/backup/portable_tables.go:554 and pkg/store/sqlite/internal/db/queries.sql.go:100. This change adds only the trust rule to the membership function.
- Group routes exist under /api/server, and no trust route exists. Proof: The pack main list gives ANY /api/server/groups and ANY /api/server/groups/{}/members, and no trust route.
- A settings registry exists in the CLI package. The pack shows no REST settings route for full sync, so the dependency add-one-generic-settings-api provides it. Proof: The pack path internal/cli/settings.go exists, and its rg output lists soft-delete-retention at internal/cli/settings.go:345.

## What Changes

- New route PUT and DELETE /api/server/groups/{id}/trust sets and clears the group attribute trusted from the identity provider, with setter and time, default off. It needs a fresh step and the reach cover check of the dependency. Changes on one group run in one write transaction, the last commit wins, and each is audited.
- Trust overrides the protected-group refusal for that group, as ADR 70 decision 16 says. A group that a person closed with the switch of ADR 70 decision 15 stays closed. A cleared group refuses the add again.
- The membership function skips the protected-group refusal for a trusted group, and for every group when full sync is on. A sync reads the trust value committed when it starts. A removed and recreated group has a new stable ID and starts not trusted.
- When full sync is on, each removal is final with no suspension.
- Full sync setting, registered through the generic settings API of its dependency, with the action server:manage-sign-in on the installation, a fresh step and an audit entry. Provisional decision: the API is the only layer until the owner confirms.
- Schema edits go into the baseline of each dialect in place and into the backup table list. This supersedes the numbered migrations that the issues name, because no tag has shipped. A dev installation runs `opendustpan dev reset`.

Out of scope:

- Suspension of memberships, restore, pin and the expiry job
- The admin access report and its acknowledgment, and the report data for trusted groups
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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A Freshness type exists in the access token package. (`pkg/accesstoken/service.go:50 (type Freshness)`)
- A test covers the freshness hook of access tokens. (`pkg/accesstoken/accesstoken_test.go:779 (func TestFreshnessHook)`)
- Membership column suspended_at_ms exists in the backup table list. (`pkg/backup/portable_tables.go:554 (func portable table list)`)
- The settings registry holds soft-delete-retention in the CLI package. (`internal/cli/settings.go:345 (func settings registry)`)
- A test covers group grants. (`pkg/access/access_test.go:385 (func TestGroupGrants)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P21)**, Item 19: #2924 installation-wide data role and protected groups: "(a) such a group is protected. Reason: write access to every state is the highest-impact power; cloud providers keep org-wide write roles out of automatic group sync. ADR 70 d13 (owner) read broadly; text waits."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 15 (current), 16 (extended), 17 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "A group can trust the identity provider. A person can set a group, protected or not, to "trusted from the identity provider". Then decision 14 does not apply to the group. The change needs a fresh MFA step (ADR 56) and a"
- Quote: "Full sync is an installation setting. A site administrator can set the whole installation to full sync from the identity provider. The identity provider is then trusted for every group, and each removal is final. The cha"
- Quote: "A switch closes any group. A person who manages a group can set it to "the identity provider may not add members". The switch writes an audit entry."
- Effect: Trust overrides protection for one group. Full sync does it for all groups. The closed switch stays.
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

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "IdP-started SAML is never a fresh sign-in (owner, 2026-11-07). A sign-in that starts at the identity provider never counts as a fresh authentication."
- Effect: A SAML person who signed in from the identity provider gets 403 mfa.fresh-step-required.
- Conflict: none.

## Open questions

1. Which setting layers may set full sync? Decides: owner.
   - Options: API only; API plus flag, file and database layers.
   - Recommended: API only for on and off. Reason: A fresh step cannot apply to a flag or a file value, and no ADR names the allowed layers. Provisional decision in force: API only. The owner confirms or changes it.

Not spec-ready: a code check fails: task "Add the e2e step against the real server, with the access token of a s" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part.

Questions considered and answered:

- Is a group with installation-wide write access protected? Yes. Owner decision, 13 Nov 2026 (queue P21). Trust still overrides it for one group, as ADR 70 decision 16 allows. The reach-check change owns the rule.
- How does a new installation take the schema change? The project rule says one baseline per dialect until the first tag, so no upgrade path exists.
- Where does full sync end suspended rows? ADR 70 decision 17 makes each removal final. Suspension is a separate change.
- Where does the fresh step of an SSO person come from? From the protocol of the method (ADR 320 decision 16). OAuth and trusted-header persons use a factor of OpenDustpan (ADR 301 decision 1). An agent never has one (ADR 354).
- Which setting layers may set full sync? Provisional decision for this draft: the API only, because a fresh step cannot apply to a flag or file value. It is listed as an open question for the owner to confirm.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
