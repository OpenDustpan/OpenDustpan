## ADDED Requirements

### Requirement: idempotency-retention

When a caller starts a job with an Idempotency-Key, the server SHALL keep the stored answer in the durable store for 24 hours.

#### Scenario: Retry within 24 hours
- **WHEN** the caller retries a job start with the same key within 24 hours
- **THEN** the server returns the stored answer for the same principal, route, and key

#### Scenario: Retry after restart
- **WHEN** the caller retries within 24 hours after a server restart or store failover
- **THEN** the server returns the stored answer for the same principal, route, and key

#### Scenario: Retry while first request runs
- **WHEN** the caller retries with the same key while the first request still runs
- **THEN** the server returns the stored or in-flight answer without starting a second job

#### Scenario: Crash before store
- **WHEN** the first request crashed or timed out before it stored an answer
- **THEN** the retry starts a new job

#### Scenario: Same key different body
- **WHEN** the caller retries with the same key and a different request body
- **THEN** the server refuses the retry with 409 and error code job.idempotency-conflict without starting a second job

#### Scenario: Retry after 24 hours
- **WHEN** the caller retries with the same key after 24 hours
- **THEN** the server treats the key as expired and starts a new job

### Requirement: wrong-source-for-agent

When the actor is an agent and the given source is not an agent source, the server SHALL refuse the job start.

#### Scenario: Non-agent source
- **WHEN** the actor is an agent and the given source is not an agent source
- **THEN** the server refuses the job start with a 400 answer and error code job.bad-source

### Requirement: denial-record

When the server refuses a job start, the server SHALL write a denial record with the refused action, the actor, the reason, and the time.

#### Scenario: Refused apply
- **WHEN** the server refuses a job start for an action other than run plan
- **THEN** the denial record names the refused action with the actor, the reason, and the time

### Requirement: cancel-state

When a cancel arrives after a change began, the server SHALL end the job and the durable record both as canceled.

#### Scenario: Cancel after change began
- **WHEN** a cancel arrives after a change began
- **THEN** the job and the durable record both carry canceled
