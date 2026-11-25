## ADDED Requirements

### Requirement: Durable retention

When a client sends a job start with an Idempotency-Key header that names a job created 24 hours ago or less, the server SHALL return that job and SHALL start no second job. The server SHALL derive the age from the creation time of the job and the time of the database, and SHALL store no expiry time.

#### Scenario: Retry after restart
- **WHEN** the server restarts and the client sends the same key and body within 24 hours of the first start
- **THEN** the server returns the first job with the header `Idempotent-Replayed: true` and starts no second job

#### Scenario: Retry at exactly 24 hours
- **WHEN** the client sends the same key and body when the job that the key names is exactly 24 hours old
- **THEN** the server returns that job and starts no second job

### Requirement: Expiry

When a client sends a job start with an Idempotency-Key header that names a job created more than 24 hours ago, the server SHALL treat the key as new. In the same transaction, the server SHALL clear the key of the old job and SHALL store the key with the new job.

#### Scenario: Retry after 24 hours
- **WHEN** the client sends the same key one millisecond more than 24 hours after the first job was created
- **THEN** the server starts a new job, the new job holds the key, and the old job holds no key

#### Scenario: Another body after 24 hours
- **WHEN** the client sends the same key with another body more than 24 hours after the first job was created
- **THEN** the server starts a new job and does not answer 422

#### Scenario: Retry within 24 hours of the new job
- **WHEN** the client sends the same key and body again after the server started the new job
- **THEN** the server returns the new job and starts no third job

#### Scenario: Two retries race after 24 hours
- **WHEN** two starts with the same expired key run at the same time
- **THEN** the server starts one new job, and both answers name it

#### Scenario: A failed start keeps the old key
- **WHEN** the start with an expired key fails after the clear, for example because the unit is busy
- **THEN** the transaction rolls back and the old job keeps its key

### Requirement: Retention value

The retention SHALL be a value of a type that only its constructor makes. The constructor SHALL refuse a retention of less than 24 hours. The zero value SHALL fail closed: an expiry check with it SHALL return an error, and the start SHALL fail.

#### Scenario: A short retention
- **WHEN** code asks for a retention of 23 hours
- **THEN** the constructor returns an error

#### Scenario: The zero value
- **WHEN** a job store built with the zero retention gets a start whose key names a job
- **THEN** the start fails with an error and starts no job

### Requirement: Clear only on update

An update of a job row SHALL be able to clear its key and SHALL NOT set or change a key.

#### Scenario: An update with another key
- **WHEN** an update of a job row carries a key that is not empty and differs from the stored key
- **THEN** the stored key does not change
