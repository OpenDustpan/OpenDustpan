# namespaces Specification

## Purpose
The namespaces capability covers how a client reads the list of namespaces that it can see. It pages the list with a cursor that only works with the query that made it, so a changed or reused cursor is refused. It does not cover the content or the settings of a namespace.

## Requirements

### Requirement: The namespace list pages with a bound cursor
The server SHALL page `GET /api/server/namespaces` with an opaque cursor that
is sealed and bound to the query that made it. The server SHALL answer 400
with the code `request.page-token-invalid` for a cursor that another query
made, a cursor that was changed, a cursor that is not the wire shape, or a
cursor that holds no namespace name. The server SHALL answer the next page in
name order for the `nextCursor` of the page before with the same query, and
SHALL answer an empty `nextCursor` on the last page.

#### Scenario: A cursor of another query
- **GIVEN** a caller that reads a page as an administrator and takes `nextCursor`
- **WHEN** a caller with one visible namespace sends that cursor
- **THEN** the answer is 400 with the code `request.page-token-invalid`, and no page

#### Scenario: A changed cursor
- **GIVEN** the `nextCursor` of a page
- **WHEN** the caller sends it with one byte changed
- **THEN** the answer is 400 with the code `request.page-token-invalid`

#### Scenario: The page after
- **GIVEN** the `nextCursor` of a page and the same query
- **WHEN** the caller sends the cursor
- **THEN** the answer holds the namespaces after that name, in name order

#### Scenario: The last page
- **GIVEN** a page that holds the last namespace of the query
- **WHEN** the caller reads it
- **THEN** `nextCursor` is empty

#### Scenario: A query without a cursor
- **GIVEN** a list request with no cursor
- **WHEN** the caller reads it
- **THEN** the answer holds the first page, and `total` counts the visible namespaces
