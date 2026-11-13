## ADDED Requirements

### Requirement: Sensitive configuration action with fresh check

When a caller changes a trusted identity provider, a claim mapping, or an account matching rule, the server SHALL require server:manage-sign-in on the installation and a fresh authentication inside the one window.

#### Scenario: Route action
- **WHEN** A caller changes a trusted identity provider, claim mapping, or account matching rule.
- **THEN** The server answers 403 for a caller without server:manage-sign-in.

#### Scenario: Fresh step required on each change
- **WHEN** A caller with server:manage-sign-in changes a trusted identity provider, claim mapping, or account matching rule without a fresh authentication inside the one window or with a fresh step outside the shortest effective window.
- **THEN** The server answers 403 with code mfa.fresh-step-required.

#### Scenario: No factor caller
- **WHEN** A caller of a plain OAuth or trusted-header sign-in with no enrolled passkey, TOTP, or SSH key attempts such a change.
- **THEN** The server answers 403 with code mfa.fresh-step-required and a message that names the enrollment page.

### Requirement: SCIM token guard for scim change

Where the SCIM token route changes a SCIM token, the server SHALL require the same guard and expiry alert as a sensitive configuration change.

#### Scenario: Guard definition
- **WHEN** The SCIM token route ships.
- **THEN** The scim change applies server:manage-sign-in with a fresh authentication inside the one window and the 168-hour expiry alert to its token route.

### Requirement: Change and expiry alert

When such a change occurs, the server SHALL write an audit entry and a WARN line with the injected logger.

#### Scenario: Expiry warning
- **WHEN** A SCIM token nears its expiry.
- **THEN** The server writes an audit entry and a WARN line 168 hours before expiry with the injected clock.

#### Scenario: Expired token and backward clock
- **WHEN** A SCIM token has already expired or the clock moves backwards.
- **THEN** The server treats the token as expired, refuses its use, and writes an audit entry.

#### Scenario: No expiry and re-armed expiry
- **WHEN** A SCIM token has no expiry date or its expiry changes after the alert was written.
- **THEN** The server raises no warning for a token with no expiry date, and raises one more warning after the expiry changes to a later date past the prior alert.

### Requirement: Config audit failure

When the audit write for such a change fails, the server SHALL fail the change with no state change.

#### Scenario: Failed audit ends change
- **WHEN** The audit write for a trusted provider, claim mapping, or account matching change fails.
- **THEN** The server fails the change, changes no configuration, and answers 500.
