## ADDED Requirements

### Requirement: Strip plain secret header

When plain-mode authentication reads the secret header, the server SHALL delete the header value before the request reaches downstream handlers.

#### Scenario: Successful plain auth
- **WHEN** plain-mode authentication succeeds
- **THEN** a downstream handler reads no secret header value.

#### Scenario: Refused plain auth
- **WHEN** plain-mode authentication is refused
- **THEN** a downstream handler reads no secret header value.

#### Scenario: Duplicate header values
- **WHEN** the request carries two secret header values
- **THEN** a downstream handler reads no secret header value.
