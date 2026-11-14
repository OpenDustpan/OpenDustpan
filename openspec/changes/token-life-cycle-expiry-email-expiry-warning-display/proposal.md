---
version: 3
---

Roadmap: `docs/roadmap.md:171` (Show token expiry warnings in API and CLIs.).

## Why

**Persona: developer.** A developer runs the Pulumi CLI and OpenTofu with a personal token. The developer wants a clear warning before the token expires, so a long run does not fail with 401.

The user path this change must make work:

1. **API.** Developer reads own tokens and sees the expiry time, the seconds left and the warning level.
2. **Console.** Developer opens the console and sees the banner for a token that expires soon.
3. **CLI.** Developer runs the first-party command that reads the token and sees the warning text.
4. **Docs page.** Developer follows the docs page to rotate the token before expiry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A request with an expired token fails. | 401 with error code token.invalid. Create a new personal token, then run the command again. | `GET /api/server/caller/tokens` |
| A read of a token without the token read grant fails. | 403 with the missing action and object. No warning field is returned. Ask an administrator for the grant. | `GET /api/server/caller/tokens` |

Evidence for the claims above:

- ADR 53 requires an API field, a console banner, an email and a CLI warning before token expiry. Proof: docs/adr/0053-access-token-life-cycle.md
- ADR 69 sets the warning times at 14 days and 3 days as settings. Proof: docs/adr/0069-token-lifetime-and-session-defaults.md
- This change is part 2 of the split of the token expiry email change. Proof: docs/roadmap.md:171
- A test for the warning computation exists in pkg/accesstoken. Proof: pkg/accesstoken/accesstoken_test.go:1386

## What Changes

- Add the expiry warning level and the seconds left to the existing token reads under /api/server/tokens, /api/server/caller/tokens and /api/server/principals/{}/tokens
- Give the console banner the same data from the caller token read
- Read the 14-day and 3-day thresholds from settings
- Leave the carriage in tool-compatible responses to an open question

Out of scope:

- Expiry enforcement, lifetime limits and inactivity disable
- Expiry warning email and SMTP queue
- Expiry events and webhooks
- Warning text in the real Pulumi CLI and OpenTofu output

Depends on:

- token-life-cycle-expiry-email-token-expiry-enforcement
- `token-life-cycle-expiry-email-token-expiry-enforcement` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A developer finds the token that nears expiry from the token list alone.
- The docs page names the one rotation command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestWarningFor covers the warning computation (`pkg/accesstoken/accesstoken_test.go:1386`)
- TestExpiryWarningOfAnEndedToken covers an ended token (`pkg/accesstoken/accesstoken_test.go:1413`)
- Error code token.invalid exists (`internal/errcode/catalog_token.go:11`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 11 (extended), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Expiry warnings in milestone 4. Before an access token expires, the server warns in four ways:"
- Quote: "a field in the API response for the token,"
- Quote: "a banner in the console,"
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: The API field and the banner data follow decision 11. Each read keeps its action and audit entry as decision 13 says.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "Expiry warnings. The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry. Both are settings."
- Effect: The two start times are settings that the warning level reads.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which field or header of the tool-compatible responses can make the real Pulumi CLI and OpenTofu show a warning, and with which text?
   - Options: Send a custom response header and use a wrapper script; Use a body field that each CLI already prints; Show the warning only in the token reads, the console banner and first-party command output.
   - Provisional decision (owner review 14 Nov): Show the warning only in the token reads, the console banner and first-party command output until a spike proves a field the real CLIs render.
   - Reason: ADR 53 decision 11 requires a CLI warning but names no field. The pack proves no CLI behavior for a custom header. Benchmark: AWS IAM shows expiring credential warnings in the console and the API, and leaves CLI text to the client.
   - Persona impact: A developer sees the warning in the API and the console in every option. Escape hatch: The docs page states which surfaces show the warning, and a later change adds the CLI carriage.
2. Do the threshold settings keys exist, and what do negative, zero and inverted values mean?
   - Options: Reuse existing keys; 0 disables a window; negative is refused; Add new keys with the same rule.
   - Provisional decision (owner review 14 Nov): Reuse existing keys if present; 0 disables a window; negative is refused; urgent wins when both match.
   - Reason: ADR 69 decision 5 says both are settings but the pack names no keys. Benchmark: AWS Organizations refuses negative periods at the settings API.
   - Persona impact: No persona loses access. A refused PATCH names the valid range. Escape hatch: An administrator reverts the settings PATCH. The next read uses the old value.

Questions considered and answered:

- Does this part send the warning email? No. ADR 53 decision 11 lists email as a separate way and the split scope puts it in the email queue part.
- Is the warning level stored? No. It is computed on each read from the server clock, the expiry and the two settings, so a restart or a threshold change applies at the next read.
- Does this part change expiry or disable rules? No. The enforcement part owns them. This part reads the expiry only.
- Does client clock skew change the count? No. The response carries seconds left from the server clock.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
