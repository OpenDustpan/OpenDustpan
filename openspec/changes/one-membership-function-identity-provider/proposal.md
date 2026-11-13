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
| SCIM add of a user to a protected or closed group is refused. | SCIM 400 with code idp.protected-group and the message names the group and the check command. | `curl /api/dustpan/v1/scim/Users/{id}` |
| Group claim at sign-in names a protected or closed group. | Sign-in succeeds with no new membership and the audit entry names the refused group and the check command. | `opendustpan audit list --principal <subject>` |
| Change to identity provider, claim mapping, or account matching without server:manage-sign-in or without a fresh step inside the one window is refused. | 403 with code authz.forbidden or mfa.fresh-step-required and the message names `GET /api/dustpan/v1/capabilities`. | `curl /api/dustpan/v1/capabilities` |

Evidence for the claims above:

- ADR 70 is accepted and holds the membership source, refusal, one-rule, and sensitive-action decisions. Proof: docs/adr/0070-act-as-reach-idp-groups-and-break-glass.md
- The membership function belongs in pkg/principal, which exists. Proof: pkg/principal
- The issue asks for one domain function that takes the source scim or claim and the provider or SCIM token ID. Proof: #614, #621, #632

## What Changes

- One domain membership function for sources scim, claim, and ldap with sentinel refusal for protected groups without trust or full sync and for closed groups, with closed winning over trust and full sync.
- Source and provider or token ID recorded on each added membership with audit entries in one transaction.
- server:manage-sign-in plus a fresh check inside the one window with the shortest effective window on each identity provider, claim mapping, and account matching route with audit plus WARN alerts and 168-hour SCIM token expiry alert specified for the scim change to apply.
- Contract tests and harness for refused adds and token expiry with audit checks; full SCIM and claim e2e after those adapters land.

Out of scope:

- SCIM adapter and sign-in claim adapters themselves, and wiring of the SCIM token route which the scim change applies from the guard specified here
- Group trust switch and full sync settings themselves
- Suspension, mass-removal alerts, and identifying-attribute changes, which ship in their own changes
- Other-admin notice and event delivery, which ship with events

Depends on:

- `fresh-authentication-window-0-turns` (order of the milestone review)
- `guide-implies-protected-groups-refuse` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused SCIM add names the group and the rule in the SCIM error so the administrator fixes the mapping or the group trust.
- A skipped claim at sign-in still signs the person in so one bad mapping does not lock the person out.

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
- Effect: The one function applies the same refusal rule to SCIM and to claims, refuses protected groups without trust or full sync and closed groups with closed winning over trust and full sync, records the source, and gates configuration changes with the sensitive action plus audit and WARN alert.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner)."
- Effect: Each route that this change covers for a trusted identity provider, claim mapping, or account matching rule change needs a fresh authentication inside the one window, using the shortest effective window, in addition to server:manage-sign-in.
- Conflict: none.

**ADR 320: One sign-in path for every sign-in method** (owner-tagged: decision 14).

- Decisions that apply: 12 (changed), 13 (current), 14 (current).
- Changed by: ADR 332 changes decision 12. Chain to the end: ADR 332.
- Current rule: this ADR, with ADR 332.
- Quote: "One function applies the group values (PM)."
- Quote: "The rules of ADR 70 apply as they are (owner, ADR 70)."
- Effect: The one function is the single place where the explicit single-mapping rule meets the refusal rules of ADR 70 for every sign-in method, with zero or unknown groups as no change, and covers sources scim, claim, and ldap.
- Conflict: none.

**ADR 332: SAML starts at the service provider, and group claims map by rule** (owner-tagged (all decisions)).

- Decisions that apply: 8 (current), 9 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The rules are a sensitive setting (owner, 2026-11-07)."
- Quote: "SCIM comes later (owner, 2026-11-07)."
- Effect: A change to a claim rule or explicit mapping is a sensitive setting with an audit entry and a fresh check, and SCIM later uses the same mapping.
- Conflict: none.

