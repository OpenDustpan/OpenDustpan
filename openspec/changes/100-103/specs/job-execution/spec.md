## ADDED Requirements

### Requirement: CreateJob for one unit

When a principal with the NEW action `unit:run-job` creates a job, the server SHALL store one job through CreateJob for one unit reference with a stable job ID.

#### Scenario: Create then list
- **WHEN** Developer with the NEW action unit:run-job creates a job for one unit reference.
- **THEN** The CreateJob result shows as queued with the unit reference and executor.

#### Scenario: Second active blocked
- **WHEN** Developer creates a second job for the same unit ID while a job is queued, dispatched, awaiting approval, running, or canceling.
- **THEN** The server denies the second create with NEW error code job.busy naming the active job ID.

#### Scenario: Denied without grant
- **WHEN** Principal without the NEW action unit:run-job creates a job.
- **THEN** The server denies with 403 and appends an audit entry naming the unit reference with secrets masked.

### Requirement: ClaimJob by one claim

When a claimant calls ClaimJob for a queued job, the server SHALL grant at most 1 claim per job to the first claimant.

#### Scenario: Single claim wins
- **WHEN** Two claimants call ClaimJob for the same queued job.
- **THEN** One claim holds the job and the job becomes dispatched.

#### Scenario: Second claim denied
- **WHEN** A second claimant calls ClaimJob for an already dispatched job.
- **THEN** The server denies the second ClaimJob with NEW error code job.invalid.

### Requirement: AdvanceJob forward only

While a job holds an active claim, the server SHALL move its phase through AdvanceJob forward only and deny a skip or rewind.

#### Scenario: Advance forward
- **WHEN** The holder reports phase progress through AdvanceJob under its claim.
- **THEN** The job phase moves to the next phase only.

#### Scenario: Rewind denied
- **WHEN** A caller reports a prior or terminal phase move.
- **THEN** The server denies the report with NEW error code job.invalid.

#### Scenario: Duplicate ignored
- **WHEN** The holder reports the same phase twice with the same claim key.
- **THEN** The server keeps one phase entry and returns the same phase.

### Requirement: Pool purpose jobs

When a runners job names a runner pool, the server SHALL accept only a pool of purpose jobs at create and at claim.

#### Scenario: Sweep pool refused at create
- **WHEN** Developer names a pool of purpose sweeps at job create.
- **THEN** The server denies the create with NEW error code job.invalid.

#### Scenario: Sweep pool refused at claim
- **WHEN** ClaimJob names a pool of purpose sweeps for a queued job.
- **THEN** The server denies ClaimJob with NEW error code job.invalid.

#### Scenario: Deleted pool refused
- **WHEN** The named pool was deleted or moved to purpose sweeps after create.
- **THEN** The server denies the claim with NEW error code job.invalid.

### Requirement: CancelJob for one job

When a principal with the NEW action `unit:run-job` calls CancelJob, the server SHALL move that job to canceled through canceling for a dispatched or running job.

#### Scenario: CancelJob on queued
- **WHEN** Developer with the NEW action unit:run-job calls CancelJob on a queued job.
- **THEN** The job becomes canceled and no claimant takes it.

#### Scenario: CancelJob on running
- **WHEN** Developer with the NEW action unit:run-job calls CancelJob on a dispatched or running job.
- **THEN** The job becomes canceling and then canceled, and late reports are denied.

#### Scenario: CancelJob denied
- **WHEN** Principal without the NEW action unit:run-job calls CancelJob.
- **THEN** The server denies with 403 and the job state does not change.

### Requirement: Executors runners and write-back

When a job uses executor write-back, the server SHALL advance its ordered phase list of at most 32 phases per job under the server executor identity without a runner claim.

#### Scenario: Write-back phases
- **WHEN** Developer creates a job with executor write-back for one unit reference.
- **THEN** The job runs its ordered phase list to terminal state under the server executor identity.

### Requirement: Sealed secrets

When a job holds secret inputs, the server SHALL seal each value with the data key of the namespace before storage and mask plain text in logs, reads, and audit entries.

#### Scenario: Secrets sealed
- **WHEN** Developer creates a job with secret inputs with at most 64 inputs per job.
- **THEN** Logs, reads, and audit entries hold sealed values only and plain text never shows.

### Requirement: Read and list jobs

When a principal with `namespace:read` then `unit:read` reads a job, the server SHALL return that job with sealed secrets only.

#### Scenario: Owner reads job
- **WHEN** Principal with namespace read then unit read reads a job of its namespace.
- **THEN** The server returns the job with sealed secrets only.

#### Scenario: Cross-namespace denied
- **WHEN** Principal reads a job of another namespace.
- **THEN** The server denies with 403.
