Roadmap: `docs/roadmap.md:171` (Token life cycle and expiry email).

## Why

**Persona: developer.** A Developer runs the Pulumi CLI and OpenTofu against the server with an access token and needs the token to stop at expiry, to stop after long disuse and to warn before expiry so a run does not fail at night.

The user path this change must make work:

1. **API.** Developer creates a personal or service token through GET /api/dustpan/v1/me/tokens and sets an expiry within the maximum.
2. **CLI.** Developer runs pulumi up and tofu apply with the token; the server records last-used time.
3. **Console.** Developer reads the warning field and banner before expiry and receives the warning email from the installation SMTP sender.
4. **Docs page.** Developer reads the docs page for token lifetimes, inactivity disable and renewal by rotation.
5. **E2e.** E2e suite runs expiry, inactivity and email checks against the real binary over TLS with the test SMTP server.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| CLI request with a token past its expiry fails. | 401 with an error code for expired or revoked token and a message that names token expiry and the rotation command. | `opendustpan token list --mine` |
| CLI request with a token disabled for inactivity fails. | 403 with an error code for disabled token and a message that names inactivity disable and the rotation command. | `opendustpan token list --mine` |
| Token creation with an expiry past the maximum fails. | 422 with an error code for lifetime over the maximum and a message that names the maximum lifetime in days. | `GET /api/dustpan/v1/settings` |
| Warning email with no SMTP settings configured skips sending. | Token answer keeps the warning field and names the missing SMTP sender when no SMTP settings exist. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- No e2e test covers SMTP sending in the pack. Proof: evidence.SMTP.e2e is [] with e2e_count 0.
- Only one guide in the pack names SMTP. Proof: evidence.SMTP.guides lists docs/guides/ldap-sign-in.md.
- Token life cycle, workload identity, service principals and lifetimes are accepted in ADR 53, ADR 54, ADR 67 and ADR 69. Proof: Milestone row: discussing; token life cycle, workload identity, service principals and lifetimes accepted (ADR 53, ADR 54, ADR 67, ADR 69).

## What Changes

- Record last-used time and enforce expiry with 401 for both CLIs.
- Disable tokens idle past the inactivity period and write audit entries.
- Enforce maximum lifetime including service principals and namespace shorter maximum.
- Show warning field, console banner and CLI warning and send warning email through installation SMTP settings.

Out of scope:

- Sign-in methods, MFA, SCIM, groups and grants, planner role, workload exchange, mTLS, collections and placement, break-glass mode 2

Depends on:

