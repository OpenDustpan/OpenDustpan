## ADDED Requirements

### Requirement: Sensitive configuration action with fresh check

When a caller changes a trusted identity provider, a claim mapping, or an account matching rule, the server SHALL require server:manage-sign-in on the installation and a fresh authentication inside the one window.

#### Scenario: Missing action
- **WHEN** A caller without server:manage-sign-in makes such a change.
- **THEN** The server answers 403.

#### Scenario: Missing fresh step
- **WHEN** A caller with the action has no fresh authentication inside the one window.
- **THEN** The server answers 403 with code mfa.fresh-step-required.

### Requirement: Change alert

When such a change occurs, the server SHALL write an audit entry and a WARN line with the injected logger.

#### Scenario: Alert on change
- **WHEN** A trusted provider changes.
- **THEN** The server writes one audit entry and one WARN line.

### Requirement: Expiry alert

When a SCIM token is within 168 hours of its expiry, the server SHALL write one audit entry and one WARN line for that token and expiry value.

#### Scenario: Expiry warning
- **WHEN** A token nears expiry.
- **THEN** One alert is written per token per expiry value, also after a restart.

#### Scenario: Expired token flood
- **WHEN** A client uses an expired token 1000 times in one minute.
- **THEN** The server refuses each use and writes one audit entry for that token and expiry value.

### Requirement: Config audit failure

When the audit write for such a change fails, the server SHALL fail the change with no state change.

#### Scenario: Failed audit ends change
- **WHEN** The audit write fails.
- **THEN** The server answers 500 and changes no configuration.
