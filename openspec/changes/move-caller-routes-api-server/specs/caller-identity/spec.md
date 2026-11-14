## ADDED Requirements

### Requirement: Caller access route

The server SHALL serve GET /api/dustpan/v1/me/access for the caller principal.

#### Scenario: Read own access
- **WHEN** caller sends GET /api/dustpan/v1/me/access with a valid token
- **THEN** the server returns only actions that grants give the caller

### Requirement: Caller grants route

The server SHALL serve GET /api/dustpan/v1/me/grants for the caller principal.

#### Scenario: List own grants
- **WHEN** caller sends GET /api/dustpan/v1/me/grants with a valid token
- **THEN** the server returns the grants that give the caller reach with holder, role, object, creator, and time

### Requirement: Caller routes need sign-in

When a request to /api/dustpan/v1/me has no access token, the server SHALL answer 401 with a stable catalog code.

#### Scenario: Call without token
- **WHEN** caller sends GET /api/dustpan/v1/me/access with no access token
- **THEN** the server answers 401 with a stable catalog code and names sign-in

### Requirement: Caller authorization

While serving /api/dustpan/v1/me routes, the server SHALL authorize by the authenticated caller principal on object caller principal with no stored grant check.

#### Scenario: Use another principal token
- **WHEN** caller sends a /me route with a valid token of one principal
- **THEN** the server returns only the reach and tokens of the token holder

### Requirement: Caller token list paging

When the caller sends GET /api/dustpan/v1/me/tokens, the server SHALL return the tokens of the caller principal with pageSize, pageToken, and nextPageToken paging.

#### Scenario: List own tokens
- **WHEN** caller sends GET /api/dustpan/v1/me/tokens with pageSize and pageToken
- **THEN** the server returns the tokens of the caller principal with nextPageToken, empty on the last page

#### Scenario: Reject bad paging
- **WHEN** caller sends GET /api/dustpan/v1/me/tokens with cursor, limit, invalid pageToken, or pageSize above the maximum
- **THEN** the server answers 400 with a stable catalog code and changes nothing

### Requirement: Error arguments

When an error answer names a value, the server SHALL carry that value as a declared argument of its catalog code.

#### Scenario: Read 404 arguments
- **WHEN** caller calls an old /api/server/caller path
- **THEN** the answer carries the replacement path as a declared argument

#### Scenario: Read 400 arguments
- **WHEN** caller sends a bad paging query on a /me list
- **THEN** the answer carries the paging field names as declared arguments

### Requirement: Caller token mutations take key

When the caller sends POST /api/dustpan/v1/me/tokens with Idempotency-Key, the server SHALL store the answer by principal, method, path, and key and replay it without a second change.

#### Scenario: Mutate with key
- **WHEN** caller sends POST /api/dustpan/v1/me/tokens with Idempotency-Key
- **THEN** the server applies the change once and keys the stored answer by principal, method, path, and key

#### Scenario: Replay same request
- **WHEN** caller repeats POST /api/dustpan/v1/me/tokens with the same Idempotency-Key within retention and with a valid token
- **THEN** the server returns the stored status with token metadata and no secret value and makes no second change

#### Scenario: Concurrent same key
- **WHEN** two requests arrive at the same time with the same principal, method, path, and Idempotency-Key
- **THEN** the server applies the change once and the other request gets the stored answer

#### Scenario: Replay after token ended
- **WHEN** caller replays POST /api/dustpan/v1/me/tokens with the same Idempotency-Key but the auth token is now ended
- **THEN** the server answers 401 with a stable catalog code and returns no stored answer

#### Scenario: Recover lost create
- **WHEN** caller loses the first create answer and lists GET /api/dustpan/v1/me/tokens
- **THEN** the server lists the created token metadata without its secret value so the caller deletes the orphan and creates a new token

### Requirement: Idempotency conflict

If the caller reuses an Idempotency-Key with a different body or on a different method or path, then the server SHALL answer conflict with a stable catalog code and change nothing.

#### Scenario: Reuse key with different body
- **WHEN** caller sends POST /api/dustpan/v1/me/tokens with the same Idempotency-Key and a different body
- **THEN** the server answers conflict with a stable catalog code and changes nothing

### Requirement: Caller token delete

When the caller sends DELETE /api/dustpan/v1/me/tokens/{id} with Idempotency-Key, the server SHALL end the own token and replay the stored answer without a second change.

#### Scenario: Delete own token
- **WHEN** caller sends DELETE /api/dustpan/v1/me/tokens/{id} with Idempotency-Key for an own token
- **THEN** the server ends the token in one transaction and stores the answer by principal, method, path, and key

### Requirement: Caller token rotate

When the caller sends POST /api/dustpan/v1/me/tokens/{id}/rotate with Idempotency-Key, the server SHALL issue the new token and end the old token in one atomic transaction.

#### Scenario: Rotate own token
- **WHEN** caller sends POST /api/dustpan/v1/me/tokens/{id}/rotate with Idempotency-Key for an own token
- **THEN** the server issues a new token value and ends the old token value in one transaction

#### Scenario: Use old token during rotate
- **WHEN** caller sends a request with the old token value while POST /api/dustpan/v1/me/tokens/{id}/rotate is in flight
- **THEN** the server accepts at most one of the two values after the transaction commits

### Requirement: Old caller paths gone

The server SHALL stop serving /api/server/caller/access, /api/server/caller/grants, and /api/server/caller/tokens with no alias and no redirect.

#### Scenario: Call each old path
- **WHEN** caller sends GET /api/server/caller/access with a valid token
- **THEN** the server answers 404 with a stable catalog code and names the matching /api/dustpan/v1/me path
