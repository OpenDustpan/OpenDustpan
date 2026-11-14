## ADDED Requirements

### Requirement: refuse-ended-token

When a request carries an expired, revoked or disabled access token, the server SHALL answer 401 with no detail about the token.

#### Scenario: expired token over Pulumi API
- **WHEN** the Pulumi CLI sends a token past its expiry
- **THEN** the server answers 401 with no detail about the token

#### Scenario: idle-disabled token over TFE API
- **WHEN** OpenTofu sends a token disabled for inactivity
- **THEN** the server answers 401 with no detail about the token

#### Scenario: revoked token over tool-neutral API
- **WHEN** a client sends a revoked token
- **THEN** the server answers 401 with no detail about the token

### Requirement: same-refusal-for-every-case

When a request carries an unknown, expired, revoked or disabled access token, the server SHALL build the 401 answer in one shared refusal function.

#### Scenario: identical bytes and headers
- **WHEN** a client sends an unknown, an expired, a revoked and a disabled token in turn
- **THEN** the response bytes and the headers are identical in all four cases

### Requirement: same-work-before-refusal

When a request carries an access token, the server SHALL look the token up by the digest of its secret and compare the digest in constant time before the ended check.

#### Scenario: ended and unknown share the path
- **WHEN** a client sends an expired token and an unknown token
- **THEN** both requests take the lookup by digest and the constant-time compare before the refusal

#### Scenario: constant-time compare
- **WHEN** a test runs the compare through the code path
- **THEN** the compare uses subtle.ConstantTimeCompare

### Requirement: audit-refusal-reason

When the server refuses an ended access token, the server SHALL write an audit entry on the request path that holds the reason and the token ID.

#### Scenario: reason recorded
- **WHEN** a client sends an expired token
- **THEN** the audit entry holds the reason expired and the token ID

### Requirement: audit-unknown-token

When the server refuses an unknown access token, the server SHALL write an audit entry on the request path with the reason unknown token and no token ID.

#### Scenario: unknown token recorded
- **WHEN** a client sends a token that the server does not know
- **THEN** the audit entry holds the reason unknown token and holds no token ID

### Requirement: list-ended-tokens

When a caller lists tokens with the status ended, the server SHALL answer the revoked and expired tokens of that caller.

#### Scenario: ended list
- **WHEN** a caller lists tokens with status ended after one token expired and one was revoked
- **THEN** the answer holds both tokens

### Requirement: visible-limits

When a caller reads the settings resource, the server SHALL answer each effective token limit with its source.

#### Scenario: limits visible
- **WHEN** a caller reads the settings resource of the installation
- **THEN** the answer holds the maximum lifetime, the inactivity period and the rotation overlap, each with its source

### Requirement: rotation-overlap

When a holder rotates an access token, the server SHALL keep the old token valid until its end time.

#### Scenario: old token inside overlap
- **WHEN** a client sends the old token before its end time
- **THEN** the server accepts the token

#### Scenario: old token after overlap
- **WHEN** a client sends the old token after its end time
- **THEN** the server answers 401
