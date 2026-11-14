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
- **WHEN** the first request crashed before the one transaction stored the job row and the replay record
- **THEN** the retry starts a new job with no duplicate from the lost attempt

#### Scenario: Same key different body
- **WHEN** the caller retries with the same key and a different request body
- **THEN** the server refuses the retry with 409 and error code job.idempotency-conflict without starting a second job

#### Scenario: Retry after 24 hours
- **WHEN** the caller retries with the same key after 24 hours
- **THEN** the server treats the key as expired and starts a new job

### Requirement: idempotency-atomic-store

When a job start with an Idempotency-Key creates a job, the server SHALL store the job row and the replay record in one transaction.

#### Scenario: Job and answer stored together
- **WHEN** a job start with an Idempotency-Key creates a job
- **THEN** the server stores the job row and the replay record together or stores neither

### Requirement: idempotency-key-scope

When a caller sends an Idempotency-Key, the server SHALL key the stored answer by principal, route, and key.

#### Scenario: Key reuse across principals
- **WHEN** two principals send the same Idempotency-Key on the same route
- **THEN** the server treats each caller key as distinct and starts a distinct job

#### Scenario: Key reuse across routes
- **WHEN** one principal sends one Idempotency-Key on two routes
- **THEN** the server treats each route key as distinct

#### Scenario: Oversized key
- **WHEN** the Idempotency-Key exceeds 128 chars
- **THEN** the server refuses the request with a 400 answer and error code job.bad-idempotency-key

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

#### Scenario: Refused wrong source
- **WHEN** the server refuses a job start for a wrong source for an agent
- **THEN** the denial record names the refused source with the actor, the reason, and the time

### Requirement: cancel-state

When a cancel arrives after a change began, the server SHALL end the job and the durable record both as canceled.

#### Scenario: Cancel after change began
- **WHEN** a cancel arrives after a change began
- **THEN** the job and the durable record both carry canceled
