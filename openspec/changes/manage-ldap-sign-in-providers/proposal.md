Issue: #2950, #2951, #2969.

## Why

**Persona: site administrator.** A site administrator manages sign-in providers for an installation. Today the first LDAP slice reads one static LDAP provider only. The administrator cannot manage providers through the API, LDAP group memberships do not change principal groups, and the static provider ID can point at another directory and reuse subjects and grants.

The user path this change must make work:

1. **Server start.** Operator sets the static LDAP provider with flags or DUSTPAN_LDAP_* variables and starts the server. A later start with a changed directory URL is refused, and the error names the rebind route.
2. **API.** Site administrator creates, reads, updates and deletes stored LDAP providers with the sign-in management action. The provider names an environment variable or a file for the bind password.
3. **CLI.** Person signs in with LDAP, gets a session, makes an access token and runs the real CLI with it. LDAP groups change principal groups under the protected-group rules.
4. **Console.** This change adds no console page. The console change of this milestone shows the providers.
5. **Docs page.** Site administrator follows the LDAP provider guide to configure a provider and to rebind the static directory.
6. **E2e.** E2E starts a real LDAP directory, manages a provider, signs in, checks group changes and audit entries, and restarts with a changed URL.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A caller without the sign-in management action calls the provider API. | 403 with the action that allows the change. | `GET /api/dustpan/v1/settings` |
| Create of a stored provider with the static provider ID. | 409 that names the reserved static ID. | `GET /api/dustpan/v1/settings` |
| Create or update with an ldap:// URL while plain connections are off, or while the setting is missing or not valid. | 400 that names the address and the setting that allows plain connections. | `GET /api/dustpan/v1/settings` |
| Create or update with a subject attribute other than entryUUID or objectGUID. | 400 that names the two allowed attributes. | `GET /api/dustpan/v1/settings` |
| Start with the static LDAP provider ID and another directory URL. | The server refuses to start. The error names the ID, the old URL, the new URL and the new rebind route. | `POST /api/dustpan/v1/sign-in/ldap-providers/{provider}/rebind` |

Evidence for the claims above:

- ModeLDAP exists and Validate refuses it until LDAP lands. Proof: pkg/session/provideroauth.go:28: // ModeLDAP is an LDAP directory. Validate refuses it until the LDAP
- Provider config validation treats LDAP as a distinct mode. Proof: pkg/session/providerconfig.go:173: case ModeSAML, ModeCAS, ModeLDAP, ModeHeader:
- Sign-in calls the shared group applier. Proof: internal/signinsvc/service.go:614: res, err := s.cfg.Groups.ApplyGroupClaims(ctx, e, principal.GroupClaims{

## What Changes

- New routes under /api/dustpan/v1/sign-in/ldap-providers to create, read, update and delete stored LDAP providers, with actions, validation and audit entries
- New route POST /api/dustpan/v1/sign-in/ldap-providers/{provider}/rebind with its own action and a typed reason
- Bind password of a stored provider by reference to an environment variable or a file, never returned and never logged
- LDAP group claims applied at sign-in through the one membership function with source ldap, protected-group refusal, audit entries and removal counting
- Static LDAP provider ID bound to its first directory URL at the first start
- A change of the plain-connection setting re-checks stored providers at the next sign-in

Out of scope:

- SAML, CAS, OAuth 2.0 and trusted-header providers
- SCIM and directory sync workers
- MFA, session policy and token policy
- A bind password set through the API
- The console page for providers

Depends on:

- ldap-sign-in
- one-membership-function-identity-provider
- `one-membership-function-identity-provider` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The administrator finds the reserved static ID and the rebind route in the guide.
- The start error names the rebind route, not only the guide.
- The console shows no provider page until its own change lands.

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

- Decisions that apply: 12 (extended), 14 (extended), 18 (current), 21 (extended).
- Changed by: ADR 142 changes decision 26; ADR 142 extends decision 13; ADR 210 changes; ADR 216 changes decisions 11, 23, 24 and 28; ADR 222 extends decisions 16, 17 and 23; ADR 227 extends decisions 16, 17 and 23; ADR 245 changes decisions 26 and 27; ADR 306 changes decisions 24, 25 and 26; ADR 306 extends decision 21; ADR 320 extends decision 12; ADR 321 extends decision 19; ADR 332 extends decisions 12, 14, 16 and 22; ADR 354 changes decision 13. Chain to the end: ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 149, ADR 297, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 142, 210, 216, 222, 227, 245, 306, 320, 321, 332, 354.
- Quote: "Each membership records its source."
- Quote: "The identity provider cannot add a member to a protected group."
- Quote: "Decisions 13 to 17 and 19 to 21 apply in the same way to a SCIM change and to a group claim at sign-in."
- Effect: LDAP group claims follow the protected-group and alert rules, and removals are counted with the source kind.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 5 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "An installation can allow plain connections with one setting. The setting is off by default. (owner)"
- Effect: Stored providers refuse plain connections by default, use the same visible setting, and fail closed.
- Conflict: none.

**ADR 321: LDAP, SAML, CAS, OAuth 2.0 groups and trusted-header sign-in** (owner-tagged: decision 7, 8).

- Decisions that apply: 5 (current), 7 (current), 8 (current).
- Changed by: ADR 332 changes decisions 19, 30, 33 and 38; ADR 332 extends decisions 13, 14, 15 and 22. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "TLS (owner, ADR 292). `ldaps://` or StartTLS, with certificate checks and an optional CA bundle. A connection with no TLS follows ADR 292."
- Quote: "The service account password (owner, ADR 63). It is a sensitive setting. It comes only from an environment variable or a `*_FILE` file (ADR 63 decision 4), and no log or audit entry shows it (decision 6)."
- Effect: The stored provider names an environment variable or a file for the bind password. The subject allowlist is narrower than decision 5 and refuses more, so it does not contradict it.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The spec and design let stored LDAP providers take any subject attribute, but the owner decision of 13 Nov 2026 (P20) requires LDAP subjects to be entryUUID or objectGUID only. The change must refuse other subject sources, and the proposal's Open questions 'none' is not valid while this is open. Resolution: answered: Subject limited to entryUUID or objectGUID, per the owner decision.
- R1.2 (adr conflict): ADR 321 decision 8 (owner, ADR 63) allows the bind password only from an environment variable or a *_FILE file. The change stores the bind password as a secret reference set through the stored-provider API, which the ADR does not allow. Resolution: answered: The password is a reference to an environment variable or a file. No API-set password.
- R1.3 (adr conflict): ADR 292 decisions 1, 2 and 5 (owner) refuse LDAP without TLS except to loopback, allow plain only by an opt-in setting that is visible, and fail closed with a refusal that names the setting. The spec has no requirement or scenario for the TLS rule, the opt-in setting or the fail-closed refusal on stored providers. Resolution: answered: Plain-connection requirements and scenarios present.
- R1.4 (adr conflict): ADR 321 decision 5 says the subject is entryUUID by default, objectGUID for Active Directory, or an attribute the administrator names. The change allows any subject field, and the owner refusal of mutable sources is not applied to LDAP. Resolution: answered: Same as R1.1.
- R1.5 (missing scenario): No scenario covers the rotation of the bind secret, though the docs path requires it. The spec and tasks do not say how a secret reference is replaced or how the old reference is revoked. Resolution: answered: No stored secret exists. Rotation is a change of the variable or the file, so no API rotation is built.
- R1.6 (missing scenario): No scenario covers a sign-in where the directory reports the user disabled, the bind fails, or the directory is unreachable. ADR 321 decisions 9 and 10 (limits and removal at each sign-in) are not reflected in the spec. Resolution: answered: Refuse unsafe sign-in requirement added.
- R1.7 (open question): The static binding requirement does not say what happens on first start with no prior binding record, or when the binding store is lost. The rebind command named in the start error is not specified in the spec or tasks. Resolution: answered: First start, lost record and the new rebind route are specified.
- R1.8 (failure mode): Two stored providers can point at different directories with the same subjects, which design.md names as a risk. No requirement makes the subject unique per provider or per directory, so grants can be shared across directories. Resolution: answered: The provider ID keys subjects and the URL is unique.
- R1.9 (missing scenario): No requirement covers the referral refusal, the empty-password refusal or the user-name boundary of ADR 321 decisions 3 and 4, although the layers table names a referral hostile test. Resolution: answered: Referral, empty password and user-name scenarios added.
- R1.10 (failure mode): Concurrent create or update of stored providers with the same ID or URL is not covered; no requirement defines uniqueness or the write conflict answer. Resolution: answered: Duplicate ID and URL answer 409.
- R1.11 (wrong claim): The proposal says Open questions: none, yet the rebind command, the secret rotation method, the subject source rule and the TLS opt-in are unanswered. The spec-ready gate requires an explicit list with owners. Resolution: answered: No open question remains, because the owner decision and the ADRs answer each one.
- R2.1 (adr conflict): ADR 321 decision 8 (owner, ADR 63) says the service account password comes only from an environment variable or a *_FILE file, and the change stores the bind password as a sealed secret set through the API. The change contradicts this owner-tagged decision and cannot be built until the owner decides. Resolution: open question: The change marks ADR 321 as owner_blocks and uses an environment variable or a file until the owner decides.
- R2.2 (open question): The proposal lists 'Open questions' with an owner question on the sealed bind password, yet the design, tasks and spec already build the write-only password field and rotation route on it. The spec-ready gate must stay closed until the owner answers. Resolution: open question: The design and tasks no longer build the API-set password. The question stays open.
- R2.3 (wrong claim): The ADR check says ADR 321 decision 5 allows an administrator-named subject attribute, and design.md decision 1 limits it to entryUUID or objectGUID. The owner decision P20 is marked 'Provisional' in the input, while the proposal quotes it as binding ('approved as written'). The change treats a provisional decision as binding without saying so. Resolution: answered: The change now says the decision is provisional and changes no ADR words.
- R2.4 (missing scenario): The spec has no requirement or scenario that refuses a stored provider create or update when the plain-connection setting is off and the URL is ldap:// to a loopback address, or the fail-closed behavior when the setting is missing or invalid (ADR 292 decision 5). The TLS scenario covers only non-loopback hosts. Resolution: answered: Loopback and missing-setting scenarios added.
- R2.5 (failure mode): The static binding has no defined answer when the binding record is lost after a first start. The spec says 'no record exists, the server writes the record', which lets a changed directory start again, the risk named in design.md. The lost-record case is treated as a first start with only an audit entry, which the spec does not state. Resolution: answered: A lost record after a first start refuses the start.
- R2.6 (missing scenario): The spec does not say how a stored provider's subject is bound per provider ID when two providers name the same directory with the same subject. The 'Duplicate URL' scenario covers only the create, not the update that changes a URL to an existing one. Resolution: answered: Duplicate URL on update scenario added.
- R2.7 (failure mode): Rotation says the old secret is discarded, but no scenario covers a failed rotation or a crash between the write of the new secret and the audit entry. Concurrent rotate and sign-in is not covered. Resolution: answered: Rotation is removed from scope until the owner decides, so no rotation race exists.
- R2.8 (missing scenario): The group-claim requirement does not cover removal of memberships at sign-in, the removal count for the mass-removal alert (issue 2951), or the source kind of the provider, although ADR 70 decision 21 applies. Resolution: answered: Removal count requirement and scenario added.
- R2.9 (scope): Issue 2969 asks for the refusal error to name the command that changes the URL. The spec's start scenario does not name the rebind route, and the proposal's error table gives a different command for the restart error than the one the start error must show. Resolution: answered: The start error scenario names the rebind route.
- R2.10 (missing scenario): The spec omits the user-name boundary (1 to 256 bytes, no control characters) of ADR 321 decision 3, and the limits of decision 9 (timeouts and the 1000-entry search cap), although the adapter layer names them. Resolution: answered: User-name bound and answer limit requirements added.
- R3.1 (adr conflict): The spec and design let a stored provider take a bind password through the API (write-only secret), but ADR 321 decision 8 (owner, ADR 63) says the service account password comes only from an environment variable or a *_FILE file. The change cannot be built until the owner decides; the proposal states Open questions 'none' is not valid and the owner question blocks it. Resolution: answered: The API-set password is removed. The password is a reference to an environment variable or a file, so decision 8 holds and the conflict is gone.
- R3.2 (owner decision conflict): The proposal quotes the P20 owner decision as binding, but the input marks it 'Provisional'. The subject allowlist (entryUUID or objectGUID only) rests on that provisional decision, while ADR 321 decision 5 still allows an administrator-named attribute. The change must state the provisional status and the conflict with ADR 321 decision 5 before the gate. Resolution: answered: The change states that the decision is provisional and that the allowlist is narrower than ADR 321 decision 5, so it does not contradict it.
- R3.3 (missing scenario): The spec has no scenario for the rebind route's authorization (who may call it, which action), no scenario for a rebind with an existing active sign-in session, and no scenario for the audit entry content. The error table names the rebind route but no requirement defines its action. Resolution: answered: Authorize rebind and Rebind on purpose scenarios cover the action, sessions and audit content.
- R3.4 (failure mode): The 'Lost record' scenario refuses start but does not say how the record is restored; the only path is the rebind route, which changes the URL. A rebind after a lost record can therefore accept any directory without a check, which defeats the binding that design.md names as a risk. Resolution: answered: A rebind needs the action, a reason and a check of the new URL, and it writes an audit entry.
- R3.5 (failure mode): Concurrent create or update of two stored providers that race on the same URL is answered 409 in the design, but the spec gives no test or check that the database enforces uniqueness rather than a read-then-write check. Resolution: answered: A unique database index and a race scenario are added.
- R3.6 (missing scenario): No scenario covers a group claim that names a group that does not exist locally, or a membership change for a user disabled between the search and the bind. Resolution: answered: Unknown group and disabled-between-search-and-bind scenarios added.
- R3.7 (open question): The plain-connection setting is read from the setting API, but the spec does not say whether a change of the setting re-checks existing stored providers that use ldap:// URLs. Resolution: answered: Re-check on setting change requirement added.
- R3.8 (scope): The tasks list the guide page and the e2e step, but the proposal's usability and definition-of-done items (console surface, CLI sign-in step) are not in tasks.md. Resolution: answered: Console is named out of scope, and the CLI sign-in step is in the docs task.
- M.7 (dependency): LDAP group claims with the protected-group refusal use the one membership function, and stored providers extend the existing ldap-sign-in change. Resolution: answered: depends_on now names ldap-sign-in and one-membership-function-identity-provider. Group claims apply only through that membership function with source ldap.

## Open questions

none

Questions considered and answered:

- Which subject sources may a stored LDAP provider use? Only entryUUID and objectGUID. Owner decision, 13 Nov 2026 (queue P20). It is provisional, so no ADR words change here.
- May the API set the bind password? No. ADR 321 decision 8 (owner) allows only an environment variable or a file.
- Do LDAP group claims change memberships? Yes, only through the one membership function, with source ldap, protected-group refusal and audit entries (ADR 70 decisions 14 and 18). Removals are counted (ADR 70 decision 21).
- Can a rebind accept any directory? No. It needs server:manage-sign-in (ADR 70 decision 11), a typed reason and a check of the new URL against egress and TLS rules. It writes an audit entry and revokes the sessions of the provider.
- Do stored providers need a plain-connection rule? Yes. ADR 292 decisions 1, 2 and 5 apply. A change to off refuses sign-in at once.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
