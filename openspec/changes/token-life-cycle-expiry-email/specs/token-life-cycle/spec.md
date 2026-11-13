## ADDED Requirements

### Requirement: Last-used time

The server SHALL record the last-used time of an access token on each authenticated use.

#### Scenario: CLI use updates time
- **WHEN** a Developer runs a CLI command with a valid token
- **THEN** the server stores a new last-used time for the token and writes an audit entry for the operation.

#### Scenario: Concurrent disable wins
- **WHEN** a use and an automatic disable race on one token
- **THEN** the server keeps the token disabled and records the use attempt in the audit entry.

### Requirement: Expired token refused

When a request carries an expired access token, the server SHALL answer 401.

#### Scenario: Pulumi with expired token
- **WHEN** a Developer runs pulumi up with a token past its expiry
- **THEN** the server answers 401 and performs no state read or write.

#### Scenario: OpenTofu with expired token
- **WHEN** a Developer runs tofu apply with a token past its expiry
- **THEN** the server answers 401 and performs no state read or write.

#### Scenario: Expired token with bad signature
- **WHEN** a hostile request carries an expired token with an invalid signature
- **THEN** the server answers 401 without naming expiry and performs no state read or write.

### Requirement: Revoked token refused

When a request carries a revoked access token, the server SHALL answer 401.

#### Scenario: Revoked before expiry
- **WHEN** a Developer runs a CLI command with a token revoked by an administrator before expiry
- **THEN** the server answers 401 and performs no state read or write.

### Requirement: Rotation grace

When a rotation call sets an end time for the old token, the server SHALL accept the old token until that end time.

#### Scenario: Old token inside end time
- **WHEN** a rotation call sets an end time for the old token
- **THEN** the server accepts the old token until that end time and refuses it after.

### Requirement: Renewal by rotation

The server SHALL provide renewal of an access token through rotation or creation of a new token.

#### Scenario: Renew an expiring token
- **WHEN** a Developer calls rotation or creates a new token before expiry
- **THEN** the server issues a new token with a new expiry and applies the rotation end time to the old token.

### Requirement: Maximum lifetime

When a token request names an expiry past the applicable maximum, the server SHALL refuse the request with 422.

#### Scenario: Expiry past default maximum
- **WHEN** a Developer requests a token expiry past 365 days with no installation maximum
- **THEN** the server answers 422 and names the 365-day maximum.

#### Scenario: Expiry past installation maximum
- **WHEN** an administrator sets an installation maximum and a Developer requests an expiry past it
- **THEN** the server answers 422 and names the installation maximum.

#### Scenario: Namespace shorter maximum
- **WHEN** a namespace sets a shorter maximum and a caller requests a service principal token past it
- **THEN** the server answers 422 and names the namespace maximum.

#### Scenario: Existing token past new maximum
- **WHEN** an administrator shortens the maximum below the expiry of an existing token
- **THEN** the server keeps the stored expiry and applies the new maximum only to new tokens.

### Requirement: Service token issue grant

When a caller requests a token for a service principal, the server SHALL require principal:manage-tokens on that principal.

#### Scenario: Service token without grant refused
- **WHEN** a caller without principal:manage-tokens on the service principal requests a token for that principal
- **THEN** the server answers 403 and creates no token.

### Requirement: Disabled owner refuses use

When the owner principal of a service token is disabled or removed, the server SHALL refuse use of that token with 401.

#### Scenario: Owner disabled
- **WHEN** a service principal token is used after its owner principal is disabled
- **THEN** the server answers 401 and performs no state read or write.

#### Scenario: Owner removed
- **WHEN** a service principal token is used after its owner principal is removed
- **THEN** the server answers 401 and performs no state read or write.

### Requirement: Single clock

The server SHALL evaluate token expiry, inactivity cutoff and warning windows against database transaction time with a tolerance of 30 seconds.

