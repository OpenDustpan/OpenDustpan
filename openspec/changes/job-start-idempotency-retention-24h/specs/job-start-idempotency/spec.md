## ADDED Requirements

### Requirement: Durable retention

When a client sends a job start with an Idempotency-Key header that the durable store holds from less than 24 hours ago, the server SHALL return the stored answer.

#### Scenario: Retry after restart
- **WHEN** the server restarts and the client sends the same key and body within 24 hours
- **THEN** the server returns the first answer and starts no second job

### Requirement: Expiry

When a client sends a job start with an Idempotency-Key header that the durable store holds from more than 24 hours ago, the server SHALL treat the key as new.

#### Scenario: Retry after 24 hours
- **WHEN** the client sends the same key after 24 hours
- **THEN** the server starts a new job
