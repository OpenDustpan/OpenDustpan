Roadmap: `docs/roadmap.md:171` (Send expiry warning email through SMTP queue.).

## Why

**Persona: operator.** Operator runs an installation where people use short-lived access tokens. Operator sets SMTP once so the server warns each token owner before expiry and no token stops a run without notice.

The user path this change must make work:

1. **API.** Set installation SMTP relay and sender through the settings API; set the SMTP password only through env or file.
2. **Server start.** Start the server with SMTP settings from env or file; warning sender starts with it.
3. **Docs page.** Follow the docs page to set SMTP and check the test SMTP server gets the warning.
4. **E2e.** Run e2e with the test SMTP server and check the sent warning and the audit entry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| SMTP settings missing: warning email cannot be sent. | The send stays queued and the token warning keeps email status unsent with reason smtp-not-configured. Set the relay and sender in the installation settings, then wait for the next poll. | `GET /api/dustpan/v1/settings` |
| SMTP send refused: plain connection refused or relay unreachable. | The row moves to retry once and then to failed with the reason. Fix the relay or the plain-SMTP opt-in setting, then wait for the next warning time. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- Milestone step 6 requires the server to send the warning email to the test SMTP server before expiry. Proof: An access token records its last-used time. A token past its expiry gives 401 to both CLIs. A token with no use for the inactivity period is disabled. Before the expiry, the server sends the warning email to the test SMTP server
- This change is part 3 of the token expiry split and covers the SMTP queue with one retry, not enforcement or display. Proof: This part sends expiry warning email through installation SMTP settings with a durable queue and one retry.

## What Changes

- Queue one expiry warning email row per token per warning time and send through installation SMTP settings.
- Retry a failed send once after 15 minutes, then mark failed with the reason.
- Record send state queued, sending, retry, sent or failed for the display part to show.

Out of scope:

- Token expiry enforcement and inactivity disable.
- Warning field in API and warning in CLI.
- SMTP setting declaration and settings API shape.
- Service principal lifetime limits.

Depends on:

- token-life-cycle-expiry-email-token-expiry-enforcement
- token-life-cycle-expiry-email-expiry-warning-display
- `token-life-cycle-expiry-email-expiry-warning-display` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operator still opens two pages to link a failed send to the SMTP setting that caused it.
- Token owner sees unsent status with no resend action and waits for the next warning time.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 11 (extended).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Expiry warnings in milestone 4. Before an access token expires, the server warns in four ways:"
- Effect: This part sends only the email warning of decision 11. Enforcement and display stay in the other two parts.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "Expiry warnings. The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry."
- Effect: The queue creates one row for each of the two warning times of decision 5.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 2 (extended).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "SMTP settings are installation settings."
- Effect: The sender reads relay and sender from installation settings. The password comes only from env or file, never from the database.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Refused by default (owner). The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "An explicit opt-in (owner). An installation can allow plain connections with one setting."
- Quote: "Fail closed. If the setting is missing or not valid, the value is off."
- Effect: The dialer refuses plain SMTP except to loopback unless the opt-in allows it, and fails closed with a refusal that names the setting.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The change is not spec-ready. The code check fails on route GET /api/dustpan/v1/access-tokens/{id}, which is not in the API description or the route table. Task 2.3 depends on this route. Name the route as new or use an existing one. Resolution: answered: Removed the token read route from tasks and what_changes. Status display is read by the display part listed in depends_on; this part only writes queue state.
- R1.2 (adr conflict): The user path step 1 sets SMTP credentials through the settings API. ADR 212 decision 2 says the SMTP password comes only from an environment variable or a *_FILE file, never from a flag or the database, and a PATCH of it answers 409. Remove the credential step from the API or cite a decision that allows it. Resolution: answered: User path step 1 now sets only relay and sender through the settings API. Credentials come only from env or file per ADR 212 decision 2.
- R1.3 (failure mode): The design lists duplicate email on sender restart as a risk, but no requirement or scenario covers a crash after SMTP accepts the message and before the row moves to sent, or a lease that expires during a send. Add an idempotency rule and a test. Resolution: answered: Added idempotent single-send requirement with lease, sending state and send-time recheck plus a crash-after-accept scenario.
- R1.4 (missing scenario): ADR 292 decisions 2 and 5 are not in the spec. There is no scenario for the plain-SMTP opt-in set on, and none for an opt-in value that is missing or invalid, which must fail closed to off. The refusal must also name the setting to change. Resolution: answered: Added TLS requirement with scenarios for opt-in on, opt-in missing or not valid failing closed, and refusal naming the setting.
- R1.5 (missing scenario): Design decision 2 says a missing owner address marks the row failed with reason missing address. The spec has no such scenario, so the build cannot be checked against it. Resolution: answered: Added missing-address scenario in the queue requirement.
- R1.6 (open question): The retry backoff duration, the sender lease duration, the poll interval and the retry limit are not set anywhere. The constructor checks values that the change never defines. Resolution: answered: Fixed poll 60 seconds, lease 300 seconds, backoff 15 minutes and one retry in design and queue requirement.
- R1.7 (missing scenario): No scenario covers a token that is revoked, disabled, deleted or rotated, or whose expiry changes, before its queued warning is sent. The sender must check the token state before it sends. Resolution: answered: Added stale-token scenario for revoked, disabled, deleted or changed-expiry tokens.
- R1.8 (failure mode): The layer table requires refusal of CRLF in the sender address and an empty relay, and that the password never reaches the queue, log or audit entry. The spec has no requirement for these, so the hostile tests have no scenario to map to. Resolution: answered: Added input-validation and credential-secrecy requirement mapped to the hostile tests.
- R1.9 (scope): Task 2.3 exposes email status on the token read path. The roadmap and the proposal put display in the other part of the split, so this task overlaps that item. Resolution: answered: Removed the display task from this part; display stays in the display part in depends_on.
- R1.10 (wrong claim): The error table says to requeue the warning and to check the queue status, but no requeue route or queue status route is defined in the change. The command named for the refusal, GET /api/dustpan/v1/settings, does not requeue anything. Resolution: answered: Error answers no longer promise a requeue or queue-status route; they name GET settings and the next poll or warning time.
- R1.11 (missing scenario): Each automatic send must write an audit record that names the sender and the reason (project rule). The e2e step checks an audit entry, but no requirement defines it. Resolution: answered: Added audit requirement for each send, skip and failure.
- R1.12 (open question): The design names the states queue, retry, sent and failed, and the spec uses queued. The state names are not one set, and the spec does not show a retry state. Resolution: answered: Unified states to queued, sending, retry, sent and failed in design and spec.

## Open questions

none

Questions considered and answered:

- When does the server send the warning email and with which settings? ADR 53 decision 11 requires the email warning and names SMTP settings as its need; ADR 212 decision 2 places those settings at installation scope; ADR 69 decision 5 sets the two send times.
- Does the queue allow plain SMTP? ADR 292 decisions 1, 2 and 5 refuse plain SMTP by default, allow one opt-in setting, and fail closed, so the sender must enforce the same rule and name the setting in each refusal.
- Does this part enforce expiry or show the API warning field? The split scope keeps enforcement and the API warning field in the other two parts, so this part queues, sends, retries once, and records send state for the display part to read.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
