---
version: 9
---

Roadmap: `docs/roadmap.md:171` (Enforce token expiry, lifetime, and inactivity disable.).

## Why

**Persona: developer.** A Developer runs the Pulumi CLI and OpenTofu against the server with an access token and needs an expired, revoked or idle token to stop with a 401, so a stale token never keeps access. The answer must not tell an attacker whether a token existed. The Developer then finds the cause in the list of ended tokens.

The user path this change must make work:

1. **API.** Developer creates an access token through the API with a picked expiry date
2. **CLI.** Developer runs pulumi up and tofu apply with the token
3. **API.** Developer lists the ended tokens with GET /api/dustpan/v1/me/tokens?status=ended to see the cause of a 401
4. **Docs page.** Developer reads the access tokens guide for expiry, lifetime, inactivity disable, rotation and the status filter
5. **API.** Developer rotates the token through the API before expiry and retries the CLI

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer runs pulumi up or tofu apply with an expired, revoked or disabled token | 401 unauthenticated, with no detail about the token. The bytes and headers equal those for an unknown token. The audit entry holds the real reason and the token ID. | `GET /api/dustpan/v1/me/tokens?status=ended` |
| Developer requests a token with an expiry past the maximum lifetime | The server refuses the create with error code token.invalid and a message that names the maximum lifetime. | `GET /api/dustpan/v1/settings` |

Evidence for the claims above:

- The roadmap assigns expiry, lifetime, inactivity disable, rotation and re-enable enforcement to this part of the split. Proof: docs/roadmap.md:171 and the split scope text in the context pack.
- The pack lists a test of the token lifetime in main. Proof: TestLifetime at pkg/accesstoken/accesstoken_test.go:311.
- Main runs the inactivity disable on a schedule tick. Proof: TestJobSchedulesRunInactivityDisableOncePerTick at pkg/dustpan/schedules_internal_test.go:341.
- Main refuses a disabled principal in the token check. Proof: TestCheckActiveRefusesADisabledPrincipal at pkg/accesstoken/accesstoken_test.go:554.
- The own token list does not take a status filter on main: `ownTokenPageRequest` at internal/api/server/tokens.go:819 accepts only `pageSize` and `pageToken` and answers 400 for any other name, and docs/guides/access-tokens.md:60 says so. The administrator lists parse `status` with accesstoken.ParseFilter at internal/api/server/tokens.go:900, and accesstoken.FilterEnded keeps revoked and expired tokens (pkg/accesstoken/accesstoken.go:175). A Developer has no administrator right, so the step 3 path needs the filter on the own list. The PM decided: the change adds it (benchmark: the guide of the administrator lists already names the three values).
- Main looks a token up by the hash of the secret and compares the digest in constant time before the ended check. Proof: Review finding PM.tim: pkg/accesstoken/service.go:637-642 (Service.AuthenticateToken: digestOfRaw, then GetTokenByDigest) and pkg/accesstoken/accesstoken.go:301 (Digest.Equal with the constant-time compare of the standard library); StatusAt runs after that lookup.
- Main audits every refused sign-in on the request path. Proof: Review finding PM.tim: internal/signinsvc/refuse.go:41, Service.refuse writes an entry for each refusal.

## What Changes

- Add a test that the authenticator refuses an idle-disabled token
- Add one new shared refusal function that every 401 case of token authentication uses, so unknown, expired, revoked and disabled tokens give the same bytes and headers
- Keep the lookup by digest and the constant-time compare before the ended check
- Write an audit entry on the request path for each refusal, with the real reason and the token ID, and with the reason unknown token and no token ID for an unknown token
- Show the token limits in the settings resource with their source: maximum lifetime, inactivity period and rotation overlap
- Keep the rotation overlap at the value of main
- Add the query name `status` (active, expiring or ended; every token when absent) to GET /api/dustpan/v1/me/tokens, with the same values and the same parser as the administrator lists. The route keeps `pageSize` and `pageToken`, and still answers 400 for `cursor`, `limit` and `principal`
- Document the status filter in the own-list table of docs/guides/access-tokens.md and drop `status` from the sentence that lists the refused names

Out of scope:

- Warning field in token API responses and CLI warnings
- Expiry warning email through the SMTP queue
- Console banner for tokens that expire soon
- SCIM revocation setting and workload token exchange lifetime
- A change of the rotation overlap value
- A refusal text that names the ended token
- Tests that measure wall-clock time

## Usability

Where the path still costs the person time or surprises them:

- A Developer with an ended token sees a plain 401 and lists ended tokens with status=ended to find the cause, which costs one extra call that the guide now documents.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests the token lifetime. (`pkg/accesstoken/accesstoken_test.go:311`)
- Main refuses a disabled principal in the token check. (`pkg/accesstoken/accesstoken_test.go:554`)
- Main runs the inactivity disable on a schedule tick. (`pkg/dustpan/schedules_internal_test.go:341`)
- Main looks a token up by digest, then compares it in constant time. (`pkg/accesstoken/service.go:637`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (extended), 8 (current), 13 (current).
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends. Chain to the end: ADR 69, ADR 70, ADR 212, ADR 237, ADR 354, ADR 297, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 369, ADR 232, ADR 235, ADR 301, ADR 365, ADR 252, ADR 285, ADR 324, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 370, ADR 288, ADR 253, ADR 312.
- Current rule: this ADR, with ADR 69, 70, 212, 237, 354.
- Quote: "Last-used time and expiry. Each access token records the time of its last use. Each access token has an expiry."
- Quote: "A maximum lifetime. An administrator sets the maximum lifetime of an access token. The server refuses a token with a longer lifetime."
- Quote: "Rotation. A rotation call issues a new access token and sets an end time for the old one. A principal can use the old token until that end time."
- Quote: "API and audit. Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: The change keeps the checks of main, adds audit entries with the real reason, and shows the limits. The rotation end time keeps the value of main.
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

- Does the authenticator need new expiry code? No. Main already refuses ended tokens. Only the shared refusal, the idle-disabled test, the audit reason and the settings view are missing.
- What is the rotation overlap? The value of main. ADR 53 decision 8 sets an end time and gives no value.
- Warning fields and email? Not here. ADR 53 decision 11 belongs to the warning display part and the email queue part.
- Does the answer to an ended token name it, or stay the plain 401? The owner decided on 14 Nov 2026: the plain 401 of main, with an audit entry that holds the real reason and the token ID. The person finds ended tokens with the status=ended list.
- Can an attacker tell an ended token from an unknown token by timing? The owner asked for no timing leak. Main does one lookup by digest and one constant-time compare for both cases, and the ended check runs after. The refusal is one shared function, and the audit write happens on the request path for both cases.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
