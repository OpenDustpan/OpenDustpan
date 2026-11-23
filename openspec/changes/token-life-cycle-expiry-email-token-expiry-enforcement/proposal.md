---
version: 10
---

Roadmap: `docs/roadmap.md:171` (Enforce token expiry, lifetime, and inactivity disable.).

## Why

**Persona: developer.** A Developer runs the Pulumi CLI and OpenTofu against the server with an access token. An expired, revoked or disabled token must stop with a 401, and the 401 must not tell an attacker whether the token existed. The Developer then finds the cause in the list of ended tokens, and the administrator finds it in the audit log.

Main already refuses an ended token. What main lacks is small:

- The refusal has no type. The reason of each refusal is only in the text of a wrapped error, and no audit entry exists for a refused access token on the request path.
- The own token list, `GET /api/dustpan/v1/me/tokens`, has no `status` filter, so a Developer cannot list the ended tokens. The guide says so on purpose today.
- The token policy shows the maximum lifetime and the inactivity period, but not the rotation overlap.

The user path this change must make work:

1. **API.** Developer creates an access token through the API with a picked expiry date.
2. **CLI.** Developer runs pulumi up and tofu apply with the token.
3. **API.** After the token ends, the CLI gets a 401. Developer lists the ended tokens with `GET /api/dustpan/v1/me/tokens?status=ended` to see which token ended.
4. **Docs page.** Developer reads the access tokens guide for expiry, lifetime, inactivity disable, rotation overlap and the status filter.
5. **API.** Developer rotates the token before expiry and retries the CLI.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Developer runs pulumi up or tofu apply with an expired, revoked or disabled token | 401 `auth.unauthenticated` with the text "unauthenticated". The bytes and headers equal those for an unknown token. The audit entry holds the real reason and the token ID. | `GET /api/dustpan/v1/me/tokens?status=ended` |
| Developer sends `status=gone` to the own list | 400 `request.invalid-query`. The text names active, expiring and ended. | `GET /api/dustpan/v1/me/tokens?status=ended` |

## What Changes

- Add a typed refusal in `pkg/accesstoken`. Every refusal of `Service.AuthenticateToken` returns it. It wraps `identity.ErrUnauthenticated`, carries a reason from a fixed list and the token ID (empty for an unknown token), and has one fixed `Error()` text. One unexported function builds it, so a new refusal case cannot skip it.
- Write an audit entry on the request path for each refusal of a value that has the form of an access token secret: the real reason and the token ID, or the reason "unknown token" and no token ID. A decorator in `pkg/dustpan` writes it, outside the failure gate of the access token lookups, so the miss budget of the gate bounds the number of entries.
- Add the query name `status` (active, expiring or ended; every token when absent) to `GET /api/dustpan/v1/me/tokens`, with the parser of the administrator lists. The route keeps `pageSize` and `pageToken`, and still answers 400 for `cursor`, `limit` and `principal`.
- Show `rotationOverlapSeconds` in the answer of `GET /api/server/token-policy`, read only. A `PATCH` that names it answers 400, as for any unknown field.
- Add guard tests for behavior that main has and no test covers: an idle-disabled token, the digest compare after the lookup, the same bytes and headers for each refusal on the three route families.
- Document the status filter, the audit entry of a refusal and the rotation overlap in `docs/guides/access-tokens.md` and `docs/guides/audit-log.md`.

Out of scope:

- A refusal text that names the ended token (the owner decided against it: see the ADR check).
- The warning fields, the email and the console banner (the two sibling changes).
- A change of the rotation overlap value, or a setting for it. A setting changes token policy defaults, and that needs the owner.
- A settings resource for the token limits. `GET /api/server/token-policy` already is that resource.
- SCIM revocation and the workload token exchange lifetime.
- Tests that measure wall-clock time.

## Usability

