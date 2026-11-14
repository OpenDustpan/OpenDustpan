Roadmap: `docs/roadmap.md:171` (Enforce token expiry, lifetime, and inactivity disable.).

## Why

**Persona: developer.** A Developer runs the Pulumi CLI and OpenTofu against the server with an access token and needs expired or idle tokens to stop with a clear 401 so a stale token never keeps access.

The user path this change must make work:

1. **API.** Developer creates an access token through the API with a picked expiry date
2. **CLI.** Developer runs pulumi up and tofu apply with the token
3. **Docs page.** Developer reads the token life cycle docs page for expiry, lifetime, inactivity disable, and rotation
4. **API.** Developer rotates the token through the API before expiry and retries the CLI

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer runs pulumi up or tofu apply with a token past its expiry | 401 with error code token.expired and message that names the expired token and the API call that shows settings. | `GET /api/dustpan/v1/settings` |
| Developer runs a CLI command with a token disabled for inactivity | 401 with error code token.disabled and message that names the disabled token and the API call that shows settings. | `GET /api/dustpan/v1/settings` |
| Developer requests a token with an expiry past the maximum lifetime | 403 with error code token.lifetime-exceeded and message that names the maximum lifetime and the API call that shows settings. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The milestone roadmap splits token life cycle into three parts and assigns enforcement of expiry, lifetime, inactivity disable, rotation, and re-enable to this part. Proof: docs/roadmap.md:171 step 6 and the split scope statement in the context pack.
- Token life cycle, maximum lifetime, inactivity disable, rotation, and token API with audit are accepted decisions. Proof: docs/adr/0053-access-token-life-cycle.md decisions 1, 2, 6, 8, and 13 as quoted in adr_check.
- Maximum lifetime of 365 days by default applies to personal and service tokens, with a person-picked expiry date. Proof: docs/adr/0069-token-lifetime-and-session-defaults.md decisions 1 and 2 as quoted in adr_check.

## What Changes

- Record last-used time on each authenticated token use
- Answer 401 for expired, revoked, and idle-disabled tokens on Pulumi, TFE-compatible, and tool-neutral APIs
- Enforce maximum lifetime at token create and rotation time for personal and service tokens
- Disable tokens with no use for the inactivity period and support re-enable with audit entries
- Rotate tokens with a 10-minute overlap where the old token stays valid until its end time

Out of scope:

- Warning field in token API responses and CLI warnings
- Expiry warning email through the SMTP queue
- Console banner for tokens that expire soon
- SCIM revocation setting and workload token exchange lifetime

## Usability

Where the path still costs the person time or surprises them:

- A Developer with an expired token sees 401 and must list tokens to find the expiry, which costs one extra command.
- A Developer mid-pipeline at rotation time must switch to the new token within 10 minutes or the next command fails.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (extended), 4 (current), 6 (changed), 8 (current), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Last-used time and expiry. Each access token records the time of its last use. Each access token has an expiry."
- Quote: "A maximum lifetime. An administrator sets the maximum lifetime of an access token. The server refuses a token with a longer lifetime."
- Quote: "Inactivity disable, 90 days by default. The server disables an access token that no principal used for 90 days."
- Quote: "Rotation. A rotation call issues a new access token and sets an end time for the old one. A principal can use the old token until that end time."
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: This part implements expiry, maximum lifetime, inactivity disable with its settings, rotation and API with audit entries. It does not implement warning fields or email.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "The person picks the expiry date. The API takes an expiry date when it makes a token. The console shows a date picker, with short choices such as 1 day next to longer ones up to the maximum. The server refuses a date after the maximum lifetime."
- Quote: "The maximum lifetime is 365 days by default. It applies to personal tokens and to service tokens. An administrator can set another maximum for the installation. A namespace can set a shorter maximum for the tokens of its principals (ADR 67)."
- Effect: This part enforces the person-picked expiry against the 365-day default maximum and the shorter installation and namespace maxima at create and rotation time.
- Conflict: none.

**ADR 67: Who creates and administers a service principal** (not owner-tagged).

