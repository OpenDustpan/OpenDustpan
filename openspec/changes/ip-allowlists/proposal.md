Roadmap: `docs/roadmap.md:183` (IP allowlists).

## Why

**Persona: site administrator.** A site administrator shares one installation between people and CI pipelines and needs to limit console sign-in and CLI access by network address, with each installation, namespace and token opting in on its own.

The user path this change must make work:

1. **API.** Turn the feature on and set the installation list in the settings API and docs page.
2. **API.** Opt in a namespace and an access token with their own lists.
3. **Console.** Sign in to the console from an allowed and a denied address and read the 403 reason.
4. **CLI.** Run pulumi up and tofu apply with a token from an allowed and a denied address.
5. **E2e.** Run the e2e allowlist suite over TLS against the real binary.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Sign-in or CLI request from an address outside an opted-in list. | 403 with error code ip-allowlist.denied that names the scope that refused; check the lists with the settings read. | `pulumi up` |
| Save of a list with an entry that is not a valid CIDR or address. | 400 with error code ip-allowlist.invalid-cidr that names the bad entry. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- IP allowlist e2e coverage exists. Proof: e2e/ipallow_test.go
- Principal e2e coverage touches allowlists. Proof: e2e/principals_test.go

## What Changes

- Installation feature flag and list settings with CIDR checks
- Namespace and access token opt-in lists
- Enforcement on console sign-in and access token requests with 403 and audit entries
- Docs page for enable, opt-in, deny and allow

Out of scope:

- New sign-in methods
- Token lifetime and expiry email
- mTLS client certificate handling

Depends on:

- add-one-generic-settings-api
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- An administrator must find which of installation, namespace or token refused a request without reading logs.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current), 11 (changed).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "IP allowlists are off by default, and each scope opts in."
- Quote: "An administrator of the installation turns the feature on first."
- Quote: "Then each scope opts in on its own: the installation, each namespace and each access token."
- Quote: "A scope that did not opt in has no list."
- Quote: "When the feature is on, each list applies to console sign-in and to requests with an access token."
- Quote: "Each change writes an audit entry."
- Effect: The change follows decision 8 for off-by-default opt-in lists on console sign-in and access token requests, and decision 11 for an audit entry on each configuration change.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "Operator actions keep the area `installation:`."
- Effect: Installation flag and list settings declare server-area actions and namespace list settings declare namespace-area actions; exact action names wait for the owner decision in open questions.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (not owner-tagged).

- Decisions that apply: 11 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "One answer, and the reason in the audit log (PM). The browser receives one answer for each refused sign-in of a method."
- Effect: Decision 11 covers one answer for a refused sign-in of a method. An IP allowlist denial is a distinct network check with its own 403 that names the refusing scope.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (not owner-tagged).

