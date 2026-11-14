## ADDED Requirements

### Requirement: Preserve maintenance codes through limiter

When the notification test limiter wraps a maintenance or schema-minimum sentinel, the server SHALL answer the maintenance or schema-minimum 503 code.

#### Scenario: Limit plus maintenance
- **WHEN** the limiter wraps a maintenance sentinel with ErrLimitUnavailable
- **THEN** the server answers the maintenance 503 code without database error text.

#### Scenario: Limit plus schema minimum
- **WHEN** the limiter wraps a schema-minimum sentinel with ErrLimitUnavailable
- **THEN** the server answers the schema-minimum 503 code without database error text.

#### Scenario: Plain limit without sentinel
- **WHEN** the limiter reports ErrLimitUnavailable with no underlying sentinel
- **THEN** the server answers the limiter 503 without database error text, message send, or audit entry.

### Requirement: Bound counter-failure warnings

When the shared notification test counter fails, the server SHALL write a rate-limited warning without logging database error text.

#### Scenario: Repeated counter failures
- **WHEN** the shared notification test counter fails repeatedly
- **THEN** the server writes at most one warning per 60 s per process, sends no message and writes no audit entry.