- A Developer with an ended token sees a plain 401 and needs one more call, `status=ended`, to find the cause. The guide documents the call. The 401 text stays the same for a Developer and for an attacker, by decision of the owner.
- An administrator reads the real reason of each refusal in the audit log.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- `Service.AuthenticateToken` refuses a value that is not a secret, an unknown digest, a stored token of an unknown kind, a digest that differs from the lookup, an expired, revoked or disabled token, a disabled owner, the namespace service principal, an inactive token and an inactive account, each with `identity.ErrUnauthenticated`, in this order. A failed store read answers its own error. (`pkg/accesstoken/service.go:632-695`, function `AuthenticateToken`; `checkPolicyActive` at `:751`)
- The lookup by digest runs before every status check, and the compare of the stored digest uses the constant-time compare of the standard library. (`pkg/accesstoken/service.go:637-659`; `Digest.Equal` at `pkg/accesstoken/accesstoken.go:347`)
- `Token.StatusAt` answers revoked, then disabled, then expired, else active. (`pkg/accesstoken/accesstoken.go:163`)
- A test covers a revoked token, an expired token and a deleted owner. No test covers a token that the inactivity sweep disabled, and none compares the refusals. (`pkg/accesstoken/accesstoken_test.go:486`, `TestAuthenticateRefusesEndedTokens`)
- A test covers the rotation: the old token works before its end time and gets a refusal after it. (`pkg/accesstoken/accesstoken_test.go:637-653`)
- The rotation end time defaults to `DefaultRotationOverlap` (24 hours), bounded by the old expiry. (`pkg/accesstoken/service.go:27-30`, `rotationEnd` at `:479`)
- The inactivity disable runs on a schedule tick, once for all instances. (`pkg/dustpan/schedules_internal_test.go:345`, `TestJobSchedulesRunInactivityDisableOncePerTick`)
- The chain of the server asks the bootstrap authenticator first. Then a gate of the access tokens counts the failures of a source, charges only values with the form of a secret, and answers `identity.ErrRateLimited` when the budget is spent. (`pkg/dustpan/area_signin.go:119-172`, `newAccessTokenGates` and `chainOf`; `internal/authgate/authgate.go:491`)
- The three route families map `identity.ErrUnauthenticated` to one fixed 401 text, whatever the wrap text says: Pulumi `internal/api/pulumi/pulumi.go:1189`, TFE-compatible `internal/api/tfe/errors.go:443`, tool-neutral `internal/api/problem/problem.go:454`.
- `Service.CheckActive` re-checks a token during a running update and wraps its own text for a revoked, expired, disabled or orphaned token. (`pkg/accesstoken/service.go:275-288`; the caller at `pkg/stack/updates.go:1196`)
- The refused sign-in writes an audit entry with the reason. A refused access token writes none. (`internal/signinsvc/refuse.go:41-90`)
- The own list takes `pageSize` and `pageToken` only, and answers 400 for any other name, `status` included. (`internal/api/server/tokens.go:817-826`, `ownTokenPageRequest`; the 400 text at `:70`; the route description at `internal/api/server/operations_caller.go:57-60`; the guide at `docs/guides/access-tokens.md:60`; the test at `internal/api/server/caller_routes_test.go:157`)
- The administrator lists parse `status` with `accesstoken.ParseFilter`. The filter `ended` keeps the revoked, the disabled and the expired tokens in the SQL. The comment of `FilterEnded` names only revoked and expired. (`internal/api/server/tokens.go:900`; `pkg/accesstoken/accesstoken.go:197`; `internal/store/sqlite/queries.sql:2031`)
- `GET /api/server/token-policy` answers the maximum lifetime, the inactivity period, the two disable switches and the exchange lifetime. The change body is decoded strictly. (`internal/api/server/tokenpolicy.go:36-45`, `:80-110`; the guide table at `docs/guides/access-tokens.md:256`)
- The audit `Entry` has the fields `TokenID`, `Reason`, `SourceIP` and `RequestID`, and `AppendServer` writes in the server scope. (`pkg/audit/audit.go:412-439`, `:815`; `requestOf` at `pkg/dustpan/dustpan.go:1073`)
- The e2e suite drives the Pulumi CLI and the API through the expiry warnings. It has no step for a refused ended token. (`e2e/tokenexpiry_test.go:27`)

