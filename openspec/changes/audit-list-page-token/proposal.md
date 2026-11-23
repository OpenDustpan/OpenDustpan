---
version: 1
---

Issue: #3683. This change builds ADR 210 decision 10 for the audit list.

## Why

**Persona: site administrator, and an agent that reads the audit log of a namespace or of the installation.** The person lists the entries of a large log through `GET /api/audit/entries`, newest first, page after page. The agent does the same to find the entries of one incident.

The path today:

1. The caller sends `GET /api/audit/entries?organization=acme&page[number]=N&page[size]=S` with an access token.
2. The server runs `COUNT(*)` of the namespace and then `LIMIT S OFFSET (N-1)*S` on each page. Both read each row that they pass. The issue measures 203 ms at offset 900000 with 1,000,000 entries. The cost grows with the page number, and the count repeats on each page.
3. The answer is `{"entries": [...], "total": T}`. The caller needs the total only to know when to stop.

The path after this change:

1. The caller sends `GET /api/audit/entries?organization=acme&pageSize=S`. The first page needs no token.
2. The answer is `{"entries": [...], "nextPageToken": "...", "pageSize": S}`. The token is opaque. It is empty on the last page.
3. The caller sends the token as `pageToken=...` with the same `organization`. The server reads the next entries with a keyset query (`sequence` below the last sequence of the page), so each page costs the same at any depth. The server counts nothing.

Errors the person may hit, and the one command that helps:

- `pageSize` is not a number from 1 to 200: `400` `request.page-size-invalid`. The text says to send a number from 1 to 200, or no `pageSize` for 50.
- `pageToken` does not decode, is too long, or comes from another scope: `400` `request.page-token-invalid`. The text says to send the request again without `pageToken`.
- The request still has `page[number]` or `page[size]`: `400` `request.invalid-query`. The text names the two new query values. Without this refusal the server would drop the old values and answer page 1 for each request, and a script would loop on page 1 with no error.
- A caller that may not read the audit log of the scope: `403`, as today. The route checks the action before it reads the token, so a caller who may not read learns nothing about a token.

What the person loses: the total count and the jump to page N. Both need a count or an offset, and both are the cost that the issue measures. A reader that wants to jump uses `GET /api/audit/entries/{sequence}` and a token is not needed for that; a reader that wants a count of entries uses the chain check (`GET /api/audit/check` answers `entriesChecked`).

## Evidence

- The cost is the count and the offset. Prototype run on this repository's layout (not the Postgres run of the issue): SQLite 3, table `audit_entries(namespace, sequence, subject, at_ms, hash, PRIMARY KEY (namespace, sequence))`, 1,000,000 rows of one namespace, one cold run each. `COUNT(*)` of the namespace: 29.4 ms. `ORDER BY sequence DESC LIMIT 50 OFFSET 900000`: 30.0 ms. `WHERE sequence < 100000 ORDER BY sequence DESC LIMIT 51`: 1.3 ms. The same without a `WHERE` on sequence: 0.07 ms. The rows are narrower than the real table, so the times are lower bounds for the first two. The keyset read uses the primary key `(namespace, sequence)` of each dialect (`internal/store/postgres/migrations/0168_baseline.sql:1687`, `internal/store/sqlite/migrations/0168_baseline.sql:265`) and needs no new index and no schema edit. Task 1.9 puts the plan of the query in a test.
- The audit list is the only list that still uses `page[number]` among the tool-neutral routes. Run on main (f7a35d59e, 19 Nov 2026): `rg -n "page\[(number|size)\]" internal/api --glob '!*_test.go' --glob '!internal/api/tfe/**'` prints only the lines of `internal/api/audit/audit.go` (417 to 475). The TFE-compatible API uses `page[number]` because its tool does, and it stays as it is.
- No other client reads the list. `rg -n "api/audit|/audit" /home/user/console/src` prints nothing (the console repository at its main `06e3b79`). `rg -n "audit/entries" internal/cli internal/clientgen cmd clients load` prints nothing. The generated description has no route of the list: `rg -n '"/api/audit/entries' internal/api/apidesc/openapi.json` prints nothing, because `Operations` in `internal/api/audit/operations.go:27` describes only the routes under `apidesc.Prefix`. The callers are the e2e suite, tests and the docs page (see "Files").