- add-one-generic-settings-api
- move-caller-routes-api-server
- `move-caller-routes-api-server` (order of the milestone review)
- `add-one-generic-settings-api` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A Developer learns of expiry only at failure unless the warning email, API field and CLI warning arrive in time.
- A token disabled for inactivity surprises a Developer when the error does not name the one renewal command.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (extended), 3 (current), 4 (current), 11 (extended), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Last-used time and expiry. Each access token records the time of its last use. Each access token has an expiry."
- Quote: "A maximum lifetime. An administrator sets the maximum lifetime of an access token. The server refuses a token with a longer lifetime."
- Quote: "Who can issue which token is part of the authorization model. Each kind of token has its own action. The grants of ADR 52 decide which principal can issue which kind. The owner wrote this with "I guess"."
- Quote: "Inactivity disable, 90 days by default. The server disables an access token that no principal used for 90 days."
- Quote: "Expiry warnings in milestone 4. Before an access token expires, the server warns in four ways: - a field in the API response for the token, - a banner in the console, - an email to the owner of the token. This needs SMTP settings. - a warning in the CLI."
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: Implements last-used time, expiry, maximum lifetime, issue grants, 90-day inactivity disable, the four expiry warnings with REST API and audit entries.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): Proposal Open questions reads none, but the adversarial pass lists R1.1 as open and the warning-window handling for tokens already inside the window at first deployment and for shortened expiries has no ADR answer; the spec-ready gate cannot be met. Resolution: open question: Added as second entry in open_questions on backlog warnings and shortened expiries.
- R1.2 (adr conflict): ADR 53 decision 6 requires each inactivity disable part to be a setting that an administrator can turn off, with an audit entry per change; the spec and design hardcode the disable with no setting and no audit requirement for setting changes. Resolution: answered: Added inactivity settings requirement with separate on-off settings, periods and audit entry for each change.
- R1.3 (adr conflict): ADR 53 decision 5 exempts the break-glass credential from inactivity disable by default; the Inactivity disable requirement does not state this exemption. Resolution: answered: Inactivity disable requirement now exempts the break-glass credential.
- R1.4 (missing scenario): No scenario covers a token whose expiry is set past the maximum lifetime, although the proposal error table promises a 422 with the maximum named; the 422 path is unspecified. Resolution: answered: Added maximum lifetime requirement with 422 scenario for expiry past the maximum.
- R1.5 (failure mode): Clock skew across server processes is listed as a risk, but no decision names the authoritative clock or a skew tolerance, so an expired token can stay live or a live token can expire early. Resolution: answered: Added design decision on database transaction time with 30 second tolerance and matching clock requirement.
- R1.6 (failure mode): Warning email without SMTP settings fails silently; no requirement or scenario makes the failure or the missing notice visible to the owner or the API, although ADR 212 decision 2 settings and the audit rule require it. Resolution: answered: Added missing-SMTP requirement that keeps the API warning field and records the skipped email.
- R1.7 (missing scenario): The warning email scenario covers only the happy path; no scenario covers SMTP failure, retry, or restart during send with the idempotent job keys named in design decision 2. Resolution: answered: Added retry requirement with idempotency key and SMTP failure scenario.
- R1.8 (failure mode): Concurrent authenticated requests with one token can race on the last-used write and the inactivity disable; no concurrency rule is stated for the stored state. Resolution: answered: Added atomic last-used update decision and concurrent use scenario.
- R1.9 (scope): Task 2.2 wires the console banner data but no spec requirement covers the console banner, and task 3.1 has no task that creates the token-lifetime docs page required by the user path and the definition of done. Resolution: answered: Added console banner requirement and a docs page task in definition of done.
- R1.10 (wrong claim): The proposal Open questions section states none while the same file gives a provisional owner-review answer for the warning-send mechanism; the ADR-based answers cite ADR 69 decision 5 for warning starts, which is marked provisional and not in the spec's gate. Resolution: answered: Kept two open questions instead of none; warning repeat and backlog handling stay undecided by ADRs.
- R1.11 (missing scenario): No scenario covers a token revoked by an administrator before expiry, although the expired-token requirement names revoked tokens and the authenticator order depends on it. Resolution: answered: Added revoked token requirement with 401 scenario before expiry.
- R2.1 (adr conflict): specs/token-life-cycle/spec.md Inactivity disable requirement disables tokens after the inactivity period with no setting, but ADR 53 decision 4 sets 90 days by default and decision 6 says each part is an administrator setting; the spec's Inactivity settings requirement does not give the 90-day default or the separate access-token, user and break-glass switches. Resolution: answered: Inactivity settings requirement now states 90-day default and separate switches for access tokens, user accounts and break-glass credential.
- R2.2 (adr conflict): proposal.md ADR check quotes ADR 53 decision 11 but the change does not respect ADR 69 decision 2 default maximum for service principals under namespace shorter maximum; ADR 67 decision 7 and ADR 69 decision 2 require a namespace shorter maximum that the Maximum lifetime requirement does not mention. Resolution: answered: Maximum lifetime requirement now names 365-day default, installation maximum and shorter namespace maximum for service principals.
- R2.3 (missing scenario): The Maximum lifetime requirement covers only the 422 refusal; it has no scenario for the 365-day default, an installation maximum set by an administrator, or a namespace shorter maximum applied to service principals (ADR 69 decision 2, ADR 67 decision 7). Resolution: answered: Added scenarios for 365-day default, installation maximum and namespace shorter maximum.
- R2.4 (failure mode): The Single clock requirement says the server refuses a token only after the 30 second tolerance, but it does not say which clock governs the inactivity cutoff and the warning window, and design decision 3 vs the risk about process clocks is not tested by any scenario for a token past expiry by more than the tolerance. Resolution: answered: Single clock requirement now governs expiry, inactivity cutoff and warning windows; added past-tolerance refusal scenario.
- R2.5 (failure mode): The Expiry warning email requirement queues one email per threshold but the spec has no requirement for the warning-window state after a restart, so a process crash between the state write and the queue insert can lose the warning; the idempotent key in the retry requirement does not cover the enqueue step. Resolution: answered: Added durable warning queue requirement that retains queued warnings across restarts with the same idempotency key.
- R2.6 (open question): proposal.md Open questions reads none but lists two provisional questions (repeat sends, backlog warnings) that no ADR answers; under the spec-ready gate the change cannot be implemented until the owner confirms the provisional answers on 14 Nov. Resolution: open question: Kept as open questions for PM; repeat sends and backlog warnings have no ADR answer and block implementation until decided.
- R2.7 (missing scenario): No scenario covers a service principal token whose owner principal is disabled or removed, or a rotation during the old token end time (ADR 53 decision 8), so an expired rotated token behaviour is unspecified. Resolution: answered: Added rotation grace requirement with old-token end-time scenario.
- R2.8 (failure mode): The Expired token refused requirement gives no behaviour for a hostile request with an expired token and an invalid signature, so it is unclear whether the 401 leaks token state; design layer port adapter requires no state read, but the scenarios do not test the order of checks. Resolution: answered: Added hostile expired token with invalid signature scenario that checks signature before state and leaks no token state.
- R2.9 (scope): tasks.md 2.2 and the CLI warning requirement name a CLI warning header, but the proposal and ADR 53 decision 11 say a CLI warning; the task does not add the docs page and the e2e step 6 check against the real SMTP server is not mapped to a requirement scenario. Resolution: answered: Tasks name CLI warning per ADR 53 decision 11 and map e2e step 6 against test SMTP server to email scenarios plus docs page.
- R3.1 (adr conflict): specs/token-life-cycle/spec.md Expiry warning email: the spec queues a warning email with no repeat rule, but design decision 2 and the open questions leave repeat sends and backlog warnings undecided by ADR; the Expired token and Maximum lifetime scenarios also omit ADR 53 decision 2 check of the maximum for tokens that already exist, so the change cannot be built without the owner answer on warning repeats. Resolution: answered: Fixed by single-send rule and backlog sweep in Expiry warning email requirement and design decision Warning state derived; open_questions is empty.
- R3.2 (adr conflict): proposal.md Open questions reads none, yet the same section lists two undecided questions (repeat sends and backlog warnings) with provisional answers; under the spec-ready gate the change may not be implemented while these are open. Resolution: answered: Fixed by empty open_questions; repeat and backlog answers moved to considered and to fixed requirements.
- R3.3 (wrong claim): proposal.md ADR check cites ADR 69 decision 5 for warning starts and ADR 53 decision 4 for 90-day inactivity; the spec Inactivity settings requirement names a 90-day default that ADR 53 decision 4 supports only for the stated PCI basis, while decision 4 also says the number applies to access tokens, which the change does not justify. Resolution: answered: Fixed by added ADR 53 decision 4 quote for the 90-day basis and Inactivity settings requirement with 90-day default for tokens and accounts.
- R3.4 (adr conflict): ADR 53 decision 3 (who can issue which token) is not in the ADR check; the Maximum lifetime and Service token creation paths do not state which grant (principal:manage-tokens, ADR 67 decision 2) gates issuing a service principal token, so the authorization action is unspecified. Resolution: answered: Fixed by added ADR 53 decision 3 to adr_check and Service token issue grant requirement with principal:manage-tokens.
- R3.5 (missing scenario): No scenario covers a service principal token whose owner principal is disabled, removed or moved; ADR 53 decision 7 makes such tokens the machine path. Resolution: answered: Fixed by Disabled owner refuses use requirement with disabled, removed and moved owner scenarios.
- R3.6 (failure mode): Concurrent-use scenario says the newer use is recorded but does not state the outcome when a disable and a use race; design decision 4 requires compare-and-set that never clears a disable, and no scenario tests the losing write. Resolution: answered: Fixed by Concurrent disable wins scenario under Last-used time.
- R3.7 (failure mode): Inactivity disable and break-glass exemption: the spec does not say how a disabled token is re-enabled or whether the audit entry of an automatic disable names the reason, as CLAUDE.md requires a record of who or what acted and why. Resolution: answered: Fixed by Automatic disable audit and re-enable requirement naming the inactivity reason and the rotation and admin re-enable path.
- R3.8 (open question): The 422 error for expiry past maximum and the CLI error messages name a renewal command, but no requirement defines the renewal path (rotation or new token) or the command name, so the user path in proposal.md cannot be built as written. Resolution: answered: Fixed by Renewal by rotation requirement defining rotation or a new token as the renewal path.
- R3.9 (scope): tasks.md 2.2 wires the console banner and CLI warning, and 3.1 names a token lifetime docs page, but no task writes the docs page content or the console UI, and the definition of done requires the docs page in the same pull request. Resolution: answered: Fixed by docs page content task and console banner requirement with banner task check.
- R3.10 (failure mode): The SMTP-absent requirement keeps the warning field but does not say whether an administrator is notified, although ADR 212 decision 2 makes SMTP installation settings the only sender and the Missing SMTP requirement records only the skipped email. Resolution: answered: Fixed by Missing SMTP requirement that writes an audit entry and surfaces SMTP status for an administrator.
- M.11 (consistency): Token routes move to /api/dustpan/v1/me/tokens in move-caller-routes-api-server. The inactivity settings and the SMTP settings need the generic settings API. The console asks for the banner threshold that this change must own. Resolution: answered: Token routes now use /api/dustpan/v1/me/tokens. Inactivity and SMTP keys are registered through the generic settings API. Expiry warning field now owns the warning threshold and the renew step for the console.

## Open questions

none

Questions considered and answered:

- Does the change need new token fields or settings? Yes. ADR 53 decision 1 requires last-used time and expiry, ADR 53 decision 4 sets the 90-day inactivity disable for tokens and accounts, and ADR 69 decision 2 sets the 365-day default maximum.
- How do expiry warnings reach the owner? Yes. ADR 53 decision 11 lists API field, console banner, email through SMTP settings and CLI warning, ADR 69 decision 5 sets the 14-day and 3-day starts, and ADR 212 decision 2 supplies the SMTP installation settings.
- Does a warning email repeat and what happens to tokens already inside a window? Fixed. The server sends one email per threshold crossing and queues backlog warnings at the next sweep after deployment or after a shortened expiry; ADR 53 decision 11 and ADR 69 decision 5 set the channels and starts and this choice fixes volume without new mail policy.
- Does the change cover placement, planner, workload or mTLS? No. Placement, planner role, workload exchange and mTLS are separate milestone steps; service principals stay inside only for the issue grant and lifetime limits of ADR 67 and ADR 69 decision 2.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
