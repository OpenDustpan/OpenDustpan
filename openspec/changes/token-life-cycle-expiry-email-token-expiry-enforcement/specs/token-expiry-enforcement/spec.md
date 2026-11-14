## ADDED Requirements

### Requirement: record-last-used

When a request authenticates with a live access token, the server SHALL record the time of the request as the last-used time of the token.

#### Scenario: authenticated request updates last-used
- **WHEN** a request authenticates with a live token
- **THEN** the server stores the time of the request as the last-used time of the token

#### Scenario: failed last-used write still authenticates
- **WHEN** the last-used write fails
- **THEN** the request still succeeds and the next request retries the write

### Requirement: refuse-expired-token

When a request carries an access token past its expiry, the server SHALL answer 401.

#### Scenario: expired token over Pulumi API
- **WHEN** the Pulumi CLI sends a token past its expiry
- **THEN** the server answers 401 and records the refusal in the audit log

#### Scenario: expired token over TFE API
- **WHEN** OpenTofu sends a token past its expiry
- **THEN** the server answers 401 and records the refusal in the audit log

#### Scenario: expired token over tool-neutral API
- **WHEN** a tool-neutral client sends a token past its expiry
- **THEN** the server answers 401 and records the refusal in the audit log

### Requirement: refuse-revoked-disabled

When a request carries a revoked or disabled access token, the server SHALL answer 401.

#### Scenario: revoked token over Pulumi API
- **WHEN** the Pulumi CLI sends a revoked token
- **THEN** the server answers 401 and records the refusal in the audit log

#### Scenario: idle-disabled token over TFE API
- **WHEN** OpenTofu sends a token disabled for inactivity
- **THEN** the server answers 401 and records the refusal in the audit log

#### Scenario: disabled token over tool-neutral API
- **WHEN** a tool-neutral client sends a revoked or disabled token
- **THEN** the server answers 401 and records the refusal in the audit log

### Requirement: inactivity-disable-default

When an access token has no use for the inactivity period of 90 days by default, the server SHALL mark the token disabled.

#### Scenario: idle token disabled
- **WHEN** a token has no use for 90 days
- **THEN** the server marks the token disabled and later requests with it answer 401

#### Scenario: break-glass credential exempt
- **WHEN** the break-glass credential has no use for 90 days
- **THEN** the server keeps the credential live

### Requirement: inactivity-settings-audit

When an administrator changes the token inactivity period, the server SHALL write an audit entry for the change.

#### Scenario: administrator changes inactivity period
- **WHEN** an administrator sets the token inactivity period or turns the disable off
- **THEN** the server stores the new period and writes an audit entry for the change

### Requirement: re-enable-token

When an authorized caller re-enables a token disabled for inactivity, the server SHALL mark the token live again.

#### Scenario: disabled token re-enabled
- **WHEN** an authorized caller re-enables a token disabled for inactivity
- **THEN** the server marks the token live again and writes an audit entry

### Requirement: enforce-maximum-lifetime

When a caller requests an expiry past the applicable maximum lifetime of 365 days by default, the server SHALL refuse the create.

#### Scenario: expiry past maximum refused
- **WHEN** a caller requests an expiry past the applicable maximum
- **THEN** the server refuses the create with 403 and creates no token

#### Scenario: service token past namespace maximum refused
- **WHEN** a service token requests an expiry past the shorter namespace maximum
- **THEN** the server refuses the create or rotation with 403

### Requirement: rotation-overlap-fixed

When a holder rotates an access token, the server SHALL issue a new token and set the end time of the old token to 10 minutes after rotation.

#### Scenario: rotation issues new token with overlap
- **WHEN** a holder rotates a token
- **THEN** the server issues a new token and the old token stays valid for 10 minutes

### Requirement: rotation-old-refused

When a request carries a rotated old token past its end time, the server SHALL answer 401.

#### Scenario: old token after end time refused
- **WHEN** a client sends the old token after its end time
- **THEN** the server answers 401 and records the refusal in the audit log

### Requirement: authorize-service-rotate

When a caller without principal:manage-tokens on a service principal requests rotation of its token, the server SHALL refuse the request.

#### Scenario: service token rotation without grant refused
- **WHEN** a caller without principal:manage-tokens on the service principal rotates its token
- **THEN** the server refuses the rotation with 403

### Requirement: fresh-auth-create-rotate

When a person creates or rotates an access token without a fresh authentication in the one window, the server SHALL refuse the request.

#### Scenario: create without fresh step refused
- **WHEN** a person creates or rotates a token without a fresh authentication in the one window
- **THEN** the server answers 403 with error code mfa.fresh-step-required and creates no token

### Requirement: fresh-auth-weaker-setting

When an administrator turns the token inactivity disable off or to a longer period without a fresh authentication, the server SHALL refuse the change.

#### Scenario: weaker disable setting without fresh step refused
- **WHEN** an administrator turns the inactivity disable off or to a longer period without a fresh authentication
- **THEN** the server answers 403 and changes no setting

### Requirement: token-audit

When the server creates, rotates, disables, or re-enables a token, the server SHALL write an audit entry for the operation.

#### Scenario: token operations audited
- **WHEN** the server creates, rotates, disables, re-enables, or changes settings of a token
- **THEN** the audit log holds one entry for the operation with holder, token id, and time

### Requirement: live-token-query

When the store answers a live-token query for authentication, the server SHALL return only tokens that are neither expired, revoked, nor disabled.

#### Scenario: expired and disabled rows excluded
- **WHEN** the store holds one expired row and one disabled row
- **THEN** the live-token query returns neither row
