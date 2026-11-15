---
version: 7
---

Issue: #1264, #1497.

## Why

**Persona: developer.** A developer manages their own sign-in state through the API: they check what they can do and they make and revoke their own access tokens. Today the preferences routes sit at /api/dustpan/v1/me/preferences, while access, grants and tokens still sit at /api/server/caller/access, /api/server/caller/grants and /api/server/caller/tokens. One caller needs two prefixes, and the API description gate leaves those routes outside the description.

The user path this change must make work:

1. **Docs page.** Read the account docs page. It names the new paths /api/dustpan/v1/me/access and /api/dustpan/v1/me/grants.
2. **API.** List, create, rotate and delete own tokens through /api/dustpan/v1/me/tokens with an access token of the caller.
3. **CLI.** Use the token with the Pulumi CLI and OpenTofu against the server.
4. **Console.** Change the console to call the new paths. The console change merges before the next server tag.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The caller calls an old caller path after the move. | 404 from the router. The server keeps no alias and no redirect. | `curl -H "Authorization: Bearer $TOKEN" https://server/api/dustpan/v1/me/access` |
| The caller calls a me route with no access token. | 401 with a stable error code. | `curl -H "Authorization: Bearer $TOKEN" https://server/api/dustpan/v1/me/access` |
| The caller sends cursor, limit, a bad pageToken or a pageSize above the maximum. | 400 with a stable error code. The message names pageSize and pageToken. | `curl -H "Authorization: Bearer $TOKEN" "https://server/api/dustpan/v1/me/tokens?pageSize=50"` |

Evidence for the claims above:

- The caller routes live in the installation adapter. Proof: path internal/api/server exists; issue 1264 lists GET /api/server/caller/access, GET /api/server/caller/grants and the token routes under /api/server/caller/tokens
- The mount helper for v1 paths is in pkg/dustpan. Proof: code fact: mountV1 at pkg/dustpan/dustpan.go:3636
- The fail-closed test of the not-moved list exists. Proof: main: TestClassCheckFailsClosed at pkg/dustpan/apidesc_test.go:420; path pkg/dustpan/apidesc_classes_test.go exists
- Coverage rows live in e2e/coverage.go. Proof: path e2e/coverage.go exists; issue 1264 asks to move each route with its row
- The caller routes are in the route table of main. Proof: main routes: ANY /api/server/caller/tokens, /api/server/caller/tokens/{}, /api/server/caller/access and /api/server/caller/grants

## What Changes

- GET /api/dustpan/v1/me/access replaces GET /api/server/caller/access
- GET /api/dustpan/v1/me/grants replaces GET /api/server/caller/grants
- GET and POST /api/dustpan/v1/me/tokens replace the token list and create under /api/server/caller/tokens
- DELETE /api/dustpan/v1/me/tokens/{id} and the rotate route replace the old ones
- The old paths stop being served, with no alias
- The moved list routes use pageSize and pageToken, default 50 and maximum 200
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
- const DefaultPageSize = 50 (`pkg/audit/audit.go:144`)
- const MaxPageSize = 200 (`pkg/audit/audit.go:147`)
- The caller token routes /api/server/caller/tokens and /api/server/caller/tokens/{id} are registered (`internal/api/server/tokens.go:181-182`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (extended), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "One prefix for the tool-neutral REST API. Each route of the tool-neutral REST API moves under `/api/dustpan/v1/`."
- Quote: "No path holds the word "server"."
- Effect: The caller routes move under the v1 prefix with no alias. Paths hold no word server. Moved lists use pageSize, pageToken and nextPageToken. The mutating routes accept the Idempotency-Key header as decision 6 says.
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

none

Questions considered and answered:

- Do old paths stay as aliases? No. ADR 210 decision 1: no alias and no redirect, because no tag shipped them.
- Do cursor and limit keep their names? No. ADR 210 decision 10 names pageSize and pageToken.
- Do the moved mutating routes take Idempotency-Key? Yes, by ADR 210 decision 6, through the shared idempotency mechanism.
- Do error answers carry codes? Yes. ADR 276 decisions 1 and 3.
- What are the default and maximum page size? The PM answered: default 50, maximum 200, the convention of main in pkg/audit/audit.go.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