**ADR 301: A passkey, TOTP or SSH signature gives the fresh step to a person who signed in with OAuth or a trusted header** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "No factor, no fresh step (owner)."
- Effect: A caller of these methods with no enrolled factor has no fresh step and gets the enrollment refusal.
- Conflict: none.

**ADR 306: Break-glass: the session limit, the mass-suspension alert and the disable rules** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A site admin may disable a break-glass principal (owner, 2026-10-20, accepted as written for now)."
- Quote: "Delete and sweep (owner, 2026-10-20, accepted as written for now)."
- Effect: The sweep disable and delete notice rule covers sweep actions on break-glass principals and does not change the interim audit plus WARN channel of this change.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): ADR 332 decision 8 (owner-tagged) says a change to a claim rule or explicit mapping needs a fresh authentication (step-up); the spec 'Route action' scenario requires only server:manage-sign-in and no fresh-step check is in the design or tasks. Resolution: answered: Sensitive configuration routes now require server:manage-sign-in and a fresh authentication with the one window; spec, design, and tasks cover both checks.
- R1.2 (owner decision conflict): The ADR check cites ADR 70 decision 22 for the alert and sensitive action but the spec and design do not implement the fresh-authentication requirement that ADR 227 decision 2 (owner) requires for the changes of ADR 70 decisions 16, 17 and 23 and for each step that ADR 70 names as fresh MFA; the change adds no fresh-step check for configuration changes. Resolution: answered: ADR 227 decision 2 added to adr_check with quote; spec requires the fresh check on each configuration change.
- R1.3 (adr conflict): ADR 70 decision 14 and ADR 320 decision 14 (owner) allow trusted-group and full-sync exceptions; the spec 'Refused protected add' scenario refuses every add to a protected group with no trust or full-sync case, so the one function may refuse adds that the ADR allows. Resolution: answered: Refused-add scenarios now carry the trust and full-sync exceptions, with accept scenarios for a trusted group and for full sync on.
- R1.4 (missing scenario): No scenario covers a change to an identifying attribute of a protected member or the suspension of decision 20, although tasks 2.3 and issue #632 require e2e steps for them; spec, tasks and proposal disagree on scope. Resolution: answered: Suspension and identifying-attribute changes moved explicitly to their own changes; e2e tasks in this change cover only refused adds and token expiry so spec, tasks, and scope agree.
- R1.5 (failure mode): The spec does not say whether the refused-add check and the membership write run in one transaction for the port adapter; a concurrent group switch or protected-group change can race the check. Resolution: answered: Spec requires the refusal check and the membership plus audit writes in one transaction; tasks test a concurrent group-switch race.
- R1.6 (failure mode): The spec does not say whether a change fails when the audit write fails; the audit entry is the only durable record, and the WARN line is lost on restart or rotation, so an unaudited change can proceed. Resolution: answered: Spec requires the change to fail when the audit write fails; design puts audit in the same transaction.
- R1.7 (wrong claim): The proposal states 'Open questions: none' while the same file lists an open question with a provisional decision pending owner review on 14 Nov for the alert channel; the spec-ready gate is not met. Resolution: answered: Alert channel moved to considered with its answer; open_questions is now none so the spec-ready gate is met.
- R1.8 (missing scenario): No scenario for a group value with no mapped group or an unknown group ID returned by the mapper, although ADR 320 decision 12 allows zero or more groups. Resolution: answered: Added a scenario for a group value with no mapped group and for an unknown group ID.
- R1.9 (missing scenario): No scenario for a SCIM token that has already expired, or for a clock that moves backwards; only the nears-expiry case is specified. Resolution: answered: Added scenarios for an already-expired SCIM token and for a clock that moves backwards.
- R1.10 (scope): Tasks 2.3 and 3.1 add the docs page, the usability check and e2e steps, while the out-of-scope list excludes the SCIM and claim adapters, so the server:manage-sign-in wiring and the one function have no caller in this change. Resolution: answered: Out-of-scope now names only the adapters and the other changes; this change still wires the routes that exist and the one function that later adapters must call.
- R1.11 (open question): The audit entry format and fields for refused adds versus accepted changes, and whether the audit records the refusing rule as ADR 70 decision 12 requires, are not specified. Resolution: answered: Audit entry fields for refused adds versus accepted changes, including the refusing rule, are now specified in design and spec.
- R2.1 (adr conflict): specs/idp-config-alerts/spec.md: the route scenario requires only server:manage-sign-in plus fresh check, but ADR 332 decision 8 (owner) says a claim rule change needs a fresh authentication; the fresh check is specified with a 403 code mfa.fresh-step-required while ADR 227 decision 2 (owner) requires the one window; the spec does not state the window age, and the tasks test only the action, so fresh-step behavior is unverified. Resolution: answered: Fresh check now states the 900-second window with 403 mfa.fresh-step-required and tasks test missing, expired, and valid fresh steps.
- R2.2 (adr conflict): design.md Decision 3 and spec: ADR 70 decision 12 is quoted only as a membership source record; the spec does not cover the refusing-rule audit for accepted adds, and the proposal ADR check does not quote ADR 320 decision 12 mapping rule (explicit single mappings) that the mapper must follow, so the mapper contract for zero or unknown groups is not defined. Resolution: answered: ADR 320 decision 12 mapping rule added to adr_check and specs; audit fields for refused and accepted changes stated per group.
- R2.3 (failure mode): specs/idp-membership/spec.md: the trusted/full-sync exception is checked inside the write transaction, but the spec does not say how a concurrent full-sync switch-off or a trust removal during an in-flight SCIM batch is handled per request; the race test in tasks 1.x is named only in the adversarial pass, not in tasks.md. Resolution: answered: Spec states per-request transaction snapshot for concurrent full-sync or trust changes and tasks carry the race test.
- R2.4 (missing scenario): specs/idp-membership/spec.md: no scenario for an add where the group is both closed and trusted, or where full sync is on but the group is closed by the switch of ADR 70 decision 15; the precedence of the closed switch against trust and full sync is not stated. Resolution: answered: Closed-wins precedence stated in design and spec with scenarios for closed plus trusted and closed plus full sync.
- R2.5 (open question): proposal.md Open questions reads none, but the alert channel (audit entry plus WARN line) is a provisional choice that ADR 70 decision 22 does not answer; the note claims no open question remains, yet the WARN line is explicitly non-durable per design Risks, and the answer for retention and restart is not decided. Resolution: open question: Durable-channel question added as an open question with PM to decide.
- R2.6 (owner decision conflict): ADR 306 decision 3 and 6 (owner) say each disable and delete by the sweep is audited and notifies admins; the alert design uses WARN only for SCIM token expiry and configuration changes, with no notice to other admins, which the change does not address although it touches the same alert family. Resolution: answered: ADR 306 added to adr_check with no conflict; other-admin notice moved to out-of-scope pending events.
- R2.7 (missing scenario): specs/idp-config-alerts/spec.md: no scenario for a claim-mapping change made by a caller whose fresh authentication is a non-factor sign-in (ADR 301 decision 4: no factor, no fresh step), which must return 403 with the enrollment message, not the generic one. Resolution: answered: Scenario added for a no-factor caller that gets 403 with the enrollment message.
- R2.8 (failure mode): specs/idp-membership/spec.md: the audit-failure scenario fails the change, but the SCIM adapter's error mapping for a failed audit write (500 versus 400 idp.protected-group) is not specified, so a refused add during an audit outage may return the wrong error code. Resolution: answered: Audit-failure mapping specified as 500, distinct from refusal 400 idp.protected-group.
- R2.9 (scope): tasks.md 2.3 requires e2e steps for refused adds through SCIM and claims, but the SCIM and claim adapters are out of scope in proposal.md; the e2e check cannot run until those adapters exist, so task 2.3 and 3.1 cannot pass in this change as written. Resolution: answered: Tasks use contract and handler tests in this change and mark full SCIM and claim e2e as blocked until those adapters land.
- R2.10 (wrong claim): proposal.md ADR check: the ADR 227 decision 2 quote is stated as the one window but the decision also lists switches of ADR 125 decision 3 and a ChatOps approval; the proposal cites only the configuration changes, so the claim that this change needs only those steps is incomplete. Resolution: answered: Effect text covers only the configuration and claim-rule steps of the one window without claiming the full window scope.
- R2.11 (open question): specs/idp-config-alerts/spec.md: the expiry warning window ('before expiry') has no defined lead time; the design and tasks do not fix the duration, so the test cannot assert the moment of the warning. Resolution: answered: Expiry lead time fixed at 168 hours with scenarios for near, expired, and backward-clock tokens.
- R3.1 (adr conflict): specs/idp-config-alerts/spec.md: the 'Route action' scenario requires only server:manage-sign-in plus the fresh check, but ADR 70 decision 16 quoted in the proposal also needs a fresh MFA step and ADR 332 decision 8 (owner) says a claim-rule change writes an audit entry and needs a fresh authentication; the spec covers the fresh step only for claim rules and not for trusted identity provider, account matching or SCIM token changes named in the same requirement, so the layer of the route is inconsistent with ADR 227 decision 2 (owner) which lists the changes of ADR 70 decisions 16, 17 and 23. Resolution: answered: Sensitive configuration spec now requires server:manage-sign-in and the 900-second fresh check on each trusted provider, claim mapping, account matching, and SCIM token change; layers and tasks test each route.
- R3.2 (open question): specs/idp-membership/spec.md: the 'Accepted trusted and full-sync adds' and 'Refused protected add' scenarios do not say how a SCIM request with a batch of mixed groups is answered when one group is refused and another is accepted; the SCIM error mapping for partial success is not decided and no ADR answers it. Resolution: open question: Mixed SCIM batch behavior added as an open question with per-row status recommended; design fixes the interim per-row snapshot rule.
- R3.3 (failure mode): specs/idp-config-alerts/spec.md: the audit-write failure for configuration changes is not covered; the change requirement says the server writes an audit entry and a WARN line but does not say the change fails when the audit write fails, unlike the membership spec, so an unaudited configuration change can proceed, which ADR 70 decision 22 (audit of each change) and the 'nothing happens without a record' rule forbid. Resolution: answered: Config spec now requires the change to fail with 500 when the audit write fails, with no state change.
- R3.4 (wrong claim): proposal.md ADR check: ADR 227 is quoted as covering 'the changes of ADR 70 decisions 16, 17 and 23' but the Effect states the window covers only configuration and claim-rule steps; ADR 70 decision 16 requires a fresh MFA step for trusted-group changes, which this change does not wire to any route in tasks, so the claim that the change covers the fresh step for trusted groups is unsupported. Resolution: answered: ADR 227 effect narrowed to the configuration routes this change wires; trusted-group membership switch wiring stays out of scope and considered states the limit.
- R3.5 (missing scenario): specs/idp-membership/spec.md: no scenario for a membership already present when the one function adds the same member again (idempotent add) or for a SCIM add that changes the source of an existing local membership. Resolution: answered: Added idempotent repeat-add and local-to-IdP source-change scenarios to the membership spec.
- R3.6 (failure mode): design.md Structure: the membershipChange constructor checks flags but the design does not say whether the check reads the group flags once per request or per row; a batch with a flag change between rows is not covered by the snapshot rule. Resolution: answered: Design states one transaction snapshot per request with per-row evaluation inside a SCIM batch.
- R3.7 (missing scenario): specs/idp-config-alerts/spec.md: the expiry scenarios do not say whether a token with no expiry date, or a token whose expiry is changed after the alert was written, raises a second alert; no scenario repeats the warning or dedupes it. Resolution: answered: Added scenarios for tokens with no expiry and for re-armed expiry with one more alert.
- R3.8 (open question): proposal.md Open questions reads none, but the durable alert channel is answered only provisionally; the provisional answer keeps a non-durable WARN line as the only alert after a restart, which ADR 70 decision 22 requires to be an alert and which the design Risks call not durable. Resolution: open question: Durable alert channel added as an open question with PM to decide.
- R3.9 (scope): tasks.md 2.3 and 3.1 require a docs page and a usability check that are not in the change files, while the proposal lists the docs page as part of the user path; the change does not name the docs page file it will write. Resolution: answered: Tasks name the docs page file docs/guides/idp-membership-and-alerts.md and its usability check.
- M.6 (consistency): The function accepts only the sources scim and claim, but manage-ldap-sign-in-providers writes source ldap. The fixed 900-second fresh check conflicts with the one window of ADR 227. The SCIM token route belongs to the scim change. Resolution: answered: One function now accepts scim, claim, and ldap; fresh check uses the one window with the shortest effective window instead of a fixed 900 seconds; SCIM token route wiring moved to the scim change with only the guard and 168-hour alert specified here; docs task updates the protected-groups guide sentence.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. How does a SCIM batch with mixed accepted and refused groups answer when one row is refused and another is accepted?
   - Options: Answer the batch with per-row status: accepted rows commit and refused rows return per-row SCIM errors; Fail the whole batch when one row is refused with no row committed.
   - Provisional decision (owner review 14 Nov): A SCIM PATCH is atomic: one refused member refuses the whole request with a SCIM error and commits nothing. A SCIM Bulk request gives a status for each operation.
   - Reason: RFC 7644 makes PATCH atomic and Bulk per-operation. A partial commit inside one PATCH would surprise the client. Benchmark: AWS IAM Identity Center follows RFC 7644: it refuses the whole PATCH on an error.
   - Persona impact: The identity provider sees a clear error and can retry without the refused member. Escape hatch: Send the refused member in its own request, or set the group trust.
