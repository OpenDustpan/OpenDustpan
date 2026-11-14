Roadmap: `docs/roadmap.md:141` (Sign-in methods).

## Why

**Persona: developer.** A developer wants to sign in with the identity provider of their team, open a session, make an access token, and run `pulumi up` and `tofu apply` with that token.

The user path this change must make work:

1. **Docs page.** Read the sign-in docs page for the method and its provider settings.
2. **Console.** Sign in in the browser with OIDC, LDAP, SAML, CAS, plain OAuth, trusted header, or a passwordless passkey.
3. **API.** Create a personal access token from the session and export it for the CLIs.
4. **CLI.** Run `pulumi up` and `tofu apply` with the token.
5. **E2e.** Run the matrix of the seven methods over TLS against the real binary with test identity providers and check the audit entries.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Sign-in with an unknown or disabled provider, or for a disabled person, fails. | 401 with code `signin.failed` and one generic message. | `curl -s $D/api/dustpan/v1/sign-in/providers` |
| A trusted header from an address that is not on the proxy list gives no sign-in. | 401 with code `signin.untrusted-proxy`. | `curl -s $D/api/dustpan/v1/sign-in/providers` |

Evidence for the claims above:

- LDAP sign-in has an e2e test and a guide. Proof: e2e/ldap_signin_test.go and docs/guides/ldap-sign-in.md
- CAS sign-in has an e2e test and a guide. Proof: e2e/e2e_test.go and docs/guides/cas-sign-in.md
- No e2e test names SAML and no guide exists. Proof: SAML keyword evidence: e2e_count 0, e2e [], guides []
- Trusted-header and console sign-in guides exist. Proof: docs/guides/trusted-header-sign-in.md and docs/guides/console-sign-in.md
- Passwordless passkey sign-in of a local user already ships with e2e tests and a guide, so this change adds no code for it and only runs it in the matrix. Proof: e2e/passkeysignin_test.go, e2e/passkeys_test.go, e2e/localusers_test.go, docs/guides/passkeys.md and the route POST /api/dustpan/v1/sign-in/passkey/finish in the coverage list
- OIDC has an e2e test only for workload identity. Proof: OIDC keyword evidence: e2e/workload_test.go only

## What Changes

- Ship SP-started SAML with pinned signing certificates (pull request 1).
- Ship plain OAuth 2.0 with a user-info endpoint, identity from `sub` only, and the CAS named-attribute subject with source `cas` (pull request 2).
- Ship trusted-header sign-in in signed-token mode only, off by default, with a proxy list for each provider (pull request 3).
- Add the e2e matrix of the seven methods, each ending with both CLIs and the audit check; the passkey leg reuses the shipped passkey sign-in (pull request 4).
- Refuse a group claim from these methods until the group rule ships.
- Fail closed when the one-time ID store is unavailable.
- Audit each change of a provider setting, including the proxy list.

Out of scope:

- MFA enrollment, freshness windows and the factor refusal for OAuth and trusted-header persons (app-level-second-factor-totp). Until that change lands, a guarded step by such a person fails closed with the no-factor error.
- SCIM provisioning and LDAP sync schedules
- Group mapping rules
- IP allowlists and token life cycle
- Break-glass mode 2
- OIDC issuer for workloads
- The generic settings API; the trusted-header switch and the IdP-started switch are keys that add-one-generic-settings-api registers

Depends on:

- add-one-generic-settings-api
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Each sign-in error gives one generic browser answer; the cause is only in the audit log, which costs the developer time.
- A trusted-header failure does not name the seen peer address in the answer; an administrator with the audit read action finds it there.
- A plain OAuth mapping error shows only as a failed sign-in; the docs page needs a working user-info example.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P20)**, Item 18: #2915 subject sources that can change: "(i) refuse mutable subject sources (CAS needs an attribute, no plain header mode, LDAP entryUUID/objectGUID only, OAuth `sub` only). Reason: Entra, Okta and Kubernetes guidance; no users, so it costs nothing now and can be loosened later. ADR 321 (owner-accepted) text waits for the owner; ADR 70 d22 (owner) is kept."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 1 (changed), 3 (current), 6 (extended).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "Trusted-header sign-in is off by default. The operator turns it on and names the proxies that may set the header. The server ignores the header from any other source."
- Quote: "OpenDustpan still issues the tokens. A sign-in method only proves who a person is. The server then issues its own session or access token (ADR 6). Tool CLIs keep using access tokens."
- Effect: The change ships the methods of decision 1. The server issues the session and token. Trusted header stays off by default.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (not owner-tagged).

