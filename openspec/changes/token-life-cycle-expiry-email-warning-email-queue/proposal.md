---
version: 2
---

Roadmap: `docs/roadmap.md:171` (Send expiry warning email through SMTP queue.).

## Why

**Persona: operator.** Operator runs an installation where people use short-lived access tokens. Operator sets SMTP once so the server warns each token owner before expiry and no token stops a run without notice.

The user path this change must make work:

1. **API.** Set installation SMTP relay and sender through `PATCH /api/dustpan/v1/settings`; set the SMTP password only through env or file.
2. **Server start.** Start the server with SMTP settings from env or file; the warning sender starts with it.
3. **Docs page.** Follow the docs page to set SMTP and check that the test SMTP server gets the warning.
4. **E2e.** Run e2e against the real binary over TLS with the test SMTP server and a personal access token; check the sent warning and the audit entry through `GET /api/dustpan/v1/namespaces/{namespace}/audit`. BLOCKED by the expiry enforcement and display parts for the CLI warning check only.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| SMTP settings missing: warning email cannot be sent. | The row stays queued with reason smtp-not-configured until the token expires, then moves to failed with reason expired. Set the relay and sender in the installation settings, then wait for the next poll. | `GET /api/dustpan/v1/settings` |
| SMTP send refused for the relay address: plain connection refused or relay unreachable. | The row moves to retry once after 15 minutes for a 4xx error and to failed for a 5xx error with the reason. Fix the relay or the plain-SMTP opt-in setting for the refused address, then wait for the retry. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- Milestone step 6 requires the server to send the warning email to the test SMTP server before expiry. Proof: An access token records its last-used time. A token past its expiry gives 401 to both CLIs. A token with no use for the inactivity period is disabled. Before the expiry, the server sends the warning email to the test SMTP server
- This change is part 3 of the token expiry split and covers the SMTP queue with one retry, not enforcement or display. Proof: This part sends expiry warning email through installation SMTP settings with a durable queue and one retry.

## What Changes

- Queue one expiry warning email row per token per warning time and send through resolved installation SMTP settings.
- Retry a temporary failed send once after 15 minutes, fail a permanent send at once, then mark failed with the reason.
- Record send state queued, sending, retry, sent or failed with reason for the display part to show.

Out of scope:

- Token expiry enforcement and inactivity disable.
- Warning field in API and warning in CLI.
- Declaration of SMTP setting keys and settings API shape.
- Service principal lifetime limits.

Depends on:

- `token-life-cycle-expiry-email-expiry-warning-display` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operator still opens two pages to link a failed send to the SMTP setting that caused it.
- Token owner sees unsent status with no resend action and waits for the next warning time.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main computes the expiry warning of a token in the access token package. (`pkg/accesstoken/accesstoken_test.go:1386`)
- Main has an expiry warning case for an ended token. (`pkg/accesstoken/accesstoken_test.go:1413`)
- Main runs a scheduled tick for token inactivity disable. (`pkg/dustpan/schedules_internal_test.go:341`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 11 (extended).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Expiry warnings in milestone 4. Before an access token expires, the server warns in four ways:"
- Effect: This part sends only the email warning of decision 11. Enforcement and display stay out of scope.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "Expiry warnings. The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry."
- Effect: The queue reads the two warning times as settings and creates one row for each time that still applies.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 2 (extended).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "SMTP settings are installation settings."
- Effect: The sender reads relay and sender from installation settings. The password comes only from env or file, never from the database.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 5 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Refused by default (owner). The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "An explicit opt-in (owner). An installation can allow plain connections with one setting."
- Quote: "The setting is visible (owner). The API shows its effective value and its source."
- Quote: "The hosted service never turns it on (owner). The hosted service does not allow the setting."
- Quote: "Fail closed. If the setting is missing or not valid, the value is off."
- Quote: "TLS checks stay. A connection with TLS keeps the certificate checks of RFC 21."
- Effect: The dialer refuses plain SMTP except to loopback unless the opt-in allows it, fails closed, names address, setting and command in each refusal, keeps TLS certificate checks, and never enables plain on the hosted service.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: route `GET /api/dustpan/v1/namespaces/{namespace}/audit` (.user_path[3].step) is not in the API description or the route table; use an existing route (nearest: /api/server/namespaces/{}/unit-grants/{}; a code check fails: test `TestWarningQueue` (.tasks[1].items[0].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: test `TestPlainRefused` (.tasks[1].items[1].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: and 7 more.

Questions considered and answered:

- When does the server send the warning email and with which settings? ADR 53 decision 11 requires the email warning and names SMTP settings as its need; ADR 212 decision 2 places those settings at installation scope; ADR 69 decision 5 sets the two send times and says both are settings.
- Does the queue allow plain SMTP? ADR 292 decisions 1, 2 and 5 refuse plain SMTP by default, allow one opt-in setting, and fail closed. Decision 5 requires each refusal to name the address, the setting and the one command that changes it.
- Does this part enforce expiry or show the API warning field? No. Enforcement and the API warning field are in the other two parts of the split.
- Which setting keys hold SMTP and warning times, and can the queue promise no duplicate email? No new setting is declared here. The sender reads resolved settings. SMTP cannot give at-most-once after accept, so the design states at-least-once after accept with a single lease holder before accept.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
