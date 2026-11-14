Issue: #1264, #1497.

## Why

**Persona: developer.** A developer manages their own sign-in state through the API: they check what they can do and they make and revoke their own access tokens. Today the preferences routes sit at /api/dustpan/v1/me/preferences while access, grants, and tokens still sit at /api/server/caller, so one caller needs two prefixes and the API description gate leaves those routes outside the description.

The user path this change must make work:

1. **Docs page.** Read the account docs page and call GET /api/dustpan/v1/me/access and GET /api/dustpan/v1/me/grants to see own reach.
2. **API.** List, create, rotate, and delete own tokens through /api/dustpan/v1/me/tokens.
3. **CLI.** Use the token with the Pulumi CLI and OpenTofu against the server.
4. **Console.** Use the console account views that read the same /me routes.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Caller calls an old /api/server/caller path after the move. | 404 with a stable catalog code; the message names the matching GET /api/dustpan/v1/me path as the replacement, carried as a declared argument. | `curl -H "Authorization: Bearer $TOKEN" https://server/api/dustpan/v1/me/tokens` |
| Caller calls a /me route with no access token. | 401 with a stable catalog code; the message names sign-in. | `curl https://server/api/dustpan/v1/me/access` |
| Caller sends cursor, limit, bad pageToken, or oversize pageSize. | 400 with a stable catalog code; the message names pageSize and pageToken, carried as declared arguments. | `curl -H "Authorization: Bearer $TOKEN" "https://server/api/dustpan/v1/me/tokens?pageSize=50"` |

Evidence for the claims above:

- Caller routes live today under internal/api/server. Proof: path internal/api/server exists; issue #1264 lists GET /api/server/caller/access, GET /api/server/caller/grants, and GET, POST, DELETE and rotate under /api/server/caller/tokens
- The route table mount helper lives in pkg/dustpan. Proof: path pkg/dustpan exists; rg found mountV1 in pkg/dustpan/dustpan.go:3636
- Coverage rows live in e2e/coverage.go. Proof: path e2e/coverage.go exists; issue #1264 work item says move each route and its tests, with its row in e2e/coverage.go

## What Changes

- GET /api/dustpan/v1/me/access for caller access
- GET /api/dustpan/v1/me/grants for caller grants
- GET /api/dustpan/v1/me/tokens with pageSize, pageToken, and nextPageToken paging
- POST /api/dustpan/v1/me/tokens for token create with Idempotency-Key
- DELETE /api/dustpan/v1/me/tokens/{id} for token delete with Idempotency-Key
- POST /api/dustpan/v1/me/tokens/{id}/rotate for token rotate with Idempotency-Key
- Stop serving the old /api/server/caller paths with no alias and no redirect
- Update API description, e2e coverage rows, docs, and console paths

Out of scope:

- The other 92 routes of the installation adapter listed in #1497 outside /api/dustpan/v1
- Token life cycle, expiry email, and SMTP settings
- Console section lock reasons

## Usability

Where the path still costs the person time or surprises them:

- A script that stored an old /api/server/caller path gets 404 and reads the new /me path from the error text.
- Token list clients change cursor and limit to pageToken and pageSize; a 400 names the new names.
- A client that loses a create answer lists own tokens, deletes the orphan, and creates a new token; the replay never returns the secret again.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide** (owner-tagged: decision 6).

