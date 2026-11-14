Roadmap: `docs/roadmap.md:171` (Show token expiry warnings in API and CLIs.).

## Why

**Persona: developer.** A developer runs the Pulumi CLI and OpenTofu with a personal token. The developer wants a clear warning before the token expires, in the API, in the console, and in the CLI, so a long run does not fail with 401.

The user path this change must make work:

1. **API.** Developer reads own tokens through the API and sees expiry time and warning level.
2. **CLI.** Developer runs work with the Pulumi CLI and sees the expiry warning.
3. **CLI.** Developer runs work with OpenTofu and sees the same expiry warning.
4. **Console.** Developer opens the console and sees the banner for a token that expires soon.
5. **Docs page.** Developer follows the docs page to rotate the token before expiry.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A CLI request with an expired token fails before any warning shows. | 401 with the expired token code. The token no longer works for any command. Rotate the token. | `pulumi up` |
| A read of a token without the token read grant fails and shows no warning field. | 403 with the missing action and object. The caller cannot read the token record. Ask an administrator for the token read grant. | `pulumi up` |

Evidence for the claims above:

- ADR 53 requires an API field, a console banner, an email, and a CLI warning before token expiry. Proof: docs/adr/0053-access-token-life-cycle.md
- ADR 69 sets the warning times at 14 days and 3 days before expiry as settings. Proof: docs/adr/0069-token-lifetime-and-session-defaults.md
- This change is part 2 of the token expiry email split and covers the warning field, banner data, and CLI warning. Proof: docs/roadmap.md:171

## What Changes

- Add new route GET /api/dustpan/v1/me/tokens and token list read with expiry warning level and time left
- Expose the same warning data for the console expiry banner
- Carry the warning in Pulumi-compatible and TFE-compatible responses so both CLIs show it
- Read the 14-day and 3-day thresholds from settings

Out of scope:

- Expiry enforcement, lifetime limits, and inactivity disable
- Expiry warning email and SMTP queue
- Expiry events and webhooks
- Service principal lifetime limits beyond display

Depends on:

