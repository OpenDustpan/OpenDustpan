---
version: 1
---

Issue: #2915, #2998.

## Problem

As a site administrator I set up sign-in providers so that a renamed or reused name never signs in as another principal. Today CAS with no attribute, a free LDAP attribute and a free OAuth claim can hand the old principal and its grants to a new person.

## Evidence

- The trusted-header strip helper has no non-test caller. Proof: rg for `Method.Strip` and `headerauth.PeerOf` outside tests finds 0 matches (pack code_facts).
- ModeHeader exists and Validate refuses it. Proof: pkg/session/provideroauth.go:31 and :33.
- LDAP already has entryUUID and objectGUID subject kinds. Proof: internal/ldapauth/config.go:59 and :62.

## Fix boundary

- A provider save refuses a mutable subject source with the NEW error code `sign-in.subject-mutable`. The check runs on the merged settings of a partial PATCH.
- CAS needs a named attribute. The CAS user fallback is removed. The server refuses the attribute names `uid`, `user`, `mail`, `email` and `username`.
- Plain header mode is removed. Only `jwt` mode stays, with its `sub` claim and the character allowlist of ADR 321 decision 39.
- LDAP accepts only entryUUID or objectGUID. The custom attribute option is removed.
- OAuth accepts only `sub`.
- The same check runs when the provider loads, so a restore or a direct database write cannot bring back a mutable provider.
- The sign-in guides are updated.

Out of scope:

- Wiring of the trusted-header method (issue #2998). It is the next change and covers jwt mode only. It is not part of this change.
- Any numbered migration or re-link code for principals that exist.
- SAML: its persistent NameID rule already refuses mutable values.

## Check

- Write failing tests of the save scenarios (allowed and mutable sources, partial PATCH) in pkg/session. Check: `go test ./pkg/session/...` fails on main
- Write failing tests of the missing-subject and fail-closed scenarios in internal/casauth, internal/ldapauth, internal/oauth and the sign-in service. Check: `go test ./internal/casauth/... ./internal/ldapauth/... ./internal/oauth/...` fails on main
- Add the subject allowlist for CAS, LDAP and OAuth and the NEW error code `sign-in.subject-mutable` in internal/errcode/catalog_signin.go. Check: `go test ./internal/casauth/... ./internal/ldapauth/... ./internal/oauth/...`
- Refuse plain header mode and run the check at provider load in pkg/session. Check: `go test ./pkg/session/...`
- Update the sign-in guides, add an e2e step through `PATCH /api/server/sign-in-providers/{provider}` with a site administrator access token, and run the usability check. Confirmed by: CI (`make e2e` and `make check` with zero findings). Check: `make e2e` and `make check` with zero findings

Depends on:

- `sign-in-follow-ups-empty` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The trusted-header strip helper exists and has no non-test caller. (`internal/headerauth/headerauth.go:252 (func Method.Strip)`)
- The header mode constant exists and Validate refuses it. (`pkg/session/provideroauth.go:33`)
- LDAP has entryUUID and objectGUID subject kinds. (`internal/ldapauth/config.go:59`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 22 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13; ADR 369 changes decision 9; ADR 372 changes. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 370, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354, 369, 372.
- Quote: "These rules close the side doors. - A change to an identifying attribute of a member of a protected group is a protected action. Examples are the user name, the email, the `externalId` and the federated subject. Account"
- Effect: The change keeps this rule: a federated subject is an identifying attribute, so only an immutable source is accepted.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 5 (current), 29 (current), 36 (current), 39 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The subject (PM) is an attribute that the administrator names. With no attribute, the subject is the CAS `user`, and the provider page says that a rename in the CAS server makes a new principal."
- Quote: "The subject (PM). The `sub` claim in `jwt` mode. The user header in `plain` mode, with the allowlist of characters of a subject at the boundary."
- Effect: Decisions 29 and 39 change in part: no CAS user fallback, no plain mode. Decision 5 is kept for entryUUID and objectGUID and loses the custom attribute. Owner decision, 13 Nov 2026 (queue P20). The ADR text waits for the owner.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does an owner decision settle the options? Yes. Owner decision, 13 Nov 2026 (queue P20): refuse mutable subject sources. ADR 70 decision 22 is kept.
- Do existing principals need a migration? No. No tag shipped, so the baseline rule forbids compatibility code. After the administrator saves an immutable source, people get new principals. The old principals stay with their grants. The guide tells the administrator to list and delete them. Owner decision, 13 Nov 2026 (queue P20): no users exist now.
- Which OAuth claim do providers without sub use? Only `sub`. A provider without it is refused. Owner decision, 13 Nov 2026 (queue P20) allows loosening later.
- Can a named CAS attribute be mutable? The server cannot prove immutability. It refuses a fixed deny list of known mutable names. The guide tells the administrator to name a stable ID attribute.
- How is objectGUID encoded and what if entryUUID is missing? The existing adapter turns the binary value into lowercase text. A missing or empty value refuses the sign-in.
- What does SAML need? Nothing. ADR 321 decision 18 already refuses transient and email NameID.
- Why is the trusted-header wiring not in this change? One change does one thing. This change sets the subject rule. The wiring (settings, Strip with the TCP peer, audit) is the next change, which depends on this one.
- How often is the fail-closed audit entry written? One entry for each provider and each hour at most, with the provider ID and the reason.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