## What main already does

Each fact names its file, line and function on main (f7a35d59e). The change specifies only the difference.

- `listEntries` answers `GET /api/audit/entries` after the route wrapper allowed the action, resolves the scope, reads the page and answers entries and a total (`internal/api/audit/audit.go:307 (func listEntries)`)
- `auditScope` resolves the scope from the path object (`internal/api/audit/audit.go:290 (func auditScope)`)
- `listEntriesResponse` holds `Entries` and `Total` (`internal/api/audit/audit.go:270 (type listEntriesResponse)`)
- `parsePage` reads `page[number]` and `page[size]` with `strconv.Atoi`; a value below 1 answers `errInvalidPage`, missing values read as page 1 and the default size, a size above the maximum is cut to it, and any other query value is ignored (`internal/api/audit/audit.go:454 (func parsePage)`)
- `requestErrors` maps `errInvalidPage` to `400` `request.invalid-query` (`internal/api/audit/audit.go:488 (func requestErrors)`)
- The default page size is 50 (`pkg/audit/audit.go:157 (const DefaultPageSize)`)
- The largest page size is 200 (`pkg/audit/audit.go:160 (const MaxPageSize)`)
- `Page{Number, Size}` has `Normalized` and `Offset` (`pkg/audit/audit.go:717 (type Page)`)
- `Listing{Entries, Total}` carries the total (`pkg/audit/audit.go:749 (type Listing)`)
- `Store.List` promises the total count in its comment (`pkg/audit/audit.go:775 (type Store)`)
- The Postgres store counts and lists in one snapshot transaction (`inSnapshotTx`), so the page and the total agree (`internal/store/postgres/audit.go:580 (func List)`)
- The SQLite store runs `CountAuditEntries` and then `ListAuditEntries` as two statements with no shared transaction; this corrects the premise of the issue, because only Postgres reads both in one snapshot (`internal/store/sqlite/audit.go:393 (func List)`)
- `ListAuditEntries` is `WHERE namespace = $1 ORDER BY sequence DESC LIMIT $2 OFFSET $3` in Postgres (`internal/store/postgres/queries.sql:1469`)
- `CountAuditEntries` is `SELECT COUNT(*) ... WHERE namespace = $1` in Postgres (`internal/store/postgres/queries.sql:1480`)
- `ListAuditEntries` is the same offset query in SQLite (`internal/store/sqlite/queries.sql:1267`)
- `CountAuditEntries` is the same count query in SQLite (`internal/store/sqlite/queries.sql:1278`)
- `ListAuditChainPage` is a keyset query but reads oldest first: `WHERE namespace = $1 AND sequence > $2 ORDER BY sequence ASC LIMIT $3`; this corrects the premise, because the list needs a new query with `<` and `DESC` (`internal/store/postgres/queries.sql:1485`)
- The SQLite copy of `ListAuditChainPage` is ascending too (`internal/store/sqlite/queries.sql:1283`)
- The sink feed uses the ascending chain query (`internal/store/postgres/audit_feed.go:21 (func After)`)
- `Check` treats a gap in the sequence as a broken chain, so the list can meet a gap and must not assume none (`pkg/audit/audit.go:762 (type CheckResult)`)
- The shared page-token module encodes with `Encode` (`internal/pagetoken/pagetoken.go:44 (func Encode)`)
- `Decode` refuses a token over 4096 bytes, unknown members and trailing JSON (`internal/pagetoken/pagetoken.go:58 (func Decode)`)
- `Fingerprint` and `SameFingerprint` bind a token to a query (`internal/pagetoken/pagetoken.go:30 (func Fingerprint)`)
- The unit list uses the module with a wire struct and a fingerprint of the query (`internal/api/inventory/paging.go:43 (type pageTokenWire)`)
- The key `(namespace, sequence)` is the primary key of `audit_entries` in both dialects, and the scope `@server` is a value of `namespace` in the same table (`internal/store/postgres/migrations/0168_baseline.sql:1687`)

