## ADDED Requirements

### Requirement: startJob plan-only job

When a principal with only the planner role starts a job with operation plan, the server SHALL create the job.

#### Scenario: Planner starts plan
- **WHEN** a principal with only the planner role starts a job with operation plan
- **THEN** the server creates the plan job and queues it for a runner.

### Requirement: startJob refuse apply and destroy

When a principal with only the planner role starts a job with operation apply or operation destroy, the server SHALL answer 403 and create no job.

#### Scenario: Planner starts apply
- **WHEN** a principal with only the planner role starts a job with operation apply
- **THEN** the server answers 403 and creates no job.

#### Scenario: Planner starts destroy
- **WHEN** a principal with only the planner role starts a job with operation destroy
- **THEN** the server answers 403 and creates no job.

#### Scenario: Planner approves
- **WHEN** a principal with only the planner role approves a job
- **THEN** the server answers 403 and records no approval.

#### Scenario: Planner reads secrets
- **WHEN** a principal with only the planner role reads a secret value through the secrets API
- **THEN** the server answers 403 and returns no secret value.

#### Scenario: Cross-namespace start
- **WHEN** a planner of one namespace starts a plan job in another namespace
- **THEN** the server answers 403 and creates no job.

### Requirement: Refusal shape

When the server refuses a planner apply or destroy job, the server SHALL answer error code job.forbidden with text that names the missing action and the action that allows it.

#### Scenario: Apply refusal body
- **WHEN** the server refuses a planner apply job
- **THEN** the body carries error code job.forbidden and names the missing action and the action that allows the apply.

### Requirement: Secrets by pinned SHA

When a planner plan job starts, the server SHALL pin the commit SHA from the request and open unit secret values only when that SHA is on a protected branch.

#### Scenario: Unprotected commit stays masked
- **WHEN** a planner plan job names a commit SHA that is not on a protected branch
- **THEN** the plan runs with the masked profile and the log holds no secret value.

#### Scenario: Protected commit opens secrets
- **WHEN** a planner plan job names a commit SHA on a protected branch
- **THEN** the job opens the unit secrets for the run.

#### Scenario: Unreadable branch check stays masked
- **WHEN** the branch protection read errors or times out
- **THEN** the plan runs with the masked profile and the log holds no secret value.

#### Scenario: No commit stays masked
- **WHEN** a planner plan job names no commit
- **THEN** the plan runs with the masked profile and the log holds no secret value.

#### Scenario: Unknown SHA stays masked
- **WHEN** a planner plan job names a commit SHA that the forge does not know
- **THEN** the plan runs with the masked profile and the log holds no secret value.

#### Scenario: Unit with no secrets
- **WHEN** a planner plan job names a unit with no secrets
- **THEN** the plan runs with the masked profile and succeeds.

### Requirement: readPlan masked plan

When a holder of job:read-plans reads the plan output of a finished plan job, the server SHALL answer the masked plan with no secret value.

#### Scenario: Planner reads finished plan
- **WHEN** a holder of job:read-plans reads the plan output of a finished plan job
- **THEN** the server answers the masked plan with no secret value.

#### Scenario: Grant removed after queue
- **WHEN** the planner grant is removed after the plan job is queued
- **THEN** later plan reads give 403 and the queued job stays queued.

### Requirement: Author change order

Where the planner role is granted, the server SHALL give the holder changeorder:author through a stored grant.

#### Scenario: Planner authors change order
- **WHEN** a principal with only the planner role creates a change order
- **THEN** the server allows the create through the stored planner grant.

### Requirement: Audit in same transaction

When the server decides a planner job start, the server SHALL write the audit entry in the same store transaction as the queue insert.

#### Scenario: Start writes audit
- **WHEN** the server decides a planner job start
- **THEN** the audit entry names the starter, the operation and the decision in the same transaction.

### Requirement: Refuse unknown operation

When a principal starts a job with an unknown operation, the server SHALL answer 400 and create no job.

#### Scenario: Unknown operation
- **WHEN** a principal starts a job with an unknown operation
- **THEN** the server answers 400 and creates no job.
