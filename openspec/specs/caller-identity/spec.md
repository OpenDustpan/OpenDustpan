# caller-identity Specification

## Purpose
This capability covers the caller identity routes: the caller's own granted actions and the objects where the caller may act, each answered for the caller only. It stops at reads; grant management stays outside.

## Requirements

### Requirement: Caller access route

The server SHALL serve GET /api/dustpan/v1/me/access for the caller principal.

#### Scenario: Read own access
- **WHEN** the caller sends GET /api/dustpan/v1/me/access with a valid token
- **THEN** the server returns only the actions that grants give the caller

### Requirement: Caller access objects route

The server SHALL serve GET /api/dustpan/v1/me/access/objects for the caller principal. It needs the action caller:read on the caller and no other grant, and it lists the objects of the query type where the caller can do the query action.

#### Scenario: List objects by type
- **WHEN** the caller sends GET /api/dustpan/v1/me/access/objects?type=namespace with a valid token
- **THEN** the server returns one page of the namespaces where the caller can do the default action, with type, action, total, all and nextPageToken, and the caller sees only its own access

#### Scenario: Type missing or unknown
- **WHEN** the request has no type, or a type other than namespace, collection, unit or principal
- **THEN** the server answers 400 with a stable error code, and the message names the allowed types and the optional action

#### Scenario: Unknown action refused
- **WHEN** the request names an action that the server does not know
- **THEN** the server answers 400 with a stable error code and lists no object

#### Scenario: Paging names
- **WHEN** the request sends cursor or limit, a pageSize above 200, or a pageToken that is not a nextPageToken
- **THEN** the server answers 400 with a stable error code that names pageSize and pageToken

#### Scenario: The access route takes no type
- **WHEN** the caller sends GET /api/dustpan/v1/me/access with the query type and no object
- **THEN** the server answers 400 with a stable error code, and the message names /api/dustpan/v1/me/access/objects

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

The server SHALL answer 404 from the router for each path under /api/server/caller/access (the objects list included), /api/server/caller/grants and /api/server/caller/tokens.

#### Scenario: Call old path
- **WHEN** the caller sends a request to /api/server/caller/access
- **THEN** the server answers the router 404 with no replacement pointer
