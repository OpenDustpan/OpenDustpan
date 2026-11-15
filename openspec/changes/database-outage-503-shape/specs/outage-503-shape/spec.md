## ADDED Requirements

### Requirement: ready-503-during-outage

When the store is unavailable, the server SHALL answer GET /health/ready with 503 and Retry-After 30 seconds.

#### Scenario: Database stopped
- **WHEN** PostgreSQL is stopped and GET /health/ready arrives
- **THEN** the instance answers 503 with Retry-After 30 seconds within 5 seconds

### Requirement: live-stays-green

While the store is unavailable, the server SHALL answer GET /health/live with 200 without a store check.

#### Scenario: Database stopped
- **WHEN** PostgreSQL is stopped and GET /health/live arrives
- **THEN** the instance answers 200 without a store check

### Requirement: write-503-shape

When the store is unavailable, the server SHALL answer each database write with 503 and Retry-After 30 seconds in the wire shape of its API.

#### Scenario: TFE write during outage
- **WHEN** PostgreSQL is stopped and a workspace write arrives
- **THEN** the adapter answers 503 with Retry-After 30 seconds in the JSON:API error shape

#### Scenario: Pulumi write during outage
- **WHEN** PostgreSQL is stopped and a stack write arrives
- **THEN** the adapter answers 503 with Retry-After 30 seconds in the camelCase error shape

#### Scenario: Tool-neutral write during outage
- **WHEN** PostgreSQL is stopped and a tool-neutral write arrives
- **THEN** the adapter answers 503 with Retry-After 30 seconds in the tool-neutral error shape

### Requirement: read-and-auth-503

When the store is unavailable, the server SHALL answer each read or sign-in lookup that needs the store with 503 and Retry-After 30 seconds.

#### Scenario: Read during outage
- **WHEN** PostgreSQL is stopped and a read or sign-in lookup needs the store
- **THEN** the adapter answers 503 with Retry-After 30 seconds

### Requirement: outage-discrimination

When the store returns a constraint error or a client context cancel, the server SHALL keep the logic error and send no outage answer.

#### Scenario: Constraint error
- **WHEN** the store returns a constraint error while PostgreSQL runs
- **THEN** the adapter keeps the logic error and never sends 503

### Requirement: recovery-without-restart

When the store returns after a stop, the server SHALL answer GET /health/ready with 200 and accept writes with no restart.

#### Scenario: Database returns
- **WHEN** PostgreSQL returns after a stop
- **THEN** the instance answers 200 on GET /health/ready and accepts writes with no restart