- token-life-cycle-expiry-email-token-expiry-enforcement
- `token-life-cycle-expiry-email-token-expiry-enforcement` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A developer with many tokens must find which token nears expiry from the token list alone.
- A CLI warning must name the days left and the one rotation command without extra text.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 11 (extended), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Expiry warnings in milestone 4. Before an access token expires, the server warns in four ways:"
- Quote: "a field in the API response for the token,"
- Quote: "a banner in the console,"
- Quote: "a warning in the CLI."
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: Decision 11 names the API field, the console banner, and the CLI warning that this part builds. Decision 13 requires a named action for each token read that returns the warning field.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 297 changes decision 4. Chain to the end: ADR 297, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 297.
- Quote: "Expiry warnings. The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry. Both are settings."
- Effect: The warning levels use the two start times of decision 5 as settings and apply the boundary rule stated in the spec.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 2 (extended).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "SMTP settings are installation settings."
- Effect: This part sends no email and changes no SMTP setting. It only reads whether a sender exists to decide banner copy.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (scope): The change lists the route GET /api/dustpan/v1/me/tokens as a token read path, but the Open questions section records it as absent from the API description and route table; the change must name it as new or use an existing route before it is spec-ready (proposal.md, tasks.md 2.1). Resolution: answered: Named GET /api/dustpan/v1/me/tokens and the list read as new routes in what_changes and tasks.
- R1.2 (open question): The change does not define the warning thresholds' units or boundary: whether a token expiring exactly at 14 or 3 days is 'early' or 'urgent', and which warning level applies when both thresholds are set to 0 or the second is larger than the first. ADR 69 decision 5 only says the warnings start 14 and 3 days before expiry (design.md Structure, spec.md Requirement Warning field). Resolution: answered: Added inclusive boundary rule, level precedence, and zero and inverted threshold behavior to design and to the threshold requirement.
- R1.3 (missing scenario): No scenario covers a token whose expiry has already passed. The proposal says a CLI request with an expired token fails with 401 before any warning shows, but the spec never states what the read path answers for an expired token (warning level, time left, or refusal). Resolution: answered: Added expired token requirement and scenario: read still answers expiry data with level expired and zero seconds left.
- R1.4 (missing scenario): No scenario covers a token with no expiry, or a service principal token whose lifetime is limited by ADR 67 and ADR 69. Null or absent expiresAt is not defined, so the warning level for such tokens is unknown. Resolution: answered: Added no-expiry requirement and scenario: warning level none with null expiry, including service tokens without expiry.
- R1.5 (missing scenario): No requirement covers authorization of the warning field. The proposal names a 403 when the token read grant is missing, but the spec and tasks have no scenario for that refusal, and ADR 53 decision 13 requires a named action for each token operation. Resolution: answered: Added authorization requirement and refusal scenario tied to the token read action.
- R1.6 (failure mode): The design accepts that a CLI which ignores the warning field shows no warning, but no fallback or test is defined for it. The e2e step (tasks 3.2) does not check a CLI version that ignores the field. The risk is recorded but not covered. Resolution: answered: Stated the API field as source of truth and added e2e and task checks that verify the API field even when a CLI ignores the carriage.
- R1.7 (failure mode): Clock skew between server and client is listed as a risk, but the spec does not say whether the server computes time left from its own clock only, nor how the response carries that time so a client cannot compute a different count. Resolution: answered: Stated server clock only and seconds left carried in the response in the computation requirement.
- R1.8 (open question): ADR 53 decision 11 says the warning is in the CLI, but the change carries it in the tool-compatible response headers or body without naming which field. The Pulumi and TFE response shapes are not stated, so the contract is open for both CLIs. Resolution: answered: Named the exact carriage: token response fields plus warning header and body field on tool-compatible responses and first-party CLI text.
- R1.9 (missing scenario): The console banner scenario covers only the current session token. It does not cover a token of another principal read by an administrator, nor a read that is refused for the console session. The usability note asks that a developer find the token near expiry from the list, but no requirement covers the list read. Resolution: answered: Added list read requirement covering the token list, other principals, and refused reads.
- R1.10 (wrong claim): The ADR check says ADR 53 decision 11 is the only decision applied, but the text 'Effect' claims the change adds the console banner data; the console data is an API consumer and the claim that the same API field drives it is not shown by the cited ADR text (ADR 53 decision 11 names a banner, not an API field shared with it). Resolution: answered: Split ADR 53 effect to decision 11 for banner and decision 13 for the named action on the read.
- R1.11 (open question): The change does not say whether the warning level is stored or computed on each read, nor how it behaves across a server restart or a change of the threshold settings while a token is in its warning window; design decision 1 says read-only but the revision and cache behavior are unstated. Resolution: answered: Stated computed on each read with no storage, restart safe, threshold change applies at next read.

## Open questions

none

Questions considered and answered:

- Does this part include the expiry warning email? No. ADR 53 decision 11 names four warning ways and ADR 69 decision 5 sets the two times as settings. This part covers the API field, the console banner data, and the CLI warning. Email stays in the email queue part.
- Does this part change SMTP settings? No. ADR 212 decision 2 keeps SMTP as installation settings. This part changes no SMTP setting and sends no mail.
- Does this part change expiry or inactivity disable rules? No. The split scope says the enforcement part owns expiry and disable rules. This part only reads expiry and remaining time to compute the warning state.
- Is the warning level stored or computed? Computed on each read from the server clock from expiry and the two threshold settings. No stored value, so a restart or a threshold change applies at the next read.
- How does clock skew affect the day count? The server clock alone decides seconds left, and the response carries seconds left, so a client never recomputes the count from its own clock.
- What happens when a CLI ignores the warning carriage? The API field is the source of truth. A CLI that ignores the carriage still leaves the warning visible in the token read, and the e2e step checks the API field.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