- Decisions that apply: 10 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "The principal source of each method (PM). ADR 70 decision 12 says that each record keeps its source. A person that the first sign-in makes has the source of its method: `oidc`, `oauth`, `saml`, `cas`, `ldap` or `header`."
- Effect: Each method sets its principal source.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 35 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Each header provider has its own list of proxies (PM). The provider setting `header.trustedProxies` is a list of addresses and CIDR ranges."
- Effect: Each header provider has its own stored list.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "IdP-started SAML is never a fresh sign-in (owner, 2026-11-07). A sign-in that starts at the identity provider never counts as a fresh authentication."
- Effect: SP-started SAML is the normal path. IdP-started never counts as fresh. Group rules are not shipped here.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sign-in stays the same (owner). The sign-in with these two methods asks for no second factor."
- Effect: Sign-in asks for no second factor. The factor refusal belongs to app-level-second-factor-totp.
- Conflict: none.

**ADR 285: Local users sign in with a password and with a passkey** (owner-tagged (all decisions)).

- Decisions that apply: 1 (extended).
- Changed by: ADR 297 changes decisions 5, 17, 19, 20 and 21; ADR 301 extends decision 1; ADR 312 changes decision 29; ADR 320 extends decision 21; ADR 333 changes decision 16; ADR 333 extends decision 20; ADR 333 extends decision 23. Chain to the end: ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 297, 301, 312, 320, 333.
- Quote: "A local user has a password, passkeys, or both (owner)."
- Effect: A local user with no password signs in with a passkey alone; this already ships.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The oauth-plain-sign-in spec takes identity from any mapped user-info claim and does not require OAuth `sub`, which the owner decision of 13 Nov (P20, item 18) refuses for mutable sources; the change does not address it. Source: specs/oauth-plain-sign-in/spec.md. Resolution: answered: OAuth takes `sub` only.
- R1.2 (owner decision conflict): The trusted-header spec accepts the header as identity from an allowlisted proxy, which is the plain header mode the owner decision of 13 Nov (P20, item 18) refuses. The change does not record the refusal or its scope. Resolution: answered: No plain header mode; scenario No plain header.
- R1.3 (owner decision conflict): CAS subject as a named attribute and the OAuth mapped claim are not checked against the owner refusal of mutable subject sources (CAS needs an attribute, LDAP entryUUID/objectGUID only, OAuth `sub` only); the design and the ADR check do not state how these pass. Resolution: answered: CAS attribute, OAuth sub, LDAP entryUUID or objectGUID stated in design.
- R1.4 (missing scenario): No requirement or scenario covers IdP-started SAML: one-time assertion ID store, maximum age, refusal when the setting is off, and the never-fresh rule of ADR 332 decisions 4 and 5, though the design makes them part of the structure. Resolution: answered: IdP-started scenarios are in the saml-sign-in spec.
- R1.5 (missing scenario): No scenario covers the 403 mfa.fresh-step-required refusal for an OAuth or trusted-header person with no enrolled factor (ADR 301 decision 4), though the proposal lists it as a user-path error. Resolution: answered: The factor refusal belongs to app-level-second-factor-totp; the user-path errors no longer list it and the ADR check says so.
- R1.6 (open question): ADR 332 decision 12 (group rule, owner-waited) and ADR 321 decisions 19, 30, 33 and 38 route group claims through a mapping rule that is still pending the owner; the change maps groups anyway, while the ADR check reads 'Open questions: none'. Resolution: answered: Group claims are refused in this change; the rule waits for the owner and is out of scope, so no open question here.
- R1.7 (missing scenario): No scenario covers the audit entry and fresh authentication for a change to a group rule or explicit mapping (ADR 332 decision 8), although the group claim mapping is in scope. Resolution: answered: Group rule changes are out of scope; no group rule ships here.
- R1.8 (failure mode): No scenario covers a replayed, expired or already-consumed SAML response or request ID, though the design's hostile test requires replay refusal. Resolution: answered: No replay scenario exists.
- R1.9 (failure mode): No scenario covers a failing, timing-out or non-200 user-info call, nor a claim of the wrong type, for plain OAuth. Resolution: answered: User-info failure scenario exists.
- R1.10 (wrong claim): The ADR check cites ADR 55 decision 10 and ADR 46 decision 2 as answers to the MFA question, but the proposal's answer text ('MCP stays with the SSO provider') is not in any quoted decision; the 'Changed by' chain for ADR 332 lists no change though the chain names ADR 332 decision 12 change. Resolution: answered: The ADR 301 quote now supports the MFA answer; the wrong text is removed.
- R1.11 (scope): Out of scope lists MFA enrollment and freshness windows, yet tasks 2.2 and the error table require the MFA freshness refusal for OAuth and trusted-header persons (ADR 301 work). Resolution: answered: Out of scope names enrollment, windows and the factor refusal together.
- R1.12 (missing scenario): No scenario states the disabled-provider or disabled-person refusal with one generic answer, nor the signin.untrusted-proxy error code named in the proposal; tasks name no test for the trusted-header default-off scenario. Resolution: answered: Disabled-person scenario, default-off test and the signin.untrusted-proxy scenario exist.
- R2.1 (missing scenario): No scenario covers a trusted-header sign-in whose provider proxy list is empty or whose header lacks signed-token mode; the design's structural guard refuses these, but the spec does not state the refusal, so the build cannot be checked against it. Resolution: answered: Valid provider setting requirement with two scenarios.
- R2.2 (missing scenario): The e2e error table names code signin.failed for disabled provider and disabled person, but no spec scenario states the disabled-person refusal, the one generic answer, or the audit entry for it. Resolution: answered: Disabled person requirement added.
- R2.3 (owner decision conflict): The CAS subject is said to come from a named attribute, but the change never states the rule that refuses the plain CAS user name as subject, and the CAS spec file is missing, so the P20 refusal of a mutable CAS subject is not covered by a scenario. Resolution: answered: cas-sign-in-subject spec added.
- R2.4 (failure mode): The SAML replay requirement covers a consumed assertion ID but no scenario covers a request ID store restart or concurrent use of one assertion ID, so the one-time store may accept a second use after a restart. Resolution: answered: Database one-time IDs; restart and concurrent scenarios added.
- R2.5 (failure mode): The OAuth requirement takes identity from the sub claim, but no scenario covers a user-info answer where sub changes or is absent for a known person; the design's refusal of a mutable subject is not tested for a missing sub. Resolution: answered: Missing sub scenario added.
- R2.6 (adr conflict): ADR 321 decision 35 requires each header provider to have its own proxy list, but the design only names a proxy list in general terms; the spec does not say the list is per provider, so a shared list would breach the quoted decision. Resolution: answered: Requirement and scenario say the list is per provider.
- R2.7 (open question): The ADR check says group claims are out of scope, yet the change ships SAML, OAuth and header sign-in whose ADR 321 decisions 19, 30, 33 and 38 route group claims through ApplyGroupClaims; the refusal of a group claim until the rule ships is not written as a requirement. Resolution: answered: No group claim requirement added.
- R2.8 (wrong claim): The ADR check quotes ADR 55 decision 10 as the reason that no MFA step is added on top of SAML, CAS or OAuth, but the quoted text is the same as ADR 46 decision 2; the claim that ADR 46 and ADR 55 both leave MFA to the provider is correct only for sign-in, not for the fresh step. Resolution: answered: Considered text separates sign-in from the fresh step.
- R2.9 (scope): Task 2.2 asks for CLI runs for each method but the tasks name no test for the audit entries of the SAML assertion refusal or the header proxy refusal beyond the e2e step, so the audit requirement is not checked per method. Resolution: answered: TestAuditEntryPerRefusal task added.
- R2.10 (failure mode): Trusted-header rejection of a spoofed peer address behind a load balancer with a changed client address is not covered; a restart that reloads the proxy list is not covered either. Resolution: answered: Design reads the list at each request and uses the trusted-proxies rule.
- R2.11 (open question): The usability section says a trusted-header failure does not name the seen peer address, while the proxy-gate scenario writes the source address to the audit log; the change does not decide who may read that log entry. Resolution: answered: The audit log read action decides; considered entry added.
- R3.1 (missing scenario): The spec set has no scenario for a SAML response with an expired assertion or a stale request ID in the browser round trip, and no scenario for a request ID consumed by a restart; the replay requirement lists these but no test name maps to the expired case in tasks.md. Resolution: answered: Expired assertion, stale request ID scenarios and tests added.
- R3.2 (owner decision conflict): The proposal's ADR check and the oauth-plain-sign-in spec treat the owner decision P20 as binding, but the owner decision text is marked 'Provisional' and says the ADR 321 text waits for the owner; the change ships SAML and header sign-in while ADR 321 decision 5 and 35 wording is still pending, so the build cannot be checked against a settled rule. Resolution: answered: Owner decision P20 is provisional; considered and design now say the rules are strict defaults that can be loosened later, and build does not depend on pending ADR 321 wording.
- R3.3 (adr conflict): ADR 321 decision 35 says each header provider has its own list of proxies, but the design decision 4 says the proxy list is read from the stored setting and reuses the trusted-proxies rule of --trusted-proxies; the design does not state the separation the quoted decision requires. Resolution: answered: Design states the list is separate from the trusted-proxies flag.
- R3.4 (adr conflict): ADR 320 decision 10 requires a first-sign-in principal source of oidc, oauth, saml, cas, ldap or header; the CAS spec names no source and the proposal ADR check does not check it, so the CAS person may get no defined source. Resolution: answered: CAS scenario and test name principal source `cas`; ADR 320 check names it.
- R3.5 (failure mode): The one-time ID store is a database table, but no scenario covers a database outage during the check: the spec does not say whether the sign-in fails closed or the ID is burned, so two servers may accept the same assertion when the store is unavailable. Resolution: answered: Store outage requirement fails closed, with scenario and test.
- R3.6 (wrong claim): The ADR check cites ADR 301 as owner-tagged and its quoted decision 4 as current, but the change ships the OAuth and header refusal of token creation under the 403 code while the ADR 301 quote is truncated; the quoted text is not a full decision line. Resolution: answered: ADR 301 quote is now a full sentence of decision 4.
- R3.7 (open question): The usability section says the trusted-header answer does not name the peer address, but the proxy-gate scenario writes the source address to the audit log without naming which role may read it; the open question is marked none. Resolution: answered: Considered entry names the audit read action as the reader.
- R3.8 (missing scenario): No scenario covers a provider setting change at run time that is rejected while sign-ins are in flight, nor the audit entry of an accepted change of the proxy list, though ADR 55 decision 11 requires one. Resolution: answered: Proxy list change audit requirement and test added; the change applies at the next request.
- R3.9 (scope): Tasks 1.6 names a test for the refusal of a group claim, but the spec for it is the SAML no-group-claim requirement only; OAuth and header group claims (ADR 321 decisions 33 and 38) have no scenario, so the scope of the group refusal is partly unchecked. Resolution: answered: No group claim requirements added for OAuth and header with tests.
- M.1 (gap): Step 1 needs seven methods, each with a sign-in, a session, an access token, `pulumi up` and `tofu apply`. The requirements cover only SAML, CAS, OAuth 2.0 and trusted header. OIDC, LDAP and the passkey of a local user with no password have no requirement or e2e step. Resolution: answered: Added the sign-in-method-matrix capability with OIDC, LDAP, passkey and the four new methods, each ending with both CLIs and the audit check. The factor refusal moved out to app-level-second-factor-totp.
- M.2 (scope): Four new provider types, the one-time ID store and the e2e matrix are too large for one pull request series. Resolution: answered: Split into four pull requests: SAML; OAuth with the CAS subject rule; trusted header; the matrix. The two switches are keys of add-one-generic-settings-api, named in depends_on.
- M2.2 (dependency): This change depends on app-level-second-factor-totp, and app-level-second-factor-totp depends on this change. This makes a cycle. Resolution: answered: app-level-second-factor-totp is removed from depends_on; out_of_scope says a guarded step fails closed with the no-factor error until that change lands.
- M2.3 (gap): The matrix tests the passkey sign-in of a local user with no password, but no change of this milestone ships it. Resolution: answered: Evidence now cites e2e/passkeysignin_test.go, e2e/passkeys_test.go, e2e/localusers_test.go, docs/guides/passkeys.md and the shipped passkey sign-in routes; the matrix only reuses them.

## Open questions

none

Questions considered and answered:

- Does each method need its own token type for the CLIs? No. ADR 55 decision 3: the method proves identity, then the server issues its own session and the person creates an access token.
- Is a plain header or a mutable subject safe? No. Owner decision, 13 Nov 2026 (queue P20): refuse mutable subject sources. OAuth uses `sub`, CAS needs a named attribute, LDAP uses entryUUID or objectGUID, no plain header mode. The decision is provisional, so these stay strict defaults.
- Does the server add an MFA step on top of SAML, CAS or OAuth at sign-in? No. ADR 46 decision 2, ADR 55 decision 10 and ADR 301 decision 3. The later fresh step belongs to app-level-second-factor-totp.
- Does SAML need IdP-started sign-in? Optional only. ADR 332 decisions 4 and 5: required checks and never fresh.
- Do group claims map in this change? No. The group mapping rule waits for the owner (ADR 320 decision 12, changed by ADR 332). These methods apply no group claim.
- What happens when the one-time ID store is down? The sign-in fails closed with 401 and an audit entry.
- Does passwordless passkey sign-in need new work? No. The pack shows e2e/passkeysignin_test.go and the passkey sign-in routes; the matrix only runs it with both CLIs.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