- Decisions that apply: 35 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "Each header provider has its own list of proxies (PM)."
- Effect: Decision 35 keeps per-provider trusted proxies separate from the installation trusted-proxy handling; the allowlist check uses only the installation recorded client address.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (scope): The proposal's Open questions section reads not spec-ready: route GET /api/dustpan/v1/me is not in the API description or route table. The change cannot be built until the route is named as new or an existing route is used; proposal.md must be corrected. Resolution: answered: No new route is proposed; the change uses the existing installation and namespace settings resource and token fields, with authorization actions left to the open question for the owner.
- R1.2 (adr conflict): ADR 55 decision 8 says 'off by default' and 'an administrator of the installation turns the feature on first'. Spec requirement 'Feature flag and opt-in' only states the flag gates checks; it does not specify default off or who may turn it on, nor its audit entry (ADR 55 decision 11 requires an audit entry per change). Resolution: answered: Flag default requirement states off by default, enable-privilege requirement states only a site administrator turns it on, and config-audit requirement writes an entry on each change.
- R1.3 (missing scenario): No scenario covers the settings API for lists: refusal of invalid CIDR (400 ip-allowlist.invalid-cidr) is in the proposal table and task 1.2 but no spec requirement or scenario states it. Resolution: answered: Invalid list entry requirement answers 400 with error code ip-allowlist.invalid-cidr and stores no change.
- R1.4 (missing scenario): The spec does not say how a request is evaluated when several scopes hold lists and the client address is in one list but outside another (installation, namespace, token). Design says denial; the spec's 'Allowed request' scenario only covers the all-match case, and no denial-by-any-list scenario exists. Resolution: answered: Deny-when-any-list-denies requirement with address-inside-one-list scenario states the 403 and the refusing scope.
- R1.5 (failure mode): Client address behind a proxy: design lists this risk, but no requirement says which address is used (recorded address after proxy handling via --trusted-proxies) or what happens when proxy headers are untrusted or spoofed. ADR 321 decision 35 scopes trusted proxies per provider; the change does not reference it. Resolution: answered: Recorded-client-address requirement checks the recorded address from installation trusted-proxy handling and ignores untrusted headers, separate from header.trustedProxies of ADR 321 decision 35.
- R1.6 (open question): The audit entry for a denied request is required but its content (who or what was refused, the scope that refused, the address) is not specified. ADR 55 decision 11 and the usability note on finding which scope refused both depend on it. Resolution: answered: Denial-audit requirement writes the refusing scope, the client address, and the refused identity when known.
- R1.7 (missing scenario): Behavior of a live session or an issued access token when the list changes or the feature is turned off mid-session is not covered (ADR 55 decision 8 says each list applies to requests; a request after a list change is not specified). Resolution: answered: List-change-timing and replica-lists requirements check each later request against the new list and leave in-flight requests on the prior verdict.
- R1.8 (failure mode): Concurrency and restart: design says the checker is built once per change of settings, but no requirement says what happens to in-flight requests during a settings change, or what the server does when stored lists fail to parse at startup (fail closed or open). Resolution: answered: List-change-timing covers in-flight requests and startup-parse-failure fails closed for sign-in and token requests until host database repair.
- R1.9 (missing scenario): Operator lockout: an administrator who sets an installation list that excludes their own address can lock out all console sign-in. No requirement or refusal covers this, and no break-glass path (ADR 57, ADR 70 decision 23) is linked to the allowlist. Resolution: answered: Self-lockout-warning requirement returns a warning when a write excludes the writer address; no bypass is added and host database repair stays outside the HTTP check.
- R1.10 (open question): The spec does not state whether the allowlist applies to requests with a CLI access token on non-token routes, to health or metrics routes, or to the REST API for settings itself. Scope of the check is not defined beyond 'console sign-in and access token requests'. Resolution: answered: Check-scope requirement limits the check to console sign-in start and any request that carries an access token, with a scenario for tool-neutral, Pulumi, TFE-compatible, and settings routes.
- R1.11 (wrong claim): Design layer table names the HTTP handler check 'before authentication result use' and the service checker 'after proxy handling', but the spec order (allowlist, then authentication/authorization) and ADR 320 decision 7 order of sign-in steps are not reconciled; the ordering of IP check relative to credential check is unstated. Resolution: answered: Check-order requirement checks the installation list before credential validation and per-scope order requirement checks namespace and token lists after authentication.
- R2.1 (missing scenario): The spec has no requirement for a 403 body that names the scope that refused, but proposal.md's error table and usability note require it. The 'Deny when any list denies' requirement only states error code ip-allowlist.denied. Resolution: answered: Deny requirement now states the 403 names the refusing scope, with a scenario that checks the scope name in the body.
- R2.2 (failure mode): The 'Denial audit' requirement requires the refused identity, but the installation list is checked before credential validation ('Check order'). For a denial at that stage the identity is unknown, so the audit scenario cannot be met as written. Resolution: answered: Denial audit requirement now writes the identity only when known, so an installation-list denial before credential validation audits the scope and address without an identity.
- R2.3 (failure mode): No requirement covers a multi-replica or restarted deployment. The checker is built once per settings change, but a change applied on one replica is not stated to reach the others. A replica can keep allowing or denying with stale lists. Resolution: answered: Added replica requirement: stored lists apply to each later request on each instance.
- R2.4 (failure mode): The fail-closed requirement for a stored list that fails to parse at startup refuses all console sign-in and token requests. The change gives no recovery path other than the operator repairing the list, and does not say whether break-glass accounts (ADR 70 decision 23) or the bootstrap token (ADR 57) bypass the check. Resolution: answered: Startup fail-closed requirement now keeps the host database repair path outside the HTTP check; break-glass sign-in stays checked with no bypass.
- R2.5 (adr conflict): ADR 320 decision 11 ('One answer, and the reason in the audit log') says the browser gets one answer for a refused sign-in. The change returns a 403 that names the refusing scope to the browser. The conflict with that decision is not checked in the ADR check section. Resolution: answered: Added ADR 320 decision 11 to adr_check with quote and no-conflict effect for the distinct network denial.
- R2.6 (open question): The proposal does not say whether --trusted-proxies or the header.trustedProxies list of ADR 321 decision 35 decides the recorded client address for the allowlist. The spec only says 'after trusted-proxy handling'. Resolution: answered: Recorded-address requirement now names installation trusted-proxy handling and ignores untrusted headers, separate from header.trustedProxies.
- R2.7 (open question): The change does not name the authorization action that writes the feature flag and the lists, or the action that reads them. ADR 216 decision 3 (owner-tagged) puts installation actions under the installation area, and the proposal requires each endpoint to declare its actions. Resolution: open question: Added as open question on authorization actions for the owner, with options and escape hatch.
- R2.8 (wrong claim): Evidence lists 'A user guide for IP allowlists exists' (docs/guides/ip-allowlists.md), but task 2.3 says to write the docs page. The proof is a path only, with no rg or command output, so the claim is unchecked. Resolution: answered: Removed the unchecked guide-exists claim; evidence now cites only pack e2e files and the docs page is a task output.
- R2.9 (scope): Task 2.3 asks the docs page to warn about lockout of the own address, but no requirement or scenario covers a lockout warning or a self-lockout refusal. The task has no spec backing. Resolution: answered: Added self-lockout warning requirement with scenario to back the docs warning task.
- M2.6 (consistency): The flag and the lists are installation settings, but the change has no dependency on the settings API. It also uses the action installation:manage-sign-in, while the other changes use server:manage-sign-in. Resolution: answered: Added add-one-generic-settings-api to depends_on for the flag and list keys; changed installation action prefix to server:manage-sign-in to match the other changes.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which authorization actions guard the feature flag, the installation list, the namespace lists, the token lists, and the reads of them?
   - Options: server:manage-sign-in for the flag and all lists; server:manage-settings family for the flag and namespace:manage-settings for namespace lists.
   - Provisional decision (owner review 14 Nov): server:manage-sign-in for the flag and the installation list, namespace:manage-settings for a namespace list, token create scope for a token list, with read actions on the same objects.
   - Reason: ADR 216 reserves the installation area for operator actions; AWS practice puts network controls under a network administration action with read and write split. Benchmark: AWS
   - Persona impact: No persona loses access; the actions stay inside site and namespace administrator roles. Escape hatch: Ship with server:manage-sign-in for flag and installation list and namespace:manage-settings for namespace lists, then rename if the owner names other actions.
   - Owner-tagged ADRs involved: ADR 216 (their words do not change).

Questions considered and answered:

- Which scopes hold a list and how is the feature turned on? ADR 55 decision 8 answers it: off by default, installation turns on first, then installation, namespace and access token each opt in.
- Which requests do the lists check? ADR 55 decision 8 answers it: console sign-in and requests with an access token. A scope that did not opt in has no list.
- What audit entries do allowlist changes and denials write? ADR 55 decision 11 answers it: each configuration change writes an audit entry; each denial writes an entry with scope, address, and identity when known.
- Which client address is checked behind a proxy? The installation trusted-proxy handling answers it; ADR 321 decision 35 covers only the identity header, so the allowlist uses the recorded address and ignores untrusted headers.
- Does naming the refusing scope conflict with one answer for refused sign-ins? ADR 320 decision 11 covers refused sign-ins of a method. The allowlist denial is a separate network check before credential use with error code ip-allowlist.denied.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