## Fix boundary

This change makes the audit list follow ADR 210 decision 10. It touches:

- The port `audit.Store.List` and the types `Page` and `Listing` in `pkg/audit/audit.go`: `Page{Size, Before}`, `Listing{Entries, More}`, a new sentinel `ErrInvalidPage`. `Number`, `Offset` and `Total` go. No count, no offset.
- The two store adapters (`internal/store/postgres/audit.go`, `internal/store/sqlite/audit.go`) and their queries: `ListAuditEntries` becomes a keyset query, and `CountAuditEntries` is removed. The generated query code is regenerated.
- The handler `internal/api/audit/audit.go`: `parsePage`, the token, the answer, the error mapping. The Contract comment on `listEntriesResponse` ("never remove or rename these fields") is changed: the console does not read this route (see "Evidence").
- The conformance suite `pkg/audit/audittest` and every caller of `Page` and `Total` (see "Files" and the tasks).
- The docs page `docs/guides/audit-log.md`, the error-code page, the unreleased release notes, and the e2e helpers.

- New names that this change adds: NEW `audit.ErrInvalidPage`, NEW `ListAuditEntriesBefore`, NEW `NextPageToken`, NEW `TestListEntriesHostileQueryRows`, NEW `TestPageTokenHostileRows`, NEW file `internal/api/audit/pagetoken_test.go`, NEW `Page.Before`, NEW `Listing.More`.

Not decided here, behavior stays as today:

- The route path. ADR 210 decision 3 moves it under the tool-neutral prefix. The route stays at `/api/audit/entries` (class "not moved" in `pkg/dustpan/apidesc_test.go:581`). The move is its own change and does not need this one.
- The filters of ADR 60 decision 8 (`subject`, `action`, `kind`, `object`, `outcome`, `from`, `to`). The list has none today. A later filter adds its values to the fingerprint of the token (design, decision 4) and is not specified here.
- The query times in Unix milliseconds that ADR 210 decision 10 turns into RFC 3339 strings. The list has no time value.
- The export route (`GET /api/audit/export`) and the chain check. They read by sequence already and do not change. (read only) `internal/api/audit/export.go`.
- The audit entries, their fields and the hash chain. The change adds no field to an entry and does not change a hash. No schema edit, no new index.
- The other lists with an offset (`ListUpdates`, state versions) and the tool-compatible routes.
- `ListAuditChainPage` and the sink feed.
- The console. It does not call this route.

Out of scope: a stored counter for an exact total. It adds a write to the append path of each entry, a schema edit and a counter that can drift from the rows. A bound such as "sequence <= max - offset" is exact only when the sequences have no gap, and the chain check reports a gap as a finding, so the log may hold one. Neither gives a fast list with the old contract, and the contract of ADR 210 does not ask for a total.

## Files

New: none in production code. Edited:

- pkg/audit/audit.go: `Page`, `Listing`, `ErrInvalidPage`, the comment of `Store.List`, the pure check `Page.Normalized`.
- pkg/audit/audit_test.go: the test of `Normalized` (lines 404 and 408) and the new table.
- pkg/audit/audittest/audittest.go, pkg/audit/audittest/chain.go, pkg/audit/audittest/entries.go: the paging cases, the new hostile table, a helper `All` that reads every entry of a scope, and the delete hook for a gap.
- internal/store/postgres/audit.go, internal/store/sqlite/audit.go, internal/store/postgres/queries.sql, internal/store/sqlite/queries.sql: the keyset read. The generated code in `internal/store/postgres/internal/db` and `internal/store/sqlite/internal/db` is regenerated, never edited.
- internal/store/postgres/audit_test.go, internal/store/sqlite/audit_test.go (or the file that runs `audittest` in each adapter): the plan test.
- internal/api/audit/audit.go, internal/api/audit/audit_test.go: the handler and its tests, with a new file internal/api/audit/pagetoken.go and internal/api/audit/pagetoken_test.go for the wire struct of the token.
- The 127 files that use `audit.Page{` and the files that read `.Total` of an audit listing: the list in task 2.7.
- pkg/dustpan/denial_audit_test.go:40, pkg/dustpan/state_refusal_audit_test.go:111: the query `page[size]=100` becomes `pageSize=100`.
- e2e/e2e_test.go (`fetchAuditEntries`, line 2960), e2e/signin_test.go (`checkProviderAuditHoldsNoSecret`, line 750): the loops use the token.
- e2e/coverage_audit.go: the reason text of the allowlist row of the list names no console (the console does not read this route).
- docs/guides/audit-log.md ("Read the entries", line 408), docs/reference/tool-neutral-error-codes.md ("Audit API", line 107), docs/release-notes/unreleased.md.

Console: no change. Run: `rg -n "api/audit|/audit/entries|audit/entries" /home/user/console/src /home/user/console/e2e` prints nothing on the console main `06e3b79`. The server change does not break the console, so ADR 73 decision 3 needs no matching console pull request. Task 3.2 reruns this command on the day of the merge.

## Check

- Test the scenarios of the delta spec with the names of the tasks in group 1. Each fails on main: the list has no `pageSize`, no `pageToken` and no `nextPageToken`, and still has `total`.
- Test the three layers with one hostile table each: `TestListHostilePageRows` (store, in `pkg/audit/audittest`), `TestPageNormalizedTable` (domain, in `pkg/audit`) and `TestListEntriesHostileQueryRows` with `TestPageTokenHostileRows` (handler). Each guard shows a test that fails when the guard is removed (task 1.8).
- Test the plan: the keyset query reads the primary key backwards with no sort and no count. Fails on main (the query is an offset query).
- E2E: `make e2e` pages the log of the harness with the token (task 1.10).
- Docs: `make docs-lint`, and the command of the guide runs as written in the e2e step.

## ADR check

Each quote is copied from the file in `docs/adr/`. The "Changed by" chains come from `docs/adr/INDEX.md`. No quoted decision is owner-tagged unless this section says so.

**ADR 210: The tool-neutral API: one prefix, resource paths, action names, versions and a style guide.**

- Decision that applies: 10. Quote: "**Lists** page with an opaque cursor. A request takes `pageSize` and `pageToken`. An answer holds the items and `nextPageToken`, which is empty on the last page. Each list route declares its default and its largest page size. A list parameter of an earlier ADR, such as `after`, follows this rule."
- Owner tag: no. The ADR says "Decided by the project manager under the owner's decision rule, 2026-10-18" and marks only decision 6 as an owner decision ("Decision 6 is an owner decision of 2026-10-18"). Decision 10 is not owner-tagged. The Context records that the project owner asked for shapes "as ergonomic and standard compared with industry as possible".
- Changed by: ADR 247 (extends decision 3, the unit list filters) and ADR 286 (the words catalog and promotion). Neither touches decision 10. Current rule: "this ADR, with ADR 247, 286".
- Effect: the audit list is a list route. It takes `pageSize` and `pageToken`, answers `nextPageToken`, and declares its default (50) and its largest (200) page size. Today it deviates. Conflict: none.
- Decision 8 of the same ADR: "Before 1.0, ADR 73 applies: `v1` can change and break, and the release notes list each break." Effect: the change adds an entry to `docs/release-notes/unreleased.md`.
- Decision 3 (the move table) moves the path, and this change keeps the path (see "Fix boundary").

