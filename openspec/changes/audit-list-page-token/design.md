## Context

ADR 210 decision 10 says that a list pages with `pageSize`, `pageToken` and `nextPageToken`. The audit list pages with `page[number]`, `page[size]` and `total`, and each page runs a count and an offset. This design gives the structure that moves the list to the decision, and keeps the read of the log on the primary key.

The persona is a site administrator or an agent that reads a large log newest first. The cost of a page must not depend on its depth, and a token must give no reach that the caller does not have.

## Decisions

1. **The port reads by sequence, not by number.** `audit.Page` holds `Size` and `Before`. `Before` is the sequence that the page starts below: 0 means the newest entry. `audit.Listing` holds the entries and `More`. The type has no count and no offset, so no caller can ask for either. This is the structure that makes the bug class impossible: a store method that takes no offset cannot run an offset query, and a listing with no total cannot cost a count. A custom analyzer or a new lint rule is not needed.
2. **One keyset query for each dialect.** `ListAuditEntriesBefore` replaces `ListAuditEntries` in place in `queries.sql` and reads `WHERE namespace = $1 AND sequence < $2 ORDER BY sequence DESC LIMIT $3`. The store maps `Before == 0` to the largest `int64`, so the first page and the next pages use one query with no `OR`. The query reads the primary key `(namespace, sequence)` backwards, so the planner needs no sort and no new index. `CountAuditEntries` is removed. The Postgres adapter no longer needs `inSnapshotTx` for the list, because the page is one statement. Why a `LIMIT` of `size + 1`: the extra row tells the store whether another page exists, so the last page needs no second read and a log of exactly `size` entries has no empty page after it.
3. **The domain checks the page, and the store checks it again.** `Page.Normalized` returns the page with the default size filled (size 0 reads `DefaultPageSize`) and refuses a negative `Before`, a negative size and a size above `MaxPageSize` with `ErrInvalidPage`. Both adapters call it first, then the query. Exported fields stay on `Page` (and not an unexported type with a constructor) because 157 test literals build `Page{Size: X}`; the check is in the one function that both adapters call, so no adapter can skip it by mistake, and a conformance case checks each adapter.
4. **The token is the last sequence and a fingerprint of the scope.** The wire struct `pageTokenWire{Before int64 "b"; Fingerprint string "f"}` lives in `internal/api/audit/pagetoken.go` and uses `internal/pagetoken` (base64url JSON, 4096 bytes at most, no unknown member, no trailing JSON). The fingerprint is `pagetoken.Fingerprint("audit\x00" + scope)`, compared with `pagetoken.SameFingerprint`. The decoder returns the unexported type `pageCursor` only after it checked: the token decodes, `Before >= 1`, and the fingerprint is the fingerprint of the scope of this request. A value of `pageCursor` cannot hold a sequence below 1 or a scope that is not the request's scope, so the handler never passes an unchecked value to the store. The fingerprint is a consistency check and not a signature (see "Layers"). A later filter adds its values to the fingerprint input, so a token works only for the query that made it, as the unit list does.
5. **Layers.** Each layer stops the attack alone. None relies on another.

   | Layer | What it checks | Test (hostile table) |
   | --- | --- | --- |
   | Route wrapper (existing) | Authentication, and the action `namespace:read-audit` or `server:read-audit` on the object of `organization`. It runs before the handler reads any page value. | The existing route tests (`TestRoutesDeclareActions`, `TestRoutesAuthorizeDeclaredObject`) and the new row "denied caller with a valid token answers 403" |
   | Handler | Query values: allowlist of the names, one value for each name, `pageSize` as a decimal number from 1 to 200 with no sign, no leading zero and no space, `pageToken` through the decoder of decision 4, the removed names refused. The scope comes from the path object, never from the token. | `TestListEntriesHostileQueryRows`, `TestPageTokenHostileRows` |
   | Domain (`audit.Page.Normalized`) | A negative `Before`, a negative size and a size above the largest. Fail closed with `ErrInvalidPage`. | `TestPageNormalizedTable` |
   | Store adapter (SQL) | `WHERE namespace = $1` binds the scope as a placeholder, so a `Before` that belongs to another scope reads no row of it. `LIMIT` binds the size plus one. The extra row never reaches the caller. | `TestListHostilePageRows` in `audittest`, run by SQLite and Postgres |

   There is no service package between the handler and the store: `internal/api/audit` calls the port directly, as it does today. The domain function takes the place of the service layer. A new `auditsvc` package for one check is not worth it, and the project rule is to prefer the structure (decisions 1 and 4) over an added layer.

   The redundant checks keep a comment. The size check in the domain and in the store call says that it is a defense-in-depth layer, and that the handler validates the size too. The comment names no ADR.
