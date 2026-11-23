## ADDED Requirements

### Requirement: ready-503-during-outage

When the store is unavailable, the server SHALL answer GET /health/ready with 503 and Retry-After 5 seconds.

#### Scenario: Database stopped
- **WHEN** PostgreSQL is stopped and GET /health/ready arrives
- **THEN** the instance answers 503 with Retry-After 5 seconds within 5 seconds

### Requirement: live-stays-green

While the store is unavailable, the server SHALL answer GET /health/live with 204 without a store check.

#### Scenario: Database stopped
- **WHEN** PostgreSQL is stopped and GET /health/live arrives
- **THEN** the instance answers 204 without a store check

### Requirement: outage-discrimination

When the store returns a constraint error or a client context cancel, the server SHALL keep the logic error and send no outage answer.

#### Scenario: Constraint error
- **WHEN** the store returns a constraint error while PostgreSQL runs
- **THEN** the adapter keeps the logic error and never sends 503
