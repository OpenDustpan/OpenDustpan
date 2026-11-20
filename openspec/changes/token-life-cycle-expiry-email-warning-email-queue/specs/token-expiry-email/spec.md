## ADDED Requirements

### Requirement: warning-notification-per-token-and-level

The server SHALL append one notification of the type `token.expiry-warning` for each active personal access token and each warning level, to the owner of the token, once.

#### Scenario: soon level
- **WHEN** a personal token expires in 13 days and the job runs
- **THEN** one notification with the level soon and the owner as recipient is stored

#### Scenario: imminent level
- **WHEN** the same token expires in 2 days and the job runs
- **THEN** one notification with the level imminent is stored, and the earlier one is not repeated

#### Scenario: repeat run
- **WHEN** the job runs twice, or on two instances, with the same state
- **THEN** one notification for the token and level exists

#### Scenario: rotation
- **WHEN** a rotation moves the expiry of the token
- **THEN** the next warning has a new dedupe key

### Requirement: no-warning-for-ended-or-other-tokens

The server SHALL NOT append a warning for an expired, revoked or disabled token, for a token of a disabled owner, or for a token that is not a personal token.

#### Scenario: ended token
- **WHEN** a token is revoked and the job runs
- **THEN** no notification is stored for it

#### Scenario: service token
- **WHEN** a service token expires in 2 days and the job runs
- **THEN** no notification is stored for it

### Requirement: warning-holds-no-secret

The notification SHALL hold only the token ID, the token name, the expiry time and the level, and SHALL NOT hold the secret, the digest or a prefix of the secret.

#### Scenario: hostile name
- **WHEN** the token name holds CR, LF and 1000 characters
- **THEN** the stored text is cut to the bounds and holds no line break
