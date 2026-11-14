---
version: 2
---

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
| Developer runs pulumi up or tofu apply with a token past its expiry | 401 with error code token.invalid and a message that names the expired token and the call that lists tokens. | `GET /api/server/caller/tokens` |
| Developer runs a CLI command with a token disabled for inactivity | 401 with error code token.ended and a message that names the disabled token and the call that lists tokens. | `GET /api/server/caller/tokens` |
| Developer requests a token with an expiry past the maximum lifetime | 403 with error code token.invalid and a message that names the maximum lifetime and the call that shows settings. | `GET /api/dustpan/v1/settings` |

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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main records token last use and expiry and tests lifetime and expiry warnings in the accesstoken suite. (`pkg/accesstoken/accesstoken_test.go:311`)
- Main tests expiry warning of an ended token in the accesstoken suite. (`pkg/accesstoken/accesstoken_test.go:1413`)
- Main computes the warning window for tokens in the accesstoken suite. (`pkg/accesstoken/accesstoken_test.go:1386`)
- Main refuses authentication for a disabled principal. (`pkg/accesstoken/accesstoken_test.go:554`)
- Main runs inactivity disable on a schedule tick. (`pkg/dustpan/schedules_internal_test.go:341`)
- Main names token error codes for invalid, ended, and not-found tokens. (`internal/errcode/catalog_token.go:11`)

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
- Effect: This part enforces the person-picked expiry against the 365-day default maximum and the shorter installation and namespace maxima at create and rotation time, with each effective value visible in the settings resource.
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
- Effect: Token creation and rotation by a person use the one fresh-authentication window as fresh steps; an access token never gives a fresh authentication.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Effect: A change that turns the inactivity disable off or to a longer period, or that raises a maximum lifetime, needs a fresh authentication step.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this part record last-used time and refuse expired tokens? Yes. ADR 53 decision 1 requires last-used time and expiry, and decision 13 requires a REST API and an audit entry for each token operation.
- Does this part enforce maximum lifetime at create and rotation time? Yes. ADR 53 decision 2 with ADR 69 decisions 1 and 2 require refusal of a token past the maximum lifetime of 365 days by default, with shorter installation and namespace maxima.
- Does this part disable idle tokens with settings and audit? Yes. ADR 53 decision 4 sets 90 days by default with a break-glass exemption, and decision 6 makes each part an administrator setting with an audit entry for each change.
- Does this part show warning fields or send email? No. ADR 53 decision 11 and ADR 69 decision 5 define warning fields and email. The split assigns them to the warning display part and the email queue part.
- How long does the old token stay valid after rotation? 10 minutes as a fixed documented overlap. The overlap is not a setting. The effective value shows in the settings resource so the default stays visible, and revocation ends the old token at once.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