6. **No count of any kind.** The answer has no `total`. The change adds no counter table and no schema edit (ADR 322 keeps the baseline as it is). A reader that needs the number of entries uses the chain check.
7. **The answer.** `{"entries": [...], "nextPageToken": "<token or empty string>", "pageSize": N}`. `nextPageToken` is always present and is the empty string on the last page, as ADR 210 decision 10 says. `pageSize` is the size the server used (the effective default or the request).
8. **The removed query values are refused, not read.** `page[number]` and `page[size]` answer `400` `request.invalid-query` with the text "page[number] and page[size] are not supported: send pageSize and pageToken". The refusal reads nothing and calls no store. It is the one place that names the old values, so a script written for the old contract fails at once and does not loop on page 1. It is not a compatibility path, and a test checks that no store call happens.
9. **The test helpers.** `audittest.All(t, store, scope)` returns every entry of a scope, newest first, by following `More` with the sequence of the last entry. The 19 callers that loop on a page variable and the callers that read `Total` as a count call it. The 157 literals `audit.Page{Number: 1, Size: X}` are rewritten with `gofmt -r` first (see "Rewrite").
10. **A gap in the sequence is not an error.** The query reads the next rows below the token. The change has no code that computes a sequence from a size. The conformance suite gets a hook to delete a row behind the back of the store (`DeleteEntry` on `ChainTestStore`, like `TamperEntry`), and a case that pages over the gap.

## Rewrite

157 literals of the form `audit.Page{Number: 1, Size: X}` and 3 of the form `audit.Page{Number: 0, Size: 0}` change by one rule, applied with the standard tool, in one commit with no other change:

```
gofmt -r 'audit.Page{Number: 1, Size: a} -> audit.Page{Size: a}' -w <files>
gofmt -r 'audit.Page{Number: 0, Size: 0} -> audit.Page{}' -w <files>
```

The files are those of `rg -l "audit\.Page\{" --glob '*.go' .`. The literals with a loop variable or a page number other than 1 (19 loops and the 2 fixed pages of `audittest`, listed in task 2.7) and the reads of `Total` do not match the rule and fail to compile, so the compiler lists the rest. Each of them calls `audittest.All` or follows `More`. No custom tool.

## Contract

### The port, `pkg/audit`

```go
// Page selects one page of a list, newest first. Before is the sequence that
// the page starts below, and 0 starts at the newest entry. Size is the
// number of entries, and 0 reads DefaultPageSize.
type Page struct {
	Size   int
	Before int64
}

// Normalized returns the page with the default size, or ErrInvalidPage for
// a negative Before, a negative Size or a Size above MaxPageSize.
func (p Page) Normalized() (Page, error)

// Listing is one page: the entries newest first, and More, true when entries
// exist below the last one.
type Listing struct {
	Entries []Entry
	More    bool
}

// ErrInvalidPage reports a Page that Normalized refuses.
var ErrInvalidPage = errors.New("invalid audit page")

// Store.List answers one page of the entries of ns, newest first. A
// namespace with no entries reads as an empty listing. ns can be
// ServerScope. It returns ErrInvalidPage for a page that Normalized refuses.
List(ctx context.Context, ns namespace.Name, page Page) (Listing, error)
```