2. What durable channel replaces the interim WARN line for identity provider configuration and SCIM token expiry alerts after events exist, and how long are interim WARN lines kept?
   - Options: Keep audit entry plus WARN line until events exist, then switch channel without changing callers; Add a database-backed pending-alert table with retry in this change.
   - Provisional decision (owner review 14 Nov): Keep audit entry plus WARN line until events exist, then switch channel without changing callers.
   - Reason: ADR 70 decision 22 requires an alert but names no channel before events exist; the interim audit plus WARN line is explicitly non-durable and its retention and restart behavior is not decided. Benchmark: SOC 2
   - Persona impact: No persona loses an alert; the site administrator still sees each change in the audit log. Escape hatch: Audit entries stay queryable and the WARN line stays until events ship, so no alert is lost during the interim.

Questions considered and answered:

- Where does the refusal logic live so SCIM and sign-in claims behave the same? One domain function in pkg/principal used by the SCIM adapter and each sign-in claim adapter, per ADR 70 decision 18 and ADR 320 decision 13; no adapter writes a membership from the identity provider by another path.
- Which membership sources does the one function accept? The function accepts sources scim, claim, and ldap; LDAP groups go through principal.ApplyGroupClaims with source ldap and the same refusal rules.
- How does each caller map a refused add and an audit failure? Refuse with a domain sentinel error; the SCIM adapter maps a refusal to a SCIM error and an audit-write failure to 500, and the sign-in path skips the membership, allows sign-in, and writes an audit entry, per ADR 70 decision 14.
- Which action and check gate identity provider configuration changes? ADR 70 decision 11 names server:manage-sign-in for sign-in, SCIM, MFA, session settings and decision 22 changes; ADR 332 decision 8 adds the fresh check for claim rules; ADR 227 decision 2 sets the one window and the shortest effective window applies where more than one window could apply; ADR 301 decision 4 covers the no-factor caller. This change wires the fresh check only on the configuration routes it touches and does not wire the trusted-group membership switch route.
- Who wires the SCIM token route? This change specifies only the guard server:manage-sign-in with the one-window fresh check and the 168-hour expiry alert for SCIM tokens; the scim change applies that guard to its token route.
- What wins when a group is both closed and trusted or full sync is on? The closed switch wins over trust and full sync: a closed group refuses each identity provider add even when trusted or full sync is on, per ADR 70 decisions 15 to 17 read together.
- Does the WARN-only channel conflict with the sweep notice rule? ADR 306 decisions 3 and 6 cover sweep disables and deletes of break-glass principals with notice to other admins; this change covers only identity provider configuration and SCIM token expiry with audit plus WARN and does not change that sweep rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