#### Scenario: Expiry past tolerance
- **WHEN** a token stands past its expiry by more than 30 seconds of database transaction time
- **THEN** the server answers 401 and performs no state read or write.

#### Scenario: Expiry at the boundary
- **WHEN** a token reaches its expiry within 30 seconds of server time
- **THEN** the server refuses the token only after the tolerance passes.

### Requirement: Inactivity disable

When an access token shows no use for the inactivity period, the server SHALL disable the token except the break-glass credential.

#### Scenario: Idle token disabled
- **WHEN** a token shows no use for the inactivity period
- **THEN** the server disables the token and the next CLI request fails.

### Requirement: Inactivity settings

The server SHALL provide separate on-off switches and periods for access tokens, user accounts and the break-glass credential with a 90-day default and write an audit entry for each change.

#### Scenario: Change writes audit
- **WHEN** an administrator changes an inactivity period or turns a part off
- **THEN** the server writes an audit entry for the change.

### Requirement: Automatic disable audit

When the server disables a token for inactivity, the server SHALL write an audit entry that names inactivity as the reason.

#### Scenario: Automatic disable recorded
- **WHEN** the server disables a token for inactivity
- **THEN** the server writes an audit entry that names inactivity as the reason.

### Requirement: Re-enable after inactivity

While a token stays disabled for inactivity, the server SHALL provide re-enable by rotation or administrator action.

#### Scenario: Re-enable after disable
- **WHEN** a Developer holds a token disabled for inactivity
- **THEN** the server enables use again after rotation or an administrator re-enable.

### Requirement: Expiry warning field

When an access token stands inside a warning window, the server SHALL include the warning field with the threshold and the renew step in the token API response.

#### Scenario: Token near expiry
- **WHEN** a Developer reads a token inside a warning window
- **THEN** the token API response holds the warning field with the threshold, the expiry time and the renew step.

### Requirement: Console banner data

When an access token stands inside a warning window, the server SHALL include banner data in the token answer for the console.

#### Scenario: Banner near expiry
- **WHEN** a Developer opens the console with a token inside a warning window
- **THEN** the token answer holds banner data with the expiry time.

### Requirement: Expiry warning email

When an access token enters a warning window, the server SHALL queue one warning email per threshold crossing to the owner through the installation SMTP settings.

#### Scenario: Warning email sent once
- **WHEN** a token enters a warning window and SMTP settings exist
- **THEN** the test SMTP server receives one warning email to the owner of the token for that threshold.

#### Scenario: Backlog warned at sweep
- **WHEN** a token already stands inside a warning window at deployment or after a shortened expiry
- **THEN** the server queues one warning email for that threshold at the next sweep.

### Requirement: Missing SMTP visible

If SMTP settings are absent at warning time, then the server SHALL keep the API warning field and record the skipped email in the token answer.

#### Scenario: No SMTP settings
- **WHEN** a token enters a warning window with no SMTP settings
- **THEN** the token answer keeps the warning field, records the skipped email and writes an audit entry visible to an administrator.

### Requirement: Durable warning queue

While email delivery is pending, the server SHALL retain the queued warning across restarts with the same idempotency key.

#### Scenario: Restart keeps warning
- **WHEN** the server restarts after queue insert and before SMTP send
- **THEN** the server sends the warning once after restart with no duplicate.

### Requirement: Email retry once

While email delivery fails, the server SHALL retry delivery with the same idempotency key without sending a duplicate.

#### Scenario: SMTP failure retries
- **WHEN** SMTP delivery fails or the server restarts during send
- **THEN** the server retries with the same idempotency key and sends no duplicate.

### Requirement: CLI expiry warning

When a CLI request carries a token inside a warning window, the server SHALL return the CLI expiry warning.

#### Scenario: CLI shows warning
- **WHEN** a Developer runs a CLI command with a token inside a warning window
- **THEN** the CLI output holds the expiry warning with the expiry time.