`DefaultPageSize` (50) and `MaxPageSize` (200) stay at `pkg/audit/audit.go:157` and `:160`. `Page.Offset` and `Listing.Total` are removed. The conformance suite requires `ErrInvalidPage` through `errors.Is` for each refused page, in `audittest` (task 1.5). The sentinel is at the line that task 2.1 gives in its own pull request, and the tick of that task records it.

### Stored schema

No change. Query, in each dialect (`$` for Postgres, `?` for SQLite):

```sql
-- name: ListAuditEntriesBefore :many
SELECT <the columns of ListAuditEntries>
FROM audit_entries
WHERE namespace = $1 AND sequence < $2
ORDER BY sequence DESC
LIMIT $3;
```

`CountAuditEntries` is removed. Index: the primary key `(namespace, sequence)` (`internal/store/postgres/migrations/0168_baseline.sql:1687`, `internal/store/sqlite/migrations/0168_baseline.sql:265`).

### The endpoint

`GET /api/audit/entries?organization=<namespace or @server>&pageSize=<n>&pageToken=<token>`

- Authorization: `namespace:read-audit` on the namespace, or `server:read-audit` on `@server` (unchanged, `internal/api/audit/routes.go:49`).
- `pageSize`: optional, a decimal number from 1 to 200, default 50.
- `pageToken`: optional, the `nextPageToken` of the page before, for the same `organization`.
- Answer, status `200`:

```json
{
  "entries": [{"sequence": 912, "...": "the fields of an entry, unchanged"}],
  "nextPageToken": "eyJiIjo4NjMsImYiOiIuLi4ifQ",
  "pageSize": 50
}
```

- Last page: `"nextPageToken": ""`.
- ETag: none, as today. The list is a read of a log that grows.
- Paging: keyset by sequence, newest first. The first page holds the newest entries.

| Status | `errorCode` | Cause |
| --- | --- | --- |
| 400 | `request.invalid-query` | `organization` is missing, a value is repeated, `page[number]` or `page[size]` is present (removed names) |
| 400 | `request.page-size-invalid` | `pageSize` is not a decimal number from 1 to 200 |
| 400 | `request.page-token-invalid` | `pageToken` does not decode, is over 4096 bytes, holds a member that the wire struct does not have, holds a sequence below 1, or has the fingerprint of another scope |
| 403 | as today | the caller may not read the audit log of the scope |
| 404 | `namespace.not-found` | no namespace with this name |

Each error text says what failed and the one command that helps: "pageToken is not valid for this scope: send the request again without pageToken" and "pageSize is a number from 1 to 200: send a number in this range, or send no pageSize for 50". The codes `request.page-size-invalid` and `request.page-token-invalid` exist in `internal/errcode/catalog_request.go:38` and `:40`, and the change adds no code, so the catalog test data does not change. The error for the removed names uses `request.invalid-query` (`internal/api/audit/audit.go:488 (requestErrors)`).

The handler errors are `problem.BadRequest` values of the package (like `internal/api/notifications/subscriptions.go:450`), and `requestErrors` maps the repeated and removed values to `request.invalid-query`. The conformance suite requires only the port sentinel.

### CLI output

None. No command lists the entries.

## Risks

- A client that follows the old contract gets `400` `request.invalid-query` at once. The release notes list the break. No client of a tagged release exists.
- A token that a script keeps for a long time still works if the log keeps the sequence. A token holds no time, so it does not expire. Retention that deletes old entries (ADR 68 sets forever as the default) leaves a token that reads the next rows below it, which is correct.
- The fingerprint is public. A caller can forge a token for any sequence of a scope that the caller may read. This gives no reach (see the questions in the proposal).
- An answer with `nextPageToken` set and an empty next page cannot happen: the extra row decides `More`. A concurrent delete between two pages is not possible: no API deletes an entry, and retention runs at the scope level.
