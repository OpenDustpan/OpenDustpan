---
version: 3
---

Roadmap: `docs/roadmap.md:141` (Sign-in methods).

## Why

**Persona: developer.** A developer wants to sign in with the identity provider of their team, open a session, make an access token, and run `pulumi up` and `tofu apply` with that token.

The user path this change must make work:

1. **Docs page.** Read the sign-in docs page for the method and its provider settings.
2. **Console.** Sign in in the browser with OIDC, LDAP, SAML, CAS, plain OAuth, trusted header, or a passwordless passkey.
3. **API.** Create a personal access token from the session with `POST /api/server/caller/tokens` and export it for the CLIs.
4. **CLI.** Run `pulumi up` and `tofu apply` with the token.
5. **E2e.** Run the matrix of the seven methods over TLS against the real binary, with MFA off, with test identity providers, and check the audit entries.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Sign-in with an unknown or disabled provider, or for a disabled person, fails. | 401 with code `sign-in.failed` (exists on main) and one generic message. | `curl -s $D/api/server/sign-in-providers` |
| A trusted header from an address that is not on the proxy list gives no sign-in. | 401 with code `sign-in.untrusted-proxy` (NEW in this change). | `curl -s $D/api/server/sign-in-providers` |

Evidence for the claims above:

- LDAP sign-in has an e2e test and a guide. Proof: e2e/ldap_signin_test.go and docs/guides/ldap-sign-in.md
- CAS sign-in has an e2e test and a guide. Proof: e2e/e2e_test.go and docs/guides/cas-sign-in.md
- No e2e test names SAML and no guide exists. Proof: SAML keyword evidence: e2e_count 0, e2e [], guides []
- Trusted-header and console sign-in guides exist. Proof: docs/guides/trusted-header-sign-in.md and docs/guides/console-sign-in.md
- Passwordless passkey sign-in already ships with e2e tests and a guide. Proof: e2e/passkeysignin_test.go, e2e/passkeys_test.go, docs/guides/passkeys.md
- OIDC has an e2e test only for workload identity. Proof: OIDC keyword evidence: e2e/workload_test.go only

## What Changes

- Ship SP-started SAML with pinned signing certificates (pull request 1).
- Ship plain OAuth 2.0 with a user-info endpoint, identity from `sub` only, and the CAS named-attribute subject with source `cas` (pull request 2).
- Ship trusted-header sign-in in signed-token mode only, off by default, with a proxy list for each provider (pull request 3).
- Add the e2e matrix of the seven methods, with MFA off (pull request 4).
- Refuse a group claim from these methods until the group rule ships.
- Fail closed when the one-time ID store is unavailable.

Out of scope:

- MFA enrollment, freshness windows and the factor refusal for OAuth and trusted-header persons (app-level-second-factor-totp). The matrix runs with MFA off.
- SCIM provisioning and LDAP sync schedules
- Group mapping rules
- IP allowlists and token life cycle
- Break-glass mode 2
- The generic settings API; the switches are keys that add-one-generic-settings-api registers

Depends on:

- add-one-generic-settings-api
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Each sign-in error gives one generic browser answer; the cause is in the audit log.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Error code sign-in.failed exists. (`internal/errcode/catalog_signin.go:8 (func catalog)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 3 (current), 6 (extended).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "Trusted-header sign-in is off by default. The operator turns it on and names the proxies that may set the header. The server ignores the header from any other source."
- Effect: Trusted header stays off by default; the server issues the session and token.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 35 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Each header provider has its own list of proxies (PM)."
- Effect: Each header provider has its own stored list.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "IdP-started SAML is never a fresh sign-in (owner, 2026-11-07)."
- Effect: IdP-started accepts an assertion only with all four checks and never counts as fresh.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph.

Questions considered and answered:

- Does each method need its own token type? No. ADR 55 decision 3.
- Is a plain header or mutable subject safe? No. Owner decision, 13 Nov 2026 (queue P20): refuse mutable subject sources.
- Does the matrix run with MFA on? No. With MFA off no fresh step applies (ADR 301 decision 12); the MFA path belongs to another change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
