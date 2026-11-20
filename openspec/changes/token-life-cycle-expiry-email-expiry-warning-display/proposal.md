---
version: 7
---

Roadmap: `docs/roadmap.md:171` (Show token expiry warnings in API and CLIs.).

## Why

**Persona: developer.** A Developer uses a personal access token with the Pulumi CLI and OpenTofu. The Developer wants to see a warning before the token ends, so a long run does not fail with a 401.

The earlier draft of this change asked for a warning field in the token reads, seconds left, a response header, a CLI warning and a console banner. Main already has the field, the header and the CLI warning, and the console already has the banner component. What main lacks is one link: the console banner reads a route that the server no longer serves.

- The console asks the old caller path of the own list with `status=expiring`, `limit` and `cursor`, and reads `total` from the answer (console repository: `src/api-tokens.ts:213`, `src/tokens-model.ts:163-169`). The server serves the own list at `GET /api/dustpan/v1/me/tokens`. The old path answers 404, the new route takes `pageSize` and `pageToken` only, refuses `status` with 400, and has no `total`. So the banner and the list of the page cannot load from the current server.

The user path this change must make work:

1. **API.** Developer reads own tokens and sees `expiresAt`, `expiresInDays` and `expiryWarning` (main does this).
2. **CLI.** Developer runs a first-party command and sees one warning, from the header (main does this).
3. **Console.** Developer opens the access tokens page and sees the banner for the tokens that expire soon (this change).
4. **Docs page.** Developer reads the guide for the three places of the warning, and for the console banner (this change).

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The console banner cannot load the expiring tokens | The access tokens page shows its error state, not an empty banner. | `GET /api/dustpan/v1/me/tokens?status=expiring` |
| A request with an expired token | 401 `auth.unauthenticated`, the same text as for an unknown token. | `GET /api/dustpan/v1/me/tokens?status=ended` |

## What Changes

- Server (this repository): none in code. The own list `status` filter that the console needs belongs to `token-life-cycle-expiry-email-token-expiry-enforcement`. This change depends on it and adds the tests that tie the warning to that route (see the tasks).
- Console (repository `OpenDustpan/console`, a separate change that this change hands off in a task): the token client reads `GET /api/dustpan/v1/me/tokens` with `pageSize` and `pageToken`, parses `nextPageToken`, and drops the use of `total`. The banner counts the expiring tokens from the page it gets, and says "at least N" when a next page exists.
- Docs: `docs/guides/access-tokens.md` states that the console shows a banner for the tokens that expire within 14 days, and no longer says that this release has none.

Out of scope:

- The email (the sibling change `token-life-cycle-expiry-email-warning-email-queue`).
- Enforcement, and the status filter itself.
- A warning text in the output of the real Pulumi CLI or OpenTofu. Main proves no field that those CLIs print, and the e2e suite records that they show none.
- Storing the two warning times as settings. The decision of the owner on token lifetime defaults says "Both are settings", and main has constants. The names and the home of the settings are not decided. They are parked.
- Seconds left in the answer. `expiresInDays` is the field that main, the console and the CLI use.

Depends on:

- `token-life-cycle-expiry-email-token-expiry-enforcement` (the `status` filter of the own list).

## Usability

- The Developer sees the banner at the top of the access tokens page, with the count of the tokens that end within 14 days, before the first CLI failure.
- The banner and the list use one route, so they cannot disagree.

## What main already does

Each fact names its file, line and function on main (the console facts name its repository).

- The token answer has `expiresInDays` (null for a token that is not active) and `expiryWarning` (none, soon, imminent) from the server clock. The administrator lists, the own list, the create, the rotate and the read all use `tokenOf`. (`internal/api/server/tokens.go:92-117`, `:221-241`, `:526`, `:887`, `:928`, `:983`)
- `WarningFor` gives imminent at 3 days or less, soon at 14 days or less, and none for an ended token. (`pkg/accesstoken/accesstoken.go:419-422`, `:447`; tests `pkg/accesstoken/accesstoken_test.go:1414`, `:1441`)
- The header `Dustpan-Token-Expires-At` is set on each answer to a stored access token that warns, and not for the bootstrap token. (`pkg/dustpan/dustpan.go:3503-3512`; the call at `pkg/dustpan/ipallow.go:463`)
- The CLI prints one warning for each command when it reads the header. (`internal/cli/client/expiry.go:12-70`)
- An e2e test drives the field, the header and the CLI warning, and records that the tool CLIs show no warning. (`e2e/tokenexpiry_test.go:18-30`, `:80-106`)
- The guide documents the field, the header and the CLI warning, and says that this release has no console banner. (`docs/guides/access-tokens.md:220-238`)
- The own list at `/api/dustpan/v1/me/tokens` takes `pageSize` and `pageToken`, answers `tokens` and `nextPageToken`, and has no `total`. (`internal/api/server/tokens.go:168-171`, `:819-826`)
- The old own path answers 404. (`internal/api/server/caller_routes_test.go:134`; `e2e/caller_routes_test.go:135`)
- The console has the banner `ExpiryWarning` and the page that loads the expiring tokens through `listOwnTokens`, with `status`, `limit` and `cursor`, from the old path. (console: `src/Tokens.tsx:22-90`, `src/api-tokens.ts:211-220`, `src/tokens-model.ts:163-169`)

Audit corrections against the earlier draft: it asked for a field, seconds left, a header and a CLI warning that main has (seconds left is `expiresInDays` on main); it said that the thresholds are read from settings (they are constants on main); and it did not see the broken console route.

## ADR check

Each quote is copied from the file in `docs/adr/`. The chains come from `docs/adr/INDEX.md`.

**ADR 53: Access token life cycle, inactivity disable and expiry warnings** (not owner-tagged; its context records answers of the owner).

- Decisions that apply: 11 and 13. Changed by: ADR 69 extends decisions 2 and 11; ADR 212 extends decision 11; ADR 354 extends; ADR 373 extends. Current rule: this ADR, with ADR 69, 70, 212, 237, 354, 373.
- Quote (11): "a field in the API response for the token," and "a banner in the console," and "a warning in the CLI."
- Quote (13): "Every token operation has a REST API and an action (ADR 17). Each operation writes an audit entry (ADR 41)."
- Effect: the field and the CLI warning are on main. The banner is the part that this change makes work.
- Conflict: none.

**ADR 69: Token lifetime and session defaults** (not owner-tagged).

- Decision that applies: 5. Changed by: ADR 297 changes decision 4 only.
- Quote: "The warnings of ADR 53 decision 11 start 14 days and again 3 days before the expiry. Both are settings."
- Effect: main has constants (`pkg/accesstoken/accesstoken.go:419-422`). This change keeps them. Moving them to settings is parked.
- Conflict: none for this change. The settings part of the decision is not done on main.

## Open questions

none

## Parked (not questions of this change)

- Where the two warning times are stored as settings, with which names, and what a value of zero or a negative value means.
- A carriage of the warning in the output of the real Pulumi CLI and OpenTofu. It needs a spike that proves a field those CLIs print.