Audit corrections against the earlier draft of this change: the earlier text said that the test of a disabled token fails on main (it passes), that a settings resource shows token limits (the token policy does, and it lacks only the overlap), that the sign-in audit covers every refusal (it covers sign-ins only), and it cited stale lines. The path `GET /api/server/caller/tokens` in the decision of the owner is an old name: main serves the own list at `/api/dustpan/v1/me/tokens`, and the old path answers 404 (`internal/api/server/caller_routes_test.go:134`).

## ADR check

Each quote is copied from the file in `docs/adr/`. The chains come from `docs/adr/INDEX.md`.

**ADR 373: The job actions in the built-in roles, the answer to an ended token, and the break-glass pass** (owner-tagged: "Decided by the project owner").

- Decision that applies: 2 (owner, 2026-11-14).
- Changed by: none in the index.
- Quote: "An expired, revoked or disabled token gets the same 401 as an unknown token. The answer does not name the token. The audit entry holds the real reason and the ID of the token."
- Quote: "So an ended token and an unknown token take the same path and the same work before the 401: the lookup by the hash of the secret, the compare in constant time, then the check of the end. An unknown token also writes an audit entry, with the reason "unknown token" and no token ID. A test checks the structure, not the time: one refusal function for each 401, the same bytes and headers for each case, and the compare in constant time."
- Effect: this decision fixes the answer, the audit entry and the kind of test. The change follows it and decides nothing new on the refusal text.
- Conflict: none. One note: the decision names the route `GET /api/server/caller/tokens?status=ended`. Main serves the own list at `/api/dustpan/v1/me/tokens`, so this change adds the filter there. The path in the decision is a name that no longer exists, not a different decision.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged; its context records answers of the owner).

- Decisions that apply: 1, 2, 8 and 13.
- Changed by: ADR 69 extends decisions 2 and 11; ADR 70 changes decisions 5, 6 and 10; ADR 70 extends; ADR 212 extends decision 11; ADR 237 changes decision 9; ADR 354 extends; ADR 373 extends. Current rule: this ADR, with ADR 69, 70, 212, 237, 354, 373.
- Quote (decision 1): "Each access token records the time of its last use. Each access token has an expiry."
- Quote (decision 2): "An administrator sets the maximum lifetime of an access token. The server refuses a token with a longer lifetime."
- Quote (decision 8): "A rotation call issues a new access token and sets an end time for the old one. A principal can use the old token until that end time."
- Quote (decision 13): "Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: the refusal audit entry is the audit entry of decision 13 for the use of a token. The overlap value stays as main: decision 8 gives no value.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decision that applies: 2. Changed by: ADR 297 changes decision 4 only.
- Quote: "The maximum lifetime is 365 days by default. It applies to personal tokens and to service tokens."
- Effect: none. The maximum lifetime is already in the token policy.
- Conflict: none.

**ADR 250: Defense in depth for security code** (not owner-tagged).

- Quote: "Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Effect: the design has one hostile-case table for each layer and the tasks have one test for each guard step.

## Open questions

none

Questions considered and answered:

- Does the refusal of an ended token name the token? No. ADR 373 decision 2 (owner, 2026-11-14) answers: the plain 401, with an audit entry that holds the real reason and the token ID. The item of that question in `decisions/pending/QUEUE.md` still reads as open. That is a record to close, not a question of this change.
- Is an audit entry for an unknown token a flood vector? ADR 373 decision 2 requires the entry. The decorator sits outside the failure gate, so only a refusal that the gate lets through writes an entry. The gate waits a request over the budget of its source for a penalty period before the lookup, and it answers the rate-limit error to a request when too many requests of that source already wait (`internal/authgate/authgate.go:167-171`, `:525-550`). So the gate bounds the entries that one source can cause, and a rate-limit answer writes none.
- What is the rotation overlap? The value of main, 24 hours. The change shows it and does not make it a setting.
