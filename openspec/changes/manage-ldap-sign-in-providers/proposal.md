---
version: 3
---

Issue: #2950, #2951, #2969.

## Why

**Persona: site administrator.** A site administrator manages sign-in providers for an installation. Today the first LDAP slice reads one static LDAP provider only. The administrator cannot manage providers through the API. LDAP group memberships do not change principal groups. The static provider ID can point at another directory and reuse subjects and grants.

The user path this change must make work:

1. **Server start.** Operator sets the static LDAP provider with flags or DUSTPAN_LDAP_* variables and starts the server. The server records the ID and the URL in the database in one transaction. A later start with another URL is refused, and the error names the rebind route.
2. **API.** Site administrator creates, reads, updates and deletes stored LDAP providers with the sign-in management action. The URL cannot change in an update. The provider names an environment variable or a file for the bind password.
3. **CLI.** Person signs in with LDAP, gets a session, makes an access token and runs the real CLI with it. LDAP groups change principal groups under the protected-group rules.
4. **Console.** This change adds no console page. The console change of this milestone shows the providers.
5. **Docs page.** Site administrator follows the LDAP provider guide to configure a provider, to rebind a directory and to delete a provider.
6. **E2e.** The step starts a real LDAP directory, manages a provider with the access token of a site administrator, signs in through POST /api/dustpan/v1/sign-in/providers/{provider}/password, runs the Pulumi CLI and OpenTofu with a personal token, and restarts with a changed URL. The directory fixture is a task of this change.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller without the sign-in management action calls the provider API. | 403 that names the action that allows the change. | `GET /api/dustpan/v1/settings` |
| Create of a stored provider with the static provider ID. | 409 that names the reserved static ID. | `GET /api/dustpan/v1/sign-in/ldap-providers` |
| Create with an ldap:// URL to a non-loopback address while plain connections are off, missing or not valid. | 400 that names the address and the installation setting that allows plain connections. | `GET /api/dustpan/v1/settings` |
| Update that changes the directory URL. | 409 that says the URL changes only through the rebind route. | `POST /api/dustpan/v1/sign-in/ldap-providers/{provider}/rebind` |
| Start with the static LDAP provider ID and another directory URL. | The server refuses to start. The error names the ID, the old URL, the new URL and the rebind route. | `POST /api/dustpan/v1/sign-in/ldap-providers/{provider}/rebind` |

Evidence for the claims above:

- ModeLDAP exists and Validate refuses it until LDAP lands. Proof: pkg/session/provideroauth.go:28: // ModeLDAP is an LDAP directory. Validate refuses it until the LDAP
- Provider config validation treats LDAP as a distinct mode. Proof: pkg/session/providerconfig.go:173: case ModeSAML, ModeCAS, ModeLDAP, ModeHeader:
- Sign-in calls the shared group applier. Proof: internal/signinsvc/service.go:614: res, err := s.cfg.Groups.ApplyGroupClaims(ctx, e, principal.GroupClaims{
- A route for password sign-in through a named provider exists. Proof: route POST /api/dustpan/v1/sign-in/providers/{}/password in the main facts of the pack

## What Changes

- Routes under /api/dustpan/v1/sign-in/ldap-providers to create, read, update and delete stored LDAP providers, with actions, validation and audit entries
- The update route refuses a URL change. Only the rebind route changes the URL
- Route POST /api/dustpan/v1/sign-in/ldap-providers/{provider}/rebind with its own action, a typed reason, a check of the new URL and revocation of the sessions of the provider
- Delete revokes the sessions of the provider, removes its grants and keeps the ID reserved, so a recreate with that ID is refused
- Provider record holds the TLS mode (ldaps or StartTLS) and an optional CA bundle reference
- Bind password by reference to an operator allowlist of environment variables or files, never returned and never logged
- LDAP group claims applied at sign-in through the one membership function with source ldap, protected-group refusal, audit entries and removal counting
- Static LDAP provider ID bound to its first directory URL in a database row written in one transaction at the first start

Out of scope:

- SAML, CAS, OAuth 2.0 and trusted-header providers
- SCIM and directory sync workers
- MFA, session policy and token policy
- A bind password set through the API
- The console page for providers
- The installation setting for plain connections (owned by its own change)

Depends on:

- ldap-sign-in
- one-membership-function-identity-provider
- `one-membership-function-identity-provider` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The administrator finds the reserved static ID, the rebind route and the delete rule in the guide.
- The start error names the rebind route, not only the guide.
- The console shows no provider page until its own change lands.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ModeLDAP is a provider mode that Validate refuses until LDAP lands. (`pkg/session/provideroauth.go:28`)
- ProviderConfig validation lists ModeLDAP with the modes that have no OAuth settings. (`pkg/session/providerconfig.go:173`)
- The sign-in service calls ApplyGroupClaims. (`internal/signinsvc/service.go:614`)
- The group applier implements ApplyGroupClaims. (`pkg/store/internal/groupclaims/groupclaims.go:192`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 17: Every feature has a REST API, authorization and policy** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 30 changes; ADR 210 extends; ADR 222 changes; ADR 224 changes; ADR 266 extends; ADR 305 extends; ADR 326 extends. Chain to the end: ADR 30, ADR 210, ADR 222, ADR 224, ADR 266, ADR 305, ADR 326, ADR 35, ADR 38, ADR 39, ADR 41, ADR 42, ADR 151, ADR 218, ADR 248, ADR 281, ADR 247, ADR 286, ADR 335, ADR 351, ADR 354, ADR 241, ADR 313, ADR 317, ADR 342, ADR 353, ADR 272, ADR 48, ADR 70, ADR 212, ADR 237, ADR 238, ADR 245, ADR 60, ADR 103, ADR 146, ADR 240, ADR 352, ADR 253, ADR 154, ADR 217, ADR 220.
- Current rule: this ADR, with ADR 30, 210, 222, 224, 266, 305, 326.
- Quote: "Each feature that OpenDustpan ships has a"
- Effect: Stored LDAP providers get a documented REST API with declared actions and audit entries.
- Conflict: none.

**ADR 70: Act-as reach, identity provider groups and break-glass accounts** (not owner-tagged).

- Decisions that apply: 11 (changed), 12 (extended), 14 (extended), 18 (current), 21 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Each membership records its source."
- Quote: "The identity provider cannot add a member to a protected group."
- Quote: "Decisions 13 to 17 and 19 to 21 apply in the same way to a SCIM change and to a group claim at sign-in."
- Effect: LDAP group claims follow the protected-group and alert rules. Removals are counted with the source kind.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 5 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "An installation can allow plain connections with one setting. The setting is off by default. (owner)"
- Effect: Stored and static providers refuse plain connections by default and read the same installation setting. A missing or not valid value counts as off. Certificate checks stay.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (owner-tagged: decision 7, 8).

- Decisions that apply: 5 (current), 7 (current), 8 (current), 9 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "TLS (owner, ADR 292). `ldaps://` or StartTLS, with certificate checks and an optional CA bundle. A connection with no TLS follows ADR 292."
- Quote: "The service account password (owner, ADR 63). It is a sensitive setting. It comes only from an environment variable or a `*_FILE` file (ADR 63 decision 4), and no log or audit entry shows it (decision 6)."
- Effect: The provider names an allowlisted variable or file for the password, with TLS and CA bundle fields. The subject allowlist is narrower than decision 5 and does not contradict it. The limits of decision 9 apply.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which subject sources may a stored LDAP provider use? Only entryUUID and objectGUID. Owner decision, 13 Nov 2026 (queue P20). It is provisional, so no ADR words change here.
- May the API set the bind password? No. ADR 321 decision 8 (owner) allows only an environment variable or a file.
- Can an update move a provider to another directory? No. The stable provider ID keys subjects and grants, so a URL change goes only through rebind.
- What does delete do? It revokes sessions and removes the provider grants. The ID stays reserved.
- Do LDAP group claims change memberships? Yes, only through the one membership function with source ldap (ADR 70 decisions 14 and 18). Removals are counted (ADR 70 decision 21).
- Are plain connections allowed? Only by the installation setting of ADR 292 decisions 2 to 5. The check runs at write time and at each sign-in.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
