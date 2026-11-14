## ADDED Requirements

### Requirement: Request unit

When a developer submits a valid Self-service request, the server SHALL make one unit in its collection and start one job.

#### Scenario: Valid request applies
- **WHEN** another team submits a valid request
- **THEN** the server makes the unit and starts one job that a runner applies

### Requirement: Refuse bad input

If a request holds an input that the blueprint does not allow, then the server SHALL refuse the request with an error that names the input.

#### Scenario: Unknown input
- **WHEN** a request holds an unknown input
- **THEN** the server answers 400 that names the input and makes no unit

### Requirement: Refuse past quota

If a create exceeds a quota rule, then the server SHALL refuse the create and make no unit and no job.

#### Scenario: Units quota
- **WHEN** a create exceeds a units rule
- **THEN** the server answers 409 and makes no unit

### Requirement: Other namespace

If a request names a blueprint of another namespace, then the server SHALL answer not found.

#### Scenario: Cross namespace
- **WHEN** a request names a blueprint of another namespace
- **THEN** the server answers 404 as for a missing name

### Requirement: Idempotent submit

When a client retries a submit with the same Idempotency-Key and body, the server SHALL return the same revision.

#### Scenario: Retry
- **WHEN** a client retries after a network failure
- **THEN** the server returns the same revision, unit and job
