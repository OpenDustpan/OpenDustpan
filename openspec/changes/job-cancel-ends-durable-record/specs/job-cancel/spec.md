## ADDED Requirements

### Requirement: Cancel ends the record

When a developer cancels a running job, the server SHALL end the job and its durable operation record as canceled in one transaction.

#### Scenario: Cancel of a running job
- **WHEN** a developer cancels a running job
- **THEN** the job state and the durable record both read canceled

### Requirement: Failed cancel keeps both

If the write of the durable record fails during a cancel, then the server SHALL leave the job state unchanged.

#### Scenario: Record write fails
- **WHEN** the durable record write fails during a cancel
- **THEN** the job state is unchanged and the request returns an error
