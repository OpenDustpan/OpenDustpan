---
version: 3
---

Roadmap: `docs/roadmap.md:171` (Enforce token expiry, lifetime, and inactivity disable.).

## Why

**Persona: developer.** A Developer runs the Pulumi CLI and OpenTofu against the server with an access token and needs an expired, revoked or idle token to stop with a clear 401, so a stale token never keeps access.

The user path this change must make work:

1. **API.** Developer creates an access token through the API with a picked expiry date
2. **CLI.** Developer runs pulumi up and tofu apply with the token
3. **Docs page.** Developer reads the token life cycle docs page for expiry, lifetime, inactivity disable and rotation
4. **API.** Developer rotates the token through the API before expiry and retries the CLI

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer runs pulumi up or tofu apply with a token past its expiry | 401 with error code token.ended and a message that names the ended token and the call that lists tokens. | `GET /api/server/caller/tokens` |
| Developer runs a CLI command with a token disabled for inactivity | 401 with error code token.ended and a message that names the disabled token and the call that lists tokens. | `GET /api/server/caller/tokens` |
| Developer requests a token with an expiry past the maximum lifetime | The server refuses the create with error code token.invalid and a message that names the maximum lifetime and the call that shows settings. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The roadmap assigns expiry, lifetime, inactivity disable, rotation and re-enable enforcement to this part of the split. Proof: docs/roadmap.md:171 and the split scope text in the context pack.
- Main refuses an expired, revoked or disabled token in the authenticator. Proof: pkg/accesstoken/service.go:632 (AuthenticateToken, the StatusAt check), tested by TestAuthenticateRefusesEndedTokens at pkg/accesstoken/accesstoken_test.go:458, as the PM finding states.
- Main sets the rotation overlap to 24 hours. Proof: DefaultRotationOverlap = 24 * time.Hour at pkg/accesstoken/service.go:30, tested by TestRotationEnd at pkg/accesstoken/accesstoken_test.go:630, as the PM finding states.
- Main runs the inactivity disable on a schedule tick. Proof: TestJobSchedulesRunInactivityDisableOncePerTick at pkg/dustpan/schedules_internal_test.go:341.

## What Changes

- Add a test that the authenticator refuses an idle-disabled token
- Map the refusal of an ended token to 401 on the Pulumi, TFE-compatible and tool-neutral routes, with audit entries
- Show the token limits in the settings resource with their source: maximum lifetime, inactivity period and rotation overlap of 24 hours
- Keep the rotation overlap at the main value of 24 hours

Out of scope:

- Warning field in token API responses and CLI warnings
- Expiry warning email through the SMTP queue
- Console banner for tokens that expire soon
- SCIM revocation setting and workload token exchange lifetime
- A change of the rotation overlap value

## Usability

Where the path still costs the person time or surprises them:

- A Developer with an ended token sees 401 and must list tokens to find the cause, which costs one extra command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main refuses ended tokens in AuthenticateToken. (`pkg/accesstoken/service.go:632`)
- Main tests the refusal of ended tokens. (`pkg/accesstoken/accesstoken_test.go:458`)
- Main sets the rotation overlap to 24 hours. (`pkg/accesstoken/service.go:30`)
- Main tests the rotation end time. (`pkg/accesstoken/accesstoken_test.go:630`)
- Main runs the inactivity disable on a schedule tick. (`pkg/dustpan/schedules_internal_test.go:341`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (extended), 4 (current), 8 (current), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 369, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 370, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Last-used time and expiry. Each access token records the time of its last use. Each access token has an expiry."
- Quote: "A maximum lifetime. An administrator sets the maximum lifetime of an access token. The server refuses a token with a longer lifetime."
- Quote: "Rotation. A rotation call issues a new access token and sets an end time for the old one. A principal can use the old token until that end time."
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: The change wires the existing checks to 401 with audit entries and shows the limits. The rotation end time keeps the value of main.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 2 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "The maximum lifetime is 365 days by default. It applies to personal tokens and to service tokens."
- Effect: The effective maximum shows in the settings resource with its source.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the authenticator need new expiry code? No. Main already refuses ended tokens at pkg/accesstoken/service.go:632. Only the idle-disabled test, the API wiring and the settings view are missing.
- What is the rotation overlap? 24 hours, the value of main. ADR 53 decision 8 sets an end time and gives no value.
- Warning fields and email? Not here. ADR 53 decision 11 belongs to the warning display part and the email queue part.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
