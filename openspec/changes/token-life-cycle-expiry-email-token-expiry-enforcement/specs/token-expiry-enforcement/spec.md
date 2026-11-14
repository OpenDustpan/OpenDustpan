## ADDED Requirements

### Requirement: refuse-ended-token

When a request carries an expired, revoked or disabled access token, the server SHALL answer 401 and write an audit entry.

#### Scenario: expired token over Pulumi API
- **WHEN** the Pulumi CLI sends a token past its expiry
- **THEN** the server answers 401 and records the refusal

#### Scenario: idle-disabled token over TFE API
- **WHEN** OpenTofu sends a token disabled for inactivity
- **THEN** the server answers 401 and records the refusal

#### Scenario: revoked token over tool-neutral API
- **WHEN** a client sends a revoked token
- **THEN** the server answers 401 and records the refusal

### Requirement: visible-limits

When a caller reads the settings resource, the server SHALL answer each effective token limit with its source.

#### Scenario: limits visible
- **WHEN** a caller reads the settings resource of the installation
- **THEN** the answer holds the maximum lifetime, the inactivity period and the rotation overlap of 24 hours, each with its source

### Requirement: rotation-overlap

When a holder rotates an access token, the server SHALL set the end time of the old token to 24 hours after the rotation.

#### Scenario: old token inside overlap
- **WHEN** a client sends the old token inside the 24 hours
- **THEN** the server accepts the token

#### Scenario: old token after overlap
- **WHEN** a client sends the old token after its end time
- **THEN** the server answers 401
