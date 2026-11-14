---
version: 2
---

Issue: #1264, #1497.

## Why

**Persona: developer.** A developer manages their own sign-in state through the API: they check what they can do and they make and revoke their own access tokens. Today the preferences routes sit at /api/dustpan/v1/me/preferences, while access, grants and tokens still sit at /api/server/caller. One caller needs two prefixes, and the API description gate leaves those routes outside the description.

The user path this change must make work:

1. **Docs page.** Read the account docs page and call GET /api/dustpan/v1/me/access and GET /api/dustpan/v1/me/grants.
2. **API.** List, create, rotate and delete own tokens through /api/dustpan/v1/me/tokens.
3. **CLI.** Use the token with the Pulumi CLI and OpenTofu against the server.
4. **Console.** Use the console account views that read the same /me routes.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The caller calls an old /api/server/caller path after the move. | 404 from the router. The server keeps no alias and no redirect. | `curl -H "Authorization: Bearer $TOKEN" https://server/api/dustpan/v1/me/access` |
| The caller calls a /me route with no access token. | 401 with a stable error code. | `curl -H "Authorization: Bearer $TOKEN" https://server/api/dustpan/v1/me/access` |
| The caller sends cursor, limit, a bad pageToken or a pageSize above the maximum. | 400 with a stable error code. The message names pageSize and pageToken. | `curl -H "Authorization: Bearer $TOKEN" "https://server/api/dustpan/v1/me/tokens?pageSize=50"` |

Evidence for the claims above:

- The caller routes live in the installation adapter. Proof: path internal/api/server exists; issue 1264 lists GET /api/server/caller/access, GET /api/server/caller/grants and the token routes under /api/server/caller/tokens
- The mount helper for v1 paths is in pkg/dustpan. Proof: code fact: mountV1 at pkg/dustpan/dustpan.go:3636
- The not-moved list and the fail-closed test exist. Proof: main: TestClassCheckFailsClosed at pkg/dustpan/apidesc_test.go:420; path pkg/dustpan/apidesc_classes_test.go exists
- Coverage rows live in e2e/coverage.go. Proof: path e2e/coverage.go exists; issue 1264 asks to move each route with its row
- The caller routes are in the route table of main. Proof: main routes: ANY /api/server/caller/tokens, /api/server/caller/tokens/{}, /api/server/caller/access and /api/server/caller/grants

## What Changes

- GET /api/dustpan/v1/me/access replaces GET /api/server/caller/access
- GET /api/dustpan/v1/me/grants replaces GET /api/server/caller/grants
- GET and POST /api/dustpan/v1/me/tokens replace the token list and create
- DELETE /api/dustpan/v1/me/tokens/{id} and the rotate route replace the old ones
- The old paths stop being served, with no alias
- The six entries leave the not-moved list; the description, e2e rows and docs follow

Out of scope:

- The other 92 routes of the installation adapter that issue 1497 lists
- Token life cycle, expiry email and SMTP settings
- Console section lock reasons

## Usability

Where the path still costs the person time or surprises them:

- A script with an old path gets 404 and finds the new path on the docs page.
- A client that sent cursor and limit gets a 400 that names pageSize and pageToken.
- A caller creates a spare token before it rotates the only working token.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- mountV1 mounts handler paths under the v1 prefix (`pkg/dustpan/dustpan.go:3636`)
- TestClassCheckFailsClosed checks the not-moved list (`pkg/dustpan/apidesc_test.go:420`)
- Label.WithQuery declares query parameters (`internal/api/apidesc/collect.go:89`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (extended), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "One prefix for the tool-neutral REST API. Each route of the tool-neutral REST API moves under `/api/dustpan/v1/`."
- Quote: "No path holds the word "server"."
- Quote: "Lists** page with an opaque cursor. A request takes `pageSize` and `pageToken`."
- Effect: The caller routes move under the v1 prefix with no alias. Paths hold no word server. Moved lists use pageSize, pageToken and nextPageToken. Decision 6 (Idempotency-Key) applies to the mutating routes, and the pack shows only its scope start, so this change adds no rule beyond the header being accepted.
- Conflict: none.

**ADR 276: Every error answer has a code and its arguments** (owner-tagged: decision 1, 3).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every error answer has a code (owner). Each 4xx and 5xx answer of each HTTP adapter has a stable machine code."
- Quote: "The arguments, from the start (owner). An answer carries the values that its text names, as arguments."
- Effect: Each 4xx answer of a moved route carries a catalog code with declared arguments.
- Conflict: none.

## Open questions

1. What are the default pageSize and the maximum pageSize of the moved token list? Decides: PM.
   - Options: default 50, maximum 200; default 20, maximum 100.
   - Recommended: default 50, maximum 200 Reason: The ADRs fix the names and give no numbers. Tests read the declared values from the API description, so the numbers can change in one place.

Not spec-ready: a code check fails: route `/api/server/caller` (.summary) is not in the API description or the route table; use an existing route (nearest: /api/server/caller/tokens/{}, /api/server/caller/tokens, /api/server/caller/gran; a code check fails: route `GET /api/dustpan/v1/me/access` (.user_path[0].step) is not in the API description or the route table; use an existing route (nearest: /api/server/caller/access, /api/dustpan/v1/me/preferences, ; a code check fails: route `GET /api/dustpan/v1/me/grants` (.user_path[0].step) is not in the API description or the route table; use an existing route (nearest: /api/server/principals/{}/grants/{}, /api/server/principals; a code check fails: and 6 more.

Questions considered and answered:

- Do old paths stay as aliases? No. ADR 210 decision 1: no alias and no redirect, because no tag shipped them.
- Do cursor and limit keep their names? No. ADR 210 decision 10 names pageSize and pageToken.
- Do the moved mutating routes take Idempotency-Key? Yes, by ADR 210 decision 6. The change uses the shared idempotency mechanism and sets no new numbers.
- Do error answers carry codes? Yes. ADR 276 decisions 1 and 3.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
