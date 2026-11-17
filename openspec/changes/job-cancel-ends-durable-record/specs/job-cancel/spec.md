## ADDED Requirements

### Requirement: Cancel ends the record

When a developer cancels a job that has no executor (queued, dispatched or awaiting state), the server SHALL end the job and its durable operation record as canceled in one transaction.

#### Scenario: Cancel of a job with no executor
- **WHEN** a developer cancels a queued job
- **THEN** the job state and the durable record both read canceled

### Requirement: Cancel of a running job ends both records through the executor

When a developer cancels a running job, the server SHALL move the job to canceling and record the cancel request on the durable record. The executor SHALL then end the job and its durable record as canceled in one transaction.

#### Scenario: Cancel of a running job
- **WHEN** a developer cancels a running job
- **THEN** the job state reads canceling and the durable record holds the cancel request and stays open

#### Scenario: Executor ends a canceling job
- **WHEN** the executor of a canceling job stops
- **THEN** the job state and the durable record both read canceled

### Requirement: Failed cancel keeps both

If the write of the durable record fails during a cancel, then the server SHALL leave the job state unchanged.

#### Scenario: Record write fails
- **WHEN** the durable record write fails during a cancel
- **THEN** the job state is unchanged and the request returns an error
