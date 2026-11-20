## ADDED Requirements

### Requirement: refusal-is-one-type

When the access token authenticator refuses a value, the server SHALL return one refusal type that wraps the unauthenticated error, holds a reason from a fixed list and the token ID, and has one fixed text for every reason.

#### Scenario: same text for every reason
- **WHEN** the authenticator refuses an unknown, an expired, a revoked, a disabled and an inactive token in turn
- **THEN** the error text is the same in all cases and each error matches the unauthenticated error

#### Scenario: reason and token ID
- **WHEN** the authenticator refuses an expired token
- **THEN** the refusal holds the reason expired and the ID of that token

#### Scenario: unknown token has no ID
- **WHEN** the authenticator refuses a value of the form of a secret that no token has
- **THEN** the refusal holds the reason unknown token and an empty token ID

### Requirement: ended-token-answer-is-plain

When a request carries an expired, revoked or disabled access token, the server SHALL answer 401 with the same bytes and headers as for an unknown token, and SHALL NOT name the token.

#### Scenario: Pulumi route
- **WHEN** the Pulumi CLI sends an expired token and then an unknown token
- **THEN** both answers have the status 401, the same headers and the same body

#### Scenario: TFE-compatible route
- **WHEN** OpenTofu sends a token that the inactivity sweep disabled and then an unknown token
- **THEN** both answers have the status 401, the same headers and the same body

#### Scenario: tool-neutral route
- **WHEN** a client sends a revoked token and then an unknown token to a tool-neutral route
- **THEN** both answers have the status 401, the same headers and the same body

### Requirement: lookup-and-compare-before-status

When a request carries a value of the form of an access token secret, the server SHALL look the token up by the digest of the secret and compare the digest in constant time before it checks the status.

#### Scenario: a different stored digest
- **WHEN** the token store answers a record whose digest differs from the digest of the value
- **THEN** the server refuses the value as an unknown token

#### Scenario: ended and unknown share the path
- **WHEN** a client sends an expired token and a token that no record has
- **THEN** both requests reach the digest lookup and the digest compare before the refusal

### Requirement: audit-refusal-of-access-token

When the server refuses a value of the form of an access token secret on the request path, the server SHALL write one audit entry in the server scope with the outcome denied, the reason, the source address and the request ID, and the token ID when the token is known.

#### Scenario: ended token
- **WHEN** a client sends an expired token
- **THEN** one entry holds the reason expired and the ID of that token

#### Scenario: unknown token
- **WHEN** a client sends a value of the form of a secret that no token has
- **THEN** one entry holds the reason unknown token and no token ID

#### Scenario: the entry never holds the secret
- **WHEN** a client sends a token that the server refuses
- **THEN** no field of the entry holds the value, its digest or a part of it

#### Scenario: a value of another form
- **WHEN** a client sends a value that does not have the form of an access token secret
- **THEN** the access token decorator writes no entry

#### Scenario: a source over its limit
- **WHEN** the failure gate answers the rate-limit error
- **THEN** the decorator writes no entry and the answer stays the rate-limit answer

#### Scenario: the audit write fails
- **WHEN** the audit store cannot write the entry of a refusal
- **THEN** the client still gets the 401, the server logs the failure and counts it

### Requirement: own-list-status-filter

The own token list, `GET /api/dustpan/v1/me/tokens`, SHALL take the query name `status` with the value active, expiring or ended, and SHALL answer 400 for any other value.

#### Scenario: ended tokens of the caller
- **WHEN** a Developer sends `GET /api/dustpan/v1/me/tokens?status=ended` after one token expired, one was revoked and one was disabled
- **THEN** the page holds those three tokens of that person and no other token

#### Scenario: no status
- **WHEN** a Developer sends the request with no `status`
- **THEN** the page holds every token of that person

#### Scenario: another value
- **WHEN** a Developer sends `status=gone`
- **THEN** the server answers 400 with the code request.invalid-query and names active, expiring and ended

#### Scenario: other names stay refused
- **WHEN** a Developer sends `principal`, `limit` or `cursor`
- **THEN** the server answers 400

### Requirement: policy-shows-rotation-overlap

When a caller with the action server:read-tokens reads the token policy, the server SHALL answer the rotation overlap in seconds, read only.

#### Scenario: overlap shown
- **WHEN** an administrator sends `GET /api/server/token-policy`
- **THEN** the answer holds `rotationOverlapSeconds` with the value 86400

#### Scenario: overlap cannot be changed
- **WHEN** an administrator sends a `PATCH` that holds `rotationOverlapSeconds`
- **THEN** the server answers 400

### Requirement: rotation-overlap

When a holder rotates an access token without an end time, the server SHALL keep the old token valid until the overlap ends or the old expiry passes, whichever is first.

#### Scenario: old token inside overlap
- **WHEN** a client sends the old token before its end time
- **THEN** the server accepts the token

#### Scenario: old token after overlap
- **WHEN** a client sends the old token after its end time
- **THEN** the server answers 401