**ADR 60: The audit log is a port, and logs go to swappable sinks.**

- Decision that applies: 8. Quote: "`GET /api/audit/entries` lists entries newest first, as today. It gets filters: `subject`, `action`, `kind`, `object`, `outcome` and a time range (`from` and `to` in Unix milliseconds). It also gets a cursor by sequence, so a reader can page a large log without an offset. The page number and size of ADR 41 stay, so no client breaks."
- Owner tag: no. The ADR records answers of the owner to RFC 22 in its Context but does not tag decision 8.
- Changed by: ADR 63, 64, 65, 68, 72, 126, 151, 210, 240, 334. ADR 210 "changes decision 8: route paths, action names, query times and paging." Current rule: "this ADR, with ADR 63, 64, 65, 68, 72, 126, 151, 210, 240, 334".
- Effect: the "cursor by sequence" of decision 8 is the aim of this change, and ADR 210 replaces the last sentence ("The page number and size of ADR 41 stay, so no client breaks") for paging. The change does not build the filters. Conflict: none.

**ADR 41: The server reads state and masks secrets.** Decision 13 built the list route with a page number and a size ("a REST route to list them"). Changed by: ADR 60 (decisions 5 and 13), 103, 146, 151, 238, 240, 352. ADR 60 decision 8 and then ADR 210 decision 10 set the paging. Current rule: "this ADR, with ADR 60, 103, 146, 151, 238, 240, 352". Conflict: none.

**ADR 73: Until 1.0, a change can break everything except the tool-compatible APIs** (owner decision: "The project owner decided that a change can break any part of the project until 1.0").

- Quote, decision 1: "**Until 1.0, a change can break what is ours.** These can change in a way that breaks a caller, with no deprecation period: - the tool-neutral APIs, for example the inventory API and the installation API".
- Quote, decision 4: "**No compatibility code for unreleased shapes.** Do not write a shim, an alias, a second read path or a data conversion for a shape that no tag shipped."
- Quote, decision 3: "**The console change merges before the next server tag.** A server change that breaks an API that the console calls has a matching console change."
- Changed by: ADR 322 (extends decision 4; changes decision 6). Current rule: "this ADR, with ADR 322".
- Effect: the audit list is a tool-neutral API, so `page[number]`, `page[size]` and `total` go with no alias and no second read path. The refusal of the old query values is not an alias: it answers an error and reads nothing. The console does not call the route, so decision 3 needs no console pull request. Conflict: none.

**ADR 250: Defense in depth for security code** (owner decision, 2026-10-19).

- Quote, decision 1: "**Never only one layer.** Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Effect: the audit list is security-adjacent, not security semantics: the change does not change who may read an entry. The token is input from outside the process, so the layers and their hostile tables are in design decision 5 and in tasks 1.6 to 1.8. Conflict: none.

**ADR 182: Stable error codes for API errors** (INDEX: changed by none; "this ADR, with ADR 210, 234, 276"). The change uses two codes that exist and adds none. Conflict: none.

**ADR 322: one baseline migration for each dialect, edited in place until the first tag.** The change edits no schema. The keyset query uses the primary key. Conflict: none.

**ADR 362: stable IDs for stored references** (owner-tagged). The token holds a sequence, a number of the log, and a fingerprint of the scope. It holds no namespace name as a reference to an entity. The namespace of a request comes from the path of the request, never from the token. Conflict: none.

**ADR 65: reading the installation audit log is an operator action** (changed by ADR 210, 216). The action on each object does not change: `namespace:read-audit` and `server:read-audit`. Conflict: none.

RFC 99 (the API description and the generated SDKs) uses the same three names for a list operation: "`pageSize` and `pageToken` in the query, and `nextPageToken` in the answer". The route is not in the description yet, so no description row changes. RFC 99 is an open question, not a decision.

## Open questions

none

Questions considered and answered:

- Can the old contract be made fast? No. An exact total needs a stored counter (an extra write on the append path of each entry, a schema edit in each baseline and a number that can drift from the rows). "sequence <= max - offset" is exact only for gapless sequences, and `Check` reports a gap as a finding. A page number without a total still needs an offset or the gap rule. ADR 210 decision 10 gives the answer that the project already chose, so the change follows it. Decided by the PM under rule 16, benchmark: Google AIP-158 ("page_token", no total required; `total_size` optional) and the same rule in the unit list on main.
- Does the answer keep a total? No. AIP-158 makes the total optional and the cost of it is the cost that the issue measures. A caller that needs a count uses the chain check, which answers `entriesChecked` and reads the chain once.
- What does the token hold? A sequence and a fingerprint of the scope (`pagetoken.Fingerprint("audit\x00" + scope)`), as base64url JSON through `internal/pagetoken`. The sequence is the last sequence of the page, so the next page reads `sequence < that`. The fingerprint is not a secret and not a signature: anyone can make a token. It is a check that a token of `@server` is not sent for `acme`, so a script that mixes the two gets `400` and not a silent wrong page. The security of the route does not rest on it (see the next question).
- What stops a forged or foreign token from reading what the caller may not? Four layers, each alone: the route wrapper checks the action on the object of the request before the handler reads the token. The handler takes the scope from the path and never from the token. The store query holds `WHERE namespace = $1`, so a sequence of another scope reads no row of it. The result of the query is cut to the page size. A forged token can only choose where in the readable scope the page starts. A caller with the read action can read each entry of the scope by `GET /api/audit/entries/{sequence}` already, so the token gives no new reach.
- Why not sign the token with a server key? A key adds a secret, a rotation and a failure mode (a restart that changes the key breaks open tokens) for a token that gives no reach. ADR 250 asks for layers that each stop the attack. The layers above do, with no key.
- A token from a time before an entry was appended: do new entries shift later pages? No. A new entry has a higher sequence than each entry that the reader has seen, and the next page reads below the last sequence. A reader never sees an entry twice and never misses one that was in the log when the first page was read. A test checks it (task 1.4).
- What if the sequences have a gap? The query reads the next rows below the token, whatever the gap. It never computes a sequence from a page number. A test deletes a row behind the back of the store and checks the pages (task 1.4).
- Is the last page found without an extra read? The store asks for `size + 1` rows. If it gets the extra row, it drops it and sets `More`, and the handler makes the token from the last kept entry. A log of exactly `size` entries answers one page with an empty `nextPageToken`, and not an empty second page.
- Does a size above 200 get cut or refused? Refused, with `400` `request.page-size-invalid`. Step 1 of the defense-in-depth rules says to reject an input that is not valid and not to change it. Today the handler cuts it. The domain check (`Page.Normalized`) refuses too, as a second layer. The size 0 in `Page` means the default, so the 157 test literals with a size keep their meaning.
- A repeated `pageSize` or `pageToken` (two values of the same name)? `400` with the code of the parameter. One query has one value.
- Are `pageSize=050`, `+5` or ` 5` accepted? No. The size is a decimal number with no sign, no leading zero and no space, as `parseSequence` in the same file reads a sequence, so each page has one URL.
- Does the answer show the effective size? Yes, the field `pageSize` holds the size that the server used. The rule of the project says that each default shows its effective value in the API. The default and the largest size are on the guide page.
- Which order do the checks run in? Route wrapper (authentication and the action on the object), then the namespace lookup (`404` for an unknown namespace, as today), then the query values (`400`), then the store. A caller who may not read gets `403` and never learns whether a token is valid.
- Is the change a security change that needs the rev2 review? The audit list is security-adjacent and the token is untrusted input, so the design applies ADR 250 and the pull request gets the Opus security review (rev2) on its exact head as a precaution. No decision about who may read changes.
- Does the change need an ADR? No. ADR 210 decision 10 and ADR 60 decision 8 (as changed by ADR 210) already decide it. The change builds a decision.
