## Problem

**Persona: a console or a script that pages the namespace list.** The caller
reads `GET /api/server/namespaces?limit=100` and gets `nextCursor`. The cursor
is the base64url of the name of the last namespace: it holds no seal and no
digest of the query that made it. A caller can send a cursor that another
query made, for example after a grant changed the visible namespaces. The list
answers 200 with a page that skips the rows before that name or repeats rows
that the caller already read. The caller shows a list that looks complete and
is not.

## Evidence

Each line is from the current checkout. Open each file at the line.

- `internal/api/server/namespaces.go:279`: `nextCursor = base64.RawURLEncoding.EncodeToString([]byte(page[len(page)-1].Name.String()))` in `readNamespacePage` (line 265). The cursor is the plain name.
- `internal/api/server/namespaces.go:312-332`: `decodeNamespaceCursor` decodes the base64 and parses a namespace name. It checks no seal and no query.
- `internal/api/server/namespaces.go:255`: `after, first, err := decodeNamespaceCursor(req.Cursor)` in `parseNamespaceList` (line 238). The cursor is read without the filter.
- `internal/api/server/namespaces.go:205`: `filter, empty, err := h.visibleNamespaces(ctx, p)` in `listNamespaces` (line 195). The filter comes from the grants of the caller, and `visibleNamespaces` (line 342) builds it from `h.authz.ListObjects`, so it can change between two pages.
- The helper for a bound cursor exists: `internal/pagetoken/pagetoken.go:19-21` (`MaxLen`), `:28-34` (`Fingerprint`), `:36-41` (`SameFingerprint`), `:43-51` (`Encode`), `:53-80` (`Decode`, with `DisallowUnknownFields`).
- The lists that bind the cursor to the query:
  - `internal/api/inventory/paging.go:40-46` (`pageTokenWire{After, Fingerprint}`), `:75-97` (`queryFingerprint`), `:113-124` (`decodePageToken` checks the fingerprint).
  - `internal/api/inventory/deleted.go:160-202`: the same shape for the deleted units.
  - `internal/api/jobs/list.go:168-178` (`parsePageToken` checks the fingerprint), `:180-184` (`cursorAAD`), `:186-228` (`sealCursor` and `openCursor` seal the cursor with `h.cursors`, a `secret.Wrapper`).
- The lists that do not bind it: `pkg/principal/page.go:120-122`: `func encodeCursor(key string) string { return base64.RawURLEncoding.EncodeToString([]byte(key)) }` (principals, groups, members, tokens). This change does not cover them.
- `internal/api/server/server.go:803` answers `errNamespaceCursor` (line 167) with 400 and the code `errcode.RequestPageTokenInvalid`; the catalog text is `request.page-token-invalid` (`internal/errcode/catalog_request.go:40`).

The report holds on the checkout: a cursor of another query is accepted, and
the page can skip or repeat rows. The report also says that a caller can edit
the cursor to any name. That is true, and the seal below refuses a cursor that
was changed.

## Fix boundary

IN:

- `internal/api/server/namespaces.go`: a `namespaceCursorWire{After, Fingerprint}`;
  `namespaceListFingerprint(filter)` gives `pagetoken.Fingerprint` of the filter
  (`all`, or the sorted names); `listNamespaces` reads the filter before the
  query; `parseNamespaceList` takes the fingerprint; `readNamespacePage` seals
  the cursor with `h.cursors` and an AAD of the fingerprint; the cursor opener
  unwraps, calls `pagetoken.Decode`, and checks `pagetoken.SameFingerprint`. The
  `principal.MaxCursorLength` check goes; `pagetoken.MaxLen` bounds the token.
- `internal/api/server/server.go`: `Config` takes `Cursors secret.Wrapper`, and
  the Handler keeps it.
- `pkg/dustpan/area_installation.go` and `pkg/dustpan/dustpan.go`: the
  installation passes `cfg.secretWrapper` as `Cursors`, as the jobs handler
  gets it.
- `docs/reference/`: a page for the namespace list states that the cursor works
  only with the query that made it, and that another query answers 400 with
  `request.page-token-invalid`.

OUT:

- The cursors of the principal, group, member and token lists
  (`pkg/principal/page.go`): the same class, a separate change.
- The query parameter names `limit` and `cursor`: the style guide names
  `pageSize` and `pageToken`, and the list predates it. A rename touches the
  console and the generated clients, and it is a separate change.
- The `total` field and the page size.
- The cursors of the tool-compatible APIs.

## Check

| Guard | Test | Mutation that fails it |
| --- | --- | --- |
| The cursor is bound to the filter: the fingerprint in the AAD and the check | `TestNamespaceListCursorOfAnotherFilterIsRefused` (a cursor of the administrator query, sent by a caller with one visible namespace, answers 400 `request.page-token-invalid`) | remove the fingerprint from the AAD and the `SameFingerprint` check |
| The seal: the server answers only a cursor that it sealed | `TestNamespaceListCursorWithoutSealIsRefused` (a wire token with a valid fingerprint, from a handler without a sealer, answers 400 at a handler with a sealer) | return the decoded name without `Unwrap` |
| The token is checked at the boundary | `TestNamespaceListBadQuery` gains the cases: not base64url, not JSON, an unknown field, an empty `after`, a name that is not a namespace name, a changed byte, a token above `pagetoken.MaxLen` | remove `pagetoken.Decode` or the name parse |
| The page size stays out of the binding | `TestNamespaceListCursorWithAnotherLimitIsAccepted` | put the limit in the fingerprint |
| The walk stays complete | `TestNamespaceListPagesAll` (each namespace once, the last page has no cursor) | a regression |

The fingerprint is a second layer: the AAD of the seal already refuses a
cursor of another query. The check stays, with a defense-in-depth comment that
names the seal.

## ADR check

**ADR 210 decision 10 (not owner-tagged; decision 6 is owner).** "**Lists** page
with an opaque cursor. A request takes `pageSize` and `pageToken`. An answer
holds the items and `nextPageToken`, which is empty on the last page. Each list
route declares its default and its largest page size. A list parameter of an
earlier ADR, such as `after`, follows this rule." Changed by: ADR 247 extends
decision 3; ADR 286 changes. Current rule: this ADR with ADR 247 and 286.
Decision 10 stands. The list keeps its `limit` and `cursor` parameters, and
the cursor becomes opaque. No conflict.

**ADR 166 decision 3 (not owner-tagged).** "The parameter `after` of `ByOwner`
becomes an opaque cursor that the store gives with each page, in place of
`Ref.Key`. `Ref.Key` holds the names of a namespace and a collection, which a
rename changes. So a page cursor of names could skip or repeat rows." Changed
by: ADR 210 changes (the decisions are not named); ADR 286 changes. Current
rule: this ADR with ADR 210 and ADR 286. The namespace list has the same
shape: a cursor of names. The fix gives it the opaque, bound cursor. No
conflict.

**ADR 276 decision 1 (owner).** "Every error answer has a code (owner). Each
4xx and 5xx answer of each HTTP adapter has a stable machine code." The refusal
keeps the code `request.page-token-invalid` of the catalog. No conflict.

**ADR 182 decision 1 (owner).** "A stable machine code for each error (owner).
Each error that an API answers has a machine code. A code is a contract: a code
that a tag shipped is never removed or given another meaning. A new kind of
error gets a new code." Changed by: ADR 210, 234 and 276. Current rule: this
ADR with those. The fix adds no code and changes none. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none
