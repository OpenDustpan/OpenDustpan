## ADDED Requirements

### Requirement: The audit list pages with a token

The server SHALL answer `GET /api/audit/entries` with the entries of the scope newest first, in pages. A request SHALL take `pageSize` and `pageToken`. The answer SHALL hold `entries`, `nextPageToken` and `pageSize`. `nextPageToken` SHALL be present on each answer and SHALL be the empty string on the last page.

#### Scenario: First page

- **WHEN** a caller who may read the audit log sends a request with no `pageToken` for a scope of 120 entries and `pageSize=50`
- **THEN** the answer holds the 50 newest entries, `pageSize` is 50, and `nextPageToken` is not empty

#### Scenario: Next page

- **WHEN** the caller sends the `nextPageToken` of the first page as `pageToken` for the same scope
- **THEN** the answer holds the entries 51 to 100 from the newest, with no entry twice and none missed, and `nextPageToken` is not empty

#### Scenario: Last page

- **WHEN** the caller sends the token of the second page of that scope
- **THEN** the answer holds the 20 oldest entries and `nextPageToken` is the empty string

#### Scenario: A log of exactly one page

- **WHEN** the scope holds exactly 50 entries and the request has `pageSize=50`
- **THEN** the answer holds the 50 entries and `nextPageToken` is the empty string, and no empty page follows

#### Scenario: An empty scope

- **WHEN** the scope holds no entry
- **THEN** the answer holds an empty `entries` list and `nextPageToken` is the empty string

### Requirement: A page costs the same at any depth

The server SHALL read each page with one query by sequence that reads the same number of rows at any depth. The server SHALL NOT count the entries of the scope and SHALL NOT use an offset.

#### Scenario: Cost at depth

- **WHEN** the store reads a page below the token in a scope of any size
- **THEN** the plan of the query reads the primary key of `audit_entries` backwards with no sort and no count, in the SQLite adapter and in the Postgres adapter

### Requirement: The page size is declared and visible

The default page size SHALL be 50 and the largest SHALL be 200. A request with no `pageSize` SHALL read the default. The answer SHALL hold the size that the server used in `pageSize`. A `pageSize` that is not a decimal number from 1 to 200, with no sign, no leading zero and no space, SHALL answer `400` with the error code `request.page-size-invalid`, and SHALL NOT be cut to the largest size. A repeated `pageSize` SHALL answer the same error.

#### Scenario: Default size

- **WHEN** the request has no `pageSize`
- **THEN** the answer holds at most 50 entries and `pageSize` is 50

#### Scenario: Size above the largest

- **WHEN** the request has `pageSize=201`
- **THEN** the answer is `400` with `request.page-size-invalid`, and the text says to send a number from 1 to 200

#### Scenario: Size that is not canonical

- **WHEN** the request has `pageSize` of `0`, `-1`, `050`, `+5`, `5x`, an empty space, or two values
- **THEN** the answer is `400` with `request.page-size-invalid` and the store is not called

### Requirement: A page token is opaque and bound to its scope

The `nextPageToken` SHALL be opaque to the caller. It SHALL hold the last sequence of the page and a fingerprint of the scope. The server SHALL read the scope of a request from its `organization` query value, and SHALL NOT read it from the token. An empty `pageToken` SHALL read as no token.

#### Scenario: Scope comes from the request

- **WHEN** a valid token of the scope `acme` is sent with `organization=acme`
- **THEN** the page reads the scope `acme` below the sequence of the token, and the authorization check ran on `acme`

### Requirement: A token that is not valid is refused

A token that does not decode, is longer than 4096 bytes, holds a member that is not part of the token, holds a sequence below 1, or holds the fingerprint of another scope SHALL answer `400` with the error code `request.page-token-invalid` and the text "pageToken is not valid for this scope: send the request again without pageToken". A repeated `pageToken` SHALL answer the same error. The server SHALL read a token only when it is the canonical form that its encoder writes: base64url text with no byte outside `A-Z`, `a-z`, `0-9`, `-` and `_`, no padding, and no unused bits, that decodes to one JSON object with each member name exactly as the wire struct spells it and no name twice. A token whose canonical form differs from the text that was sent SHALL be refused. The refusal SHALL NOT call the store.

#### Scenario: Token of another scope

- **WHEN** the token of a page of the scope `acme` is sent with `organization=other`
- **THEN** the answer is `400` with `request.page-token-invalid` and the store is not called

#### Scenario: Token of the installation

- **WHEN** the token of a page of `@server` is sent with `organization=acme`, or the reverse
- **THEN** the answer is `400` with `request.page-token-invalid`

#### Scenario: Token that is changed

- **WHEN** the token is not base64url, is valid base64url that is not JSON, is a JSON array, has an unknown member, has a member of another type, has a sequence of 0 or below, or has a sequence above the largest number the server reads
- **THEN** the answer is `400` with `request.page-token-invalid` and the store is not called

#### Scenario: Token with a byte outside the alphabet

- **WHEN** the token holds a byte outside `A-Z`, `a-z`, `0-9`, `-` and `_`, for example a carriage return or a line feed inside the base64url text
- **THEN** the answer is `400` with `request.page-token-invalid` and the store is not called

#### Scenario: Token that is not the canonical encoding