- Decisions that apply: 2 (changed).
- Changed by: ADR 70 changes decisions 2, 3, 4 and 5; ADR 70 extends decision 6; ADR 210 changes; ADR 215 changes decision 7. Chain to the end: ADR 70, ADR 210, ADR 215, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 247, ADR 286, ADR 248, ADR 149, ADR 297, ADR 273, ADR 335, ADR 351, ADR 301, ADR 339, ADR 295, ADR 333.
- Current rule: this ADR, with ADR 70, 210, 215.
- Quote: "**The right to make tokens for a service principal is a grant on that principal.** The action `principal:manage-tokens` has the service principal as its object."
- Effect: Only a holder of principal:manage-tokens on a service principal rotates or revokes its tokens.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Token creation and rotation use the one fresh-authentication window as fresh steps.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Effect: A change that turns the inactivity disable off or to a longer period needs a fresh authentication step.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): Spec rotation-overlap and tasks do not apply the owner-tagged ADR 227 decision 2 ('One window everywhere (owner)'), which names token creation and rotation as fresh-authentication steps. The spec has no fresh-authentication requirement for create or rotate. Fix in specs/token-expiry-enforcement/spec.md and proposal.md. Resolution: answered: Added fresh-auth-create-rotate requirement and ADR 227 to adr_check; rotation and create now need a fresh step in the one window.
- R1.2 (adr conflict): Spec has no requirement for the fresh MFA step when the inactivity disable is turned off or changed to a weaker setting. ADR 212 decision 3 (owner-tagged) requires a fresh MFA step for a change that reduces security. Tasks 2.3 adds a re-enable path with no such step. Resolution: answered: Added fresh-auth-weaker-setting requirement per ADR 212 decision 3; turning the disable off or longer needs a fresh step.
- R1.3 (open question): The spec-ready gate is not met: proposal.md open question 1 (old-token overlap length) is marked 'Decides: PM' with no provisional decision, and the section does not read 'none'. The spec's rotation-overlap requirement cannot be built without this value. Resolution: answered: Removed the open question; rotation-overlap-fixed sets a 10-minute overlap with an at-once revoke escape hatch.
- R1.4 (wrong claim): The error table and the usability path name the commands 'opendustpan token list' and 'opendustpan settings get'. The check reports that the 'token' and 'settings' commands do not exist in the CLI command list, so the one-command help promised to the Developer is not real. Resolution: answered: Replaced nonexistent CLI commands with real API calls in the error table.
- R1.5 (missing scenario): Spec covers only expired tokens. Revoked tokens, and tokens disabled for inactivity over the Pulumi and TFE routes, have no 401 scenario. The tool-neutral route has no scenario at all, although design layer HTTP requires 401 for all denials with an audit entry. Resolution: answered: Added refuse-revoked-disabled with Pulumi, TFE-compatible, and tool-neutral 401 scenarios plus audit.
- R1.6 (missing scenario): Inactivity disable has no value and no setting. ADR 53 decision 4 sets 90 days by default, decision 5 exempts the break-glass credential, and decision 6 makes each part an administrator setting with an audit entry per change. None of these appear in the spec. The re-enable path named in tasks 2.3 and proposal has no requirement. Resolution: answered: Added inactivity-disable-default with 90-day default and break-glass exemption, inactivity-settings-audit, and re-enable requirements.
- R1.7 (missing scenario): Rotation has no scenario for the old token after its end time (it must answer 401), no audit entry (ADR 53 decision 13), and no rule for who may rotate a service token (ADR 67 decision 2 needs principal:manage-tokens). The design says lifetime is checked at rotation time, but the spec does not say so. Resolution: answered: Added rotation-old-refused, token-audit, authorize-service-rotate, and lifetime check at rotation in enforce-maximum-lifetime.
- R1.8 (missing scenario): Maximum lifetime has no source. ADR 69 decision 2 makes 365 days the default, lets an administrator set another installation maximum, and lets a namespace set a shorter one (ADR 67). The spec does not say which maximum applies, and the 403 scenario covers create only, not service tokens at rotation. Resolution: answered: Enforce-maximum-lifetime now names the 365-day default, the installation maximum, and the shorter namespace maximum for personal and service tokens.
- R1.9 (failure mode): Design decision 2 says a failed last-used write never blocks authentication and is retried, but no scenario covers a failed write. The write-per-request contention risk named in design has no mitigation or test. Concurrent revocation during an in-flight request is not covered. Resolution: answered: Added last-used-failure scenario where a failed write never blocks authentication; risk notes bounded retry outside the auth transaction.
- R1.10 (failure mode): Clock skew is listed as a risk (a token accepted a few seconds past expiry) but no bound or decision is given. The spec and design do not say whether that window is accepted or how it is tested. Resolution: answered: Risks now state strict expiry comparison with no grace window; no clock-skew acceptance window remains.
- R1.11 (missing scenario): Design layer port adapter requires the live-token query and the state-write fence in the same transaction, and a hostile test with an expired row and a disabled row. No spec requirement or task covers the store query or the transaction fence. Resolution: answered: Added live-token-query requirement with hostile store scenario and fence in the same transaction.
- R1.12 (scope): Tasks 3.2 and 2.2 require audit entries for each refusal and each token operation, but the spec has no audit requirement for successful create, rotate or disable. The e2e check cannot prove what the spec does not require. Resolution: answered: Added token-audit requirement for create, rotate, disable, re-enable, and settings changes so e2e checks audit entries.

## Open questions

none

Questions considered and answered:

- Does this part record last-used time and refuse expired tokens? Yes. ADR 53 decision 1 requires last-used time and expiry, and decision 13 requires a REST API and an audit entry for each token operation.
- Does this part enforce maximum lifetime at create and rotation time? Yes. ADR 53 decision 2 with ADR 69 decisions 1 and 2 require refusal of a token past the maximum lifetime of 365 days by default, with shorter installation and namespace maxima.
- Does this part disable idle tokens with settings and audit? Yes. ADR 53 decision 4 sets 90 days by default with a break-glass exemption, and decision 6 makes each part an administrator setting with an audit entry for each change.
- Does this part show warning fields or send email? No. ADR 53 decision 11 and ADR 69 decision 5 define warning fields and email. The split assigns them to the warning display part and the email queue part.
- How long does the old token stay valid after rotation? 10 minutes. ADR 53 decision 8 requires an end time with no length. A fixed 10-minute overlap lets running CLI commands finish, and an administrator revokes the old token at once to end it early.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
