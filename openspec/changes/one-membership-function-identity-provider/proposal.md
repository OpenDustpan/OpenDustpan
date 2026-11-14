---
version: 3
---

Issue: #614, #621, #632.

## Why

**Persona: site administrator.** A site administrator shares an installation with many people and pipelines and must keep identity provider changes from granting sensitive access. The server must apply one rule to SCIM and to sign-in claims, refuse adds to protected and closed groups, and alert on each configuration change.

The user path this change must make work:

1. **API.** Site administrator configures a sign-in provider and claim mapping through the API.
2. **E2e.** Person signs in and the server applies group claims through the one function.
3. **Docs page.** Administrator reads the docs page for the refusal behavior and the alert channel.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| SCIM add of a user to a protected or closed group is refused. | SCIM answers a SCIM error with code idp.protected-group that names the group and the refusing rule. | `curl /api/dustpan/v1/capabilities` |
| Group claim at sign-in names a protected or closed group. | Sign-in succeeds with no new membership. The audit entry names the refused group and the refusing rule. | `curl /api/dustpan/v1/capabilities` |
| Change to identity provider, claim mapping, or account matching without server:manage-sign-in or without a fresh step inside the one window is refused. | 403 with code authz.forbidden or mfa.fresh-step-required. The no-factor message names the enrollment page and each way to enroll. | `curl /api/dustpan/v1/capabilities` |

Evidence for the claims above:

- ADR 70 is accepted and holds the membership source, refusal, one-rule, and sensitive-action decisions. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- The membership function belongs in pkg/principal, which exists. Proof: pkg/principal
- The issue asks for one domain function that takes the source scim or claim and the provider or SCIM token ID. Proof: #614, #621, #632

## What Changes

- One domain membership function for sources scim, claim, and ldap with sentinel refusal for protected groups without trust or full sync and for closed groups, with closed winning over trust and full sync.
- Source and provider or token ID recorded on each added membership with audit entries in one transaction.
- server:manage-sign-in plus a fresh check inside the one window on each identity provider, claim mapping, and account matching route with audit plus WARN alerts and a 168-hour SCIM token expiry alert specified for the scim change to apply.
- Contract tests for refused adds and token expiry with audit checks; full SCIM and claim e2e after those adapters land.

Out of scope:

- SCIM adapter and sign-in claim adapters themselves, and wiring of the SCIM token route, which the scim change applies from the guard specified here
- Group trust switch and full sync settings themselves
- Suspension, mass-removal alerts, and identifying-attribute changes, which ship in their own changes
- Other-admin notice and event delivery, which ship with events

Depends on:

- guide-implies-protected-groups-refuse
- fresh-authentication-window-0-turns
- check-reach-when-grant-membership
- `guide-implies-protected-groups-refuse` (order of the milestone review)
- `fresh-authentication-window-0-turns` (order of the milestone review)
- `check-reach-when-grant-membership` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused SCIM add names the group and the rule so the administrator fixes the mapping or the group trust.
- A skipped claim at sign-in still signs the person in, so one bad mapping does not lock the person out.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the sign-in failure error code used by sign-in refusals. (`internal/errcode/catalog_signin.go:8`)
- Main has a grant race test that covers membership removal races. (`pkg/access/race_test.go:169`)
- Main has a blocklist input-length test for sign-in names. (`pkg/localuser/blocklist/blocklist_test.go:115`)
- Main has a check make target that gates this change. (`Makefile:14`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 11 (changed), 12 (extended), 14 (extended), 15 (current), 16 (extended), 17 (extended), 18 (current), 22 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "The identity provider cannot add a member to a protected group."
- Quote: "Each membership records its source."
- Quote: "One rule for SCIM and for sign-in claims."
- Quote: "`server:manage-sign-in` gates the sign-in settings."
- Quote: "A switch closes any group."
- Quote: "A group can trust the identity provider."
- Effect: The one function applies the same refusal rule to SCIM and to claims, records the source, and gates configuration changes with the sensitive action plus audit and WARN alert.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner)."
- Effect: Each configuration route that this change wires needs a fresh authentication inside the one window, in addition to server:manage-sign-in.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 14).

- Decisions that apply: 12 (changed), 13 (current), 14 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "One function applies the group values (PM)."
- Quote: "The rules of ADR 70 apply as they are (owner, ADR 70)."
- Effect: The one function is the single place where the mapping meets the refusal rules for every sign-in method.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 8 (current), 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The rules are a sensitive setting (owner, 2026-11-07)."
- Quote: "SCIM comes later (owner, 2026-11-07)."
- Effect: A change to a claim rule is a sensitive setting with an audit entry and a fresh check.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner)."
- Effect: A caller with no enrolled factor gets the enrollment refusal.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A site admin may disable a break-glass principal (owner, 2026-10-20, accepted as written for now)."
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now)."
- Effect: The sweep rule stays as it is; this change does not alter it.
- Conflict: none.

## Open questions

1. Does the PM confirm that a SCIM PATCH is atomic and a Bulk request is per operation? Decides: PM.
   - Options: PATCH atomic, Bulk per operation; Per-row commit for both.
   - Recommended: PATCH atomic, Bulk per operation Reason: RFC 7644 defines PATCH as atomic, and a partial commit would surprise the client.
2. Which durable alert channel replaces the interim audit entry plus WARN line after events exist? Decides: PM.
   - Options: Keep audit plus WARN, switch channel later without changing callers; Add a pending-alert table with retry now.
   - Recommended: Keep audit plus WARN, switch channel later without changing callers Reason: The audit entry is the durable record and the WARN line is a view of it, so no alert is lost.

Not spec-ready: a code check fails: main_facts[3]: "where" is not file:line (func Name).

Questions considered and answered:

- Where does the refusal logic live? One domain function in pkg/principal, per ADR 70 decision 18 and ADR 320 decision 13.
- How does a SCIM batch with mixed rows answer? Provisional: a SCIM PATCH is atomic, and a Bulk request gives per-operation status. The PM confirms in open question 1.
- What wins when a group is closed and trusted? Closed wins, per ADR 70 decisions 15 to 17 read together.
- Does an identity provider add overwrite a local source? No. The local source stays and one audit entry names both sources.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
