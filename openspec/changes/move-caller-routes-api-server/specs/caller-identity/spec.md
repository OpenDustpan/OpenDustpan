## ADDED Requirements

### Requirement: Caller access route

The server SHALL serve GET /api/dustpan/v1/me/access for the caller principal.

#### Scenario: Read own access
- **WHEN** the caller sends GET /api/dustpan/v1/me/access with a valid token
- **THEN** the server returns only the actions that grants give the caller

### Requirement: Caller grants route

The server SHALL serve GET /api/dustpan/v1/me/grants for the caller principal.

#### Scenario: List own grants
- **WHEN** the caller sends GET /api/dustpan/v1/me/grants with a valid token
- **THEN** the server returns the grants of the caller with holder, role, object, creator and time

### Requirement: Caller routes need sign-in

When a request to a /api/dustpan/v1/me route has no access token, the server SHALL answer 401 with a stable error code.

#### Scenario: Call without token
- **WHEN** a request has no access token
- **THEN** the server answers 401 with a stable error code

### Requirement: Caller token list

When the caller sends GET /api/dustpan/v1/me/tokens, the server SHALL return the tokens of the caller with pageSize, pageToken and nextPageToken paging.

#### Scenario: List own tokens
- **WHEN** the caller sends pageSize and pageToken
- **THEN** the server returns the tokens of the caller and an empty nextPageToken on the last page

#### Scenario: Reject old paging
- **WHEN** the caller sends cursor or limit, or a bad pageToken
- **THEN** the server answers 400 with a stable error code that names pageSize and pageToken

### Requirement: Token create

When the caller sends POST /api/dustpan/v1/me/tokens, the server SHALL create one token for the caller.

#### Scenario: Create with key
- **WHEN** the caller sends POST /api/dustpan/v1/me/tokens with an Idempotency-Key
- **THEN** the server creates the token once and a replay makes no second token

### Requirement: Token rotate

When the caller sends POST /api/dustpan/v1/me/tokens/{id}/rotate for an own token, the server SHALL issue the new token and end the old token in one transaction.

#### Scenario: Rotate own token
- **WHEN** the caller rotates an own token
- **THEN** the new token works and the old token answers 401

#### Scenario: Failed rotate
- **WHEN** the rotate fails before commit
- **THEN** the old token still works and no new token exists

### Requirement: Token delete

When the caller sends DELETE /api/dustpan/v1/me/tokens/{id} for an own token, the server SHALL end that token.

#### Scenario: Delete own token
- **WHEN** the caller deletes an own token
- **THEN** the next request with that token answers 401

#### Scenario: Delete foreign token
- **WHEN** the token does not exist or belongs to another principal
- **THEN** the server answers 404 and changes nothing

### Requirement: Old paths gone

The server SHALL answer 404 from the router for each /api/server/caller path.

#### Scenario: Call old path
- **WHEN** the caller sends a request to /api/server/caller/access
- **THEN** the server answers the router 404 with no replacement pointer