- **WHEN** the token decodes, but the base64url text of its decoded bytes is not the same text (a final character with unused bits set, for example `e31` and `e30` for the object `{}`)
- **THEN** the answer is `400` with `request.page-token-invalid` and the store is not called

#### Scenario: Token with a member name in another case or twice

- **WHEN** the token holds a member name that differs from `b` or `f` only in letter case, or holds a name twice
- **THEN** the answer is `400` with `request.page-token-invalid` and the store is not called

#### Scenario: Forged token inside the scope

- **WHEN** a caller who may read the scope makes a token with the right fingerprint and any sequence
- **THEN** the answer holds only entries of the scope of the request below that sequence, and no entry of another scope

### Requirement: A refused page token is recorded

The server SHALL write one audit entry of the outcome denied for each request that it refuses with `request.page-token-invalid`, whatever the cause (a token of another scope, a token that does not decode, a token that is not the canonical form, a repeated `pageToken`). The server SHALL write the entry through the shared refusal recorder, with the action `namespace:read-audit` (or `server:read-audit` for `@server`) on the object of the request, so the denial limiter counts it as for any other refusal. The entry SHALL NOT hold the token. A refusal with `request.page-size-invalid` and with `request.invalid-query` stays unrecorded.

#### Scenario: Token of another scope is recorded

- **WHEN** a caller who may read the audit log sends the token of the scope `acme` with `organization=other`
- **THEN** the answer is `400` with `request.page-token-invalid`, and the audit log of `other` holds one entry with the outcome denied, the subject of the caller and the reason `request.page-token-invalid`

#### Scenario: Token that does not decode is recorded

- **WHEN** the caller sends `pageToken=not-a-token`
- **THEN** the answer is `400` with `request.page-token-invalid`, and one entry with the outcome denied is written, with no token text in it

### Requirement: Authorization runs before the token is read

The server SHALL check the action on the object of the request before it reads `pageSize` or `pageToken`. A caller who may not read the audit log of the scope SHALL get the refusal of the route, with a valid token, a changed token or no token, and SHALL learn nothing about the token.

#### Scenario: Denied caller with a valid token

- **WHEN** a caller without `namespace:read-audit` on `acme` sends a token that the server made for `acme`
- **THEN** the answer is `403`, the refusal is recorded as for any refused read (one entry with the outcome denied, and none for the token), and no token check runs

#### Scenario: Denied caller with a changed token

- **WHEN** the same caller sends a token that does not decode
- **THEN** the answer is the same `403` and not `400`

### Requirement: Pages stay stable while the log grows

The page after a token SHALL hold the entries with a sequence below the last sequence of the page before. An entry that is appended between two requests SHALL NOT appear in a later page and SHALL NOT move an entry of a later page. A gap in the sequence of the scope SHALL NOT change the entries that a page holds.

#### Scenario: Entry appended between two pages

- **WHEN** an entry is appended after the first page was read and before the second page is read
- **THEN** the second page holds the next older entries, and the new entry is in no page of that walk

#### Scenario: Gap in the sequence

- **WHEN** an entry of the scope is deleted behind the back of the store and the caller reads the scope page by page
- **THEN** the pages hold each remaining entry once, newest first, and the chain check still reports the gap

### Requirement: The old query values are refused

The server SHALL NOT read `page[number]` or `page[size]` on the audit list, and the answer SHALL NOT hold `total`. A request that holds `page[number]` or `page[size]` SHALL answer `400` with the error code `request.invalid-query` and the text "page[number] and page[size] are not supported: send pageSize and pageToken". The refusal SHALL NOT call the store.

#### Scenario: Old paging values

- **WHEN** the request holds `page[number]=2&page[size]=10`
- **THEN** the answer is `400` with `request.invalid-query`, the store is not called, and the answer holds no entry

#### Scenario: No total

- **WHEN** a caller reads any page
- **THEN** the answer has no member named `total`

### Requirement: The store refuses a page that is not valid

The port `audit.Store.List` SHALL answer `audit.ErrInvalidPage` for a page with a negative `Before`, a negative `Size`, or a `Size` above the largest page size, and SHALL read nothing. A `Size` of 0 SHALL read the default size. A `Before` of 0 SHALL start at the newest entry.

#### Scenario: Negative start

- **WHEN** a store gets `Page{Before: -1}`
- **THEN** it answers `ErrInvalidPage`, in the SQLite adapter and in the Postgres adapter

### Requirement: The store reads only the scope of the call

The adapter SHALL bind the scope as a placeholder in the query. A `Before` that belongs to the sequence range of another scope SHALL read no entry of that scope. A `Before` that no entry of the scope has below it SHALL answer an empty page. The extra row that the store reads to set `More` SHALL NOT be in the listing.

#### Scenario: Start in the range of another scope

- **WHEN** the scope `acme` holds the sequences 1 to 3, the scope `other` holds 1 to 500, and the store gets `Page{Before: 400}` for `acme`
- **THEN** the page holds the 3 entries of `acme` and no entry of `other`

#### Scenario: Extra row never leaves the store

- **WHEN** the store reads a page of size 2 in a scope of 5 entries
- **THEN** the listing holds 2 entries and `More` is true, and the third entry that the store read is not in the listing