- Decisions that apply: 1 (current), 3 (extended), 6 (current), 7 (current), 8 (current), 10 (current).
- Changed by: ADR 247 extends decision 3; ADR 286 changes. Chain to the end: ADR 247, ADR 286.
- Current rule: this ADR, with ADR 247, 286.
- Quote: "One prefix for the tool-neutral REST API. Each route of the tool-neutral REST API moves under `/api/dustpan/v1/`. This includes the routes that the console uses. The move lands before the first tag. The server keeps no alias and no redirect for an old path of the REST API, because no tag shipped one (ADR 73).  Benchmark: Google AIP-185 puts the major version of an API in its path."
- Quote: "No path holds the word "server"."
- Quote: "**Every mutating route takes an `Idempotency-Key` (owner decision 2026-10-18).** Each route of the tool-neutral API that changes data"
- Quote: "Action names: `<area>:<verb>`."
- Quote: "Inside a major version, a change is additive only: a new route, a new optional request field, a new answer field, a new optional query parameter or a new error code (ADR 182)."
- Quote: "A request takes `pageSize` and `pageToken`."
- Effect: The move places six caller routes under /api/dustpan/v1/me with no alias and no redirect. Paths hold no word server. Each mutating token route takes Idempotency-Key. Each route declares its authorization action with its object. The new v1 routes use the standard paging names from the start. Old query names end with the old paths.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): ADR 210 decision 6 (owner-tagged) requires every mutating route to accept Idempotency-Key, quoted as 'Every mutating route takes an Idempotency-Key'; the POST, DELETE and rotate routes on /api/dustpan/v1/me/tokens are moved with no such requirement in the spec or tasks. Resolution: answered: Added ADR 210 decision 6 to adr_check and added idempotency requirement and task for POST, DELETE and rotate under /me/tokens.
- R1.2 (owner decision conflict): The owner decision of 2026-10-18 (ADR 210 decision 6) requires Idempotency-Key on every mutating tool-neutral route; the move of token create, delete and rotate omits it and neither the spec nor the tasks defers it. Resolution: answered: Same cover as R1.1: spec requires Idempotency-Key on moved mutating token routes.
- R1.3 (missing scenario): The spec has no scenario for rotate: its behavior, the state of the old token after rotation, and its authorization action are not defined, although rotate is moved. Resolution: answered: Added rotate requirement with old token state and authorization action, plus e2e step.
- R1.4 (missing scenario): The 404 answer for an old path has no requirement for the code, the message naming GET /api/dustpan/v1/me/tokens as replacement, although the proposal error table requires it; ADR 276 decision 1 (owner) also requires a stable error code on every 4xx answer. Resolution: answered: Added old-path 404 requirement with stable code and replacement message.
- R1.5 (missing scenario): No scenario covers 401 unauthenticated on the /me routes with the sign-in message, although the proposal error table promises it. Resolution: answered: Added 401 requirement for /me routes with no token.
- R1.6 (missing scenario): No scenario covers an invalid, expired or foreign pageToken, or a pageSize above the maximum on GET /me/tokens. Resolution: answered: Added paging requirement for invalid pageToken and pageSize above maximum.
- R1.7 (open question): The rename of cursor and limit to pageToken and pageSize breaks the live console list with no compatibility or sunset decision; the proposal says Open questions: none, while the owner must accept the break before 1.0. The cited ADR 210 decision 10 only states the paging names, not the migration. Resolution: answered: No open question: ADR 210 decision 1 keeps no alias because no tag shipped the path, and before 1.0 tool-neutral APIs break without deprecation; console change merges before the next server tag.
- R1.8 (wrong claim): The ADR check quotes ADR 210 decision 1 as deciding no aliases 'because no tag shipped an old path', but the decision text cites ADR 73 for that reason and the proposal relies on it without the cited lines; the claim that the move needs no alias is only partly supported by the quote. Resolution: answered: Expanded ADR 210 decision 1 quote to the full decision lines including the no-alias rule.
- R1.9 (failure mode): Design claims a missed mount is impossible because an unmounted path returns 404 in the route test; this catches only a route the test covers, and the not-moved list shrink in task 2.2 is not enforced when an old path stays served. Resolution: answered: Softened design structure claim and added task to fail when an old path stays served or a new path is missing.
- R1.10 (open question): The metrics label for /api/dustpan/v1/me/ is server (issue 1264); the change does not state whether the label changes, which affects dashboards and ADR 210 decision 3 which forbids the word server in paths. Resolution: answered: Added design decision to keep the metrics adapter label server; it is not a path.
- R1.11 (scope): Task 2.3 defers console path confirmation to before the next server tag, yet issue 1264 requires the console change to merge before the tag; the owner of the console confirmation is not named. Resolution: answered: Task now states the console change merges before the next server tag, checked by rg and owner PM review.
- R2.1 (adr conflict): ADR 210 decision 6 (owner decision 2026-10-18) requires Idempotency-Key on every mutating route. The spec only covers Idempotency-Key on POST create. The rotate requirement and the DELETE scenario have no replay rule, and task 3.1 e2e tests only POST with a key. The owner-tagged decision is not fully met. Resolution: answered: Spec now requires Idempotency-Key on POST, DELETE, and rotate, with replay rule on each; e2e covers each mutating route with a key.
- R2.2 (failure mode): A replayed rotate or create with the same Idempotency-Key returns the stored answer. If that answer holds a token secret, the shared idempotency store keeps a usable secret in plain text. The design does not say whether stored answers hold secrets, or how long they live. Resolution: answered: Design states stored idempotency records hold no token secret; replay returns metadata without the secret value within store retention.
- R2.3 (missing scenario): No scenario covers the same Idempotency-Key sent with a different body or a different route, or two concurrent requests with one key. ADR 210 decision 6 defines the key scope as principal, method, path and key, but the spec does not state the conflict answer or the retention time. Resolution: answered: Spec covers same key with different body or route as a conflict with no change, concurrent use of one key, and retention behavior.
- R2.4 (missing scenario): The spec names no authorization action for the /me routes (access, grants, token list, create, delete, rotate). The project rule requires each endpoint to declare its actions, and task 3.2 checks them. Resolution: answered: Spec declares authorization for /me routes as authenticated caller only with no grant check.
- R2.5 (missing scenario): The paging requirement does not state nextPageToken, which ADR 210 decision 10 requires. It also does not say that the last page returns an empty nextPageToken, or what the default and maximum pageSize are. Resolution: answered: Paging requirement now states nextPageToken, empty on last page, with default and maximum declared in the API description.
- R2.6 (missing scenario): Only the old GET access path has a 404 scenario. Old grants, token list, token delete and rotate paths have no replacement-naming 404 scenario, although the design says all six routes move with no alias. Resolution: answered: Spec requires 404 with replacement message for each old caller path, not only GET access.
- R2.7 (open question): The error codes request.not-found and request.unauthenticated are cited as stable, but no catalog entry in internal/errcode is shown. ADR 276 decision 2 requires that each code is declared in the catalog with its arguments, and the proposal does not show that they exist. Resolution: answered: Proposal no longer cites literal codes; it requires stable catalog codes with declared arguments.
- R2.8 (failure mode): The rotate scenario does not cover a crash between issuing the new token and ending the old one, nor a request that uses the old token while rotate is in flight. A restart could leave two valid tokens. Resolution: answered: Rotate requirement states atomic issue and end in one transaction and defines concurrent old-token use.
- R2.9 (wrong claim): The proposal says the console change merges before the next server tag, and the adversarial answer says the rename ships with no alias. The console-facing break for clients that still send cursor and limit is not tested by any task, so the claim that no open question remains is not proven. Resolution: answered: Task checks that cursor and limit give 400 naming pageSize and pageToken on the new paths.
- R2.10 (scope): Task 2.3 confirms console paths only by rg and an owner PM review, with no named owner. The task also covers docs for all routes, but the spec has no requirement for the console route change itself. Resolution: answered: Console and docs task names explicit rg checks and the PM acceptance before the next server tag; no server behavior change for console is specified.
- R3.1 (failure mode): A replayed rotate or DELETE with the same Idempotency-Key, sent after the rotate has ended the token that made the request, gets 401 before the idempotency lookup. The spec does not say that the replay is checked before authentication, so the stored answer cannot be returned. The spec does not cover this case. Resolution: answered: Design now states authentication runs before idempotency lookup and fails closed; spec states a replay with an ended token answers 401 without reaching the stored answer.
- R3.2 (failure mode): A replay of a create returns the status and metadata with no secret value. A client that lost the first answer cannot get its token again. The change does not say how that client recovers, so a retry after a timeout leaves a token that nobody can use. Resolution: answered: Spec states a replay returns metadata with no secret and the client recovers by listing own tokens, deleting the orphan, and creating a new token.
- R3.3 (adr conflict): ADR 276 decision 3 (owner) requires that each answer carries the arguments that its text names. The 404 that names the replacement /api/dustpan/v1/me path and the 400 that names pageSize and pageToken have no declared argument in the spec. The spec also does not name the catalog codes or their arguments. Resolution: answered: Added ADR 276 decision 3 to adr_check; spec requires each named value as a declared argument of its catalog code.
- R3.4 (wrong claim): The proposal says that before 1.0 tool-neutral APIs break without deprecation. The quoted ADR 210 decisions do not say this. Decision 8 says that inside a major version a change is additive only, and that a change that can break a client needs a new major version. Renaming cursor and limit breaks clients inside v1, so the claim that the break is allowed is not supported. Resolution: answered: Removed the claim that tool-neutral APIs break without deprecation before 1.0; the move ends old paths outside v1 per decision 1 and new v1 routes use standard paging names from the start.
- R3.5 (owner decision conflict): The spec declares the /me routes as authenticated caller only, with no grant check. The project rule requires each endpoint to declare its authorization actions and object, and every permission to be a stored grant. The change does not show an owner decision that exempts these routes. Resolution: answered: Design and spec now declare one authorization action with object caller principal on each /me route, with a self-only check and no stored grant.
- R3.6 (open question): The default and maximum pageSize, and the idempotency retention period, are not set. The spec refers to the retention shown in the API and to limits declared in the description, but no value is given. The task that checks paging cannot be written without these values. Resolution: open question: Added open question for default pageSize, maximum pageSize, and idempotency retention with options, benchmark, and PM decider.
- R3.7 (missing scenario): The spec has no scenario for two concurrent requests that use one Idempotency-Key. Task 1.3 names this case, but no spec scenario states the expected answer, so the test has no requirement to check. Resolution: answered: Added concurrent same-key scenario to the mutating token requirement.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the default pageSize, the maximum pageSize, and the idempotency retention period for the moved /me routes?
   - Options: default pageSize 50 with maximum 200 and idempotency retention 24 hours; default pageSize 20 with maximum 100 and idempotency retention 72 hours.
   - Provisional decision (owner review 14 Nov): default pageSize 50 with maximum 200 and idempotency retention 24 hours
   - Reason: The ADRs fix the paging names and the idempotency scope but give no numbers, so the PM sets numbers that match common API practice. Benchmark: Google AIP-158, which uses pageSize with a small default and a capped maximum, and a short idempotency retention window
   - Persona impact: A developer pages token lists with one default and sees the same nextPageToken behavior on each list; no persona loses access. Escape hatch: An operator reads the declared default, maximum, and retention from the API description and the docs page before they script token lists.
   - Owner-tagged ADRs involved: ADR 210 (their words do not change).

Questions considered and answered:

- Do old /api/server/caller paths stay as aliases? ADR 210 decision 1 answers it: each tool-neutral route moves under /api/dustpan/v1 with no alias and no redirect because no tag shipped one. The old caller paths were outside /api/dustpan/v1, so stopping them is not a break of a v1 route.
- Do cursor and limit keep their names on the moved lists? ADR 210 decision 10 answers it: a v1 list takes pageSize and pageToken and returns nextPageToken. The moved lists enter /api/dustpan/v1 with those names from the start. The old cursor and limit names end with the old paths outside v1.
- Do moved mutating token routes accept Idempotency-Key, including rotate and DELETE, and what does replay return? ADR 210 decision 6 answers it: each POST, DELETE, and rotate under /me/tokens accepts Idempotency-Key keyed by principal, method, path, and key. Authentication runs before the idempotency lookup and fails closed, so a replay with an ended token gives 401.
- Do the 404, 401, 400, and conflict answers carry error codes and arguments? ADR 276 decisions 1 and 3 answer it: each 4xx answer carries a stable catalog code, and each value that its text names is a declared argument of that code.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
