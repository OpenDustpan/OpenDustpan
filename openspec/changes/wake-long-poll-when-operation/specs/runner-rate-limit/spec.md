## ADDED Requirements

### Requirement: Poll rate limit

When a runner sends more than 10 poll requests in one second, the server SHALL answer 429 with code rate-limited and a Retry-After header.

#### Scenario: Eleventh poll limited
- **WHEN** one runner sends an 11th poll in one second
- **THEN** the server answers 429 rate-limited with Retry-After: 1 and the other runner still polls

### Requirement: Session rate limit

When a job session sends more than 50 requests in one second, the server SHALL answer 429 with code rate-limited and a Retry-After header.

#### Scenario: Fifty-first session request limited
- **WHEN** one job session sends a 51st request in one second
- **THEN** the server answers 429 rate-limited with Retry-After: 1

### Requirement: Retry-After value

When the limiter refuses a request, the server SHALL set Retry-After to 1 second.

#### Scenario: Retry-After is one second
- **WHEN** the limiter refuses a poll or a session request
- **THEN** each 429 carries Retry-After: 1

### Requirement: Session key scope

When a new job session starts, the server SHALL count its requests under its own session ID.

#### Scenario: New session starts new window
- **WHEN** a launcher exchanges a handoff token for a new job session
- **THEN** the new session gets the full 50 requests in one second

### Requirement: Bounded limiter state

While the limiter holds per-runner and per-session counts, the server SHALL evict each entry idle for 5 minutes.

#### Scenario: Idle keys removed
- **WHEN** a runner or session stays idle for 5 minutes
- **THEN** the limiter holds no entry for the idle runner or session
