## ADDED Requirements

### Requirement: Expiry on another unit

When a start names an expired key of the same principal and the same namespace, the server SHALL treat the key as new on any unit. The server SHALL write no field of the old job except its key, its version and its update time. The server SHALL write the audit entry `job:key-expired` with the old job, the unit of the old job, the unit of the new start and the principal.

#### Scenario: A start on another unit
- **WHEN** a principal starts a job on unit A with a key, and more than 24 hours later starts a job on unit B with the same key
- **THEN** the server starts the job on unit B, the job on unit A holds no key, and one entry `job:key-expired` names both units

#### Scenario: Another principal
- **WHEN** another principal sends the same key string more than 24 hours after the first start
- **THEN** the server starts a job and the job of the first principal keeps its key and its version

#### Scenario: Another unit within 24 hours
- **WHEN** the principal sends the same key for unit B within 24 hours of the start on unit A
- **THEN** the server answers 422 with `request.idempotency-key-reused` and writes no job
