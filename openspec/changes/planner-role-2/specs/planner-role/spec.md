## ADDED Requirements

### Requirement: Start plan-only job

When a principal with only the planner role starts a job with operation plan, the server SHALL create the job.

#### Scenario: Planner starts plan
- **WHEN** a principal with only the planner role starts a job with operation plan
- **THEN** the server creates the plan job and queues it for a runner.

### Requirement: Refuse apply and destroy

When a principal with only the planner role starts a job with operation apply or operation destroy, the server SHALL answer 403 and create no job.

#### Scenario: Planner starts apply
- **WHEN** a principal with only the planner role starts a job with operation apply
- **THEN** the server answers 403 and creates no job.

#### Scenario: Planner starts destroy
- **WHEN** a principal with only the planner role starts a job with operation destroy
- **THEN** the server answers 403 and creates no job.

### Requirement: Refusal shape

When the server refuses a planner apply or destroy job, the server SHALL answer error code job.forbidden with text that names the missing action and the role that holds it.

#### Scenario: Apply refusal body
- **WHEN** the server refuses a planner apply job
- **THEN** the body carries error code job.forbidden and names the missing action and the role that holds it.

### Requirement: Secrets by named commit

When a planner plan job names a commit, the server SHALL read the branch protection of that commit at job start and open unit secret values only when that commit is on a protected branch.

#### Scenario: Unprotected commit stays masked
- **WHEN** a planner plan job names a commit that is not on a protected branch
- **THEN** the plan runs with the masked profile and the log holds no secret value.

#### Scenario: Protected commit opens secrets
- **WHEN** a planner plan job names a commit on a protected branch
- **THEN** the job opens the unit secrets for the run.

#### Scenario: Unreadable branch check stays masked
- **WHEN** the branch protection read errors or times out
- **THEN** the plan runs with the masked profile and the log holds no secret value.

#### Scenario: Unit with no secrets
- **WHEN** a planner plan job names a unit with no secrets
- **THEN** the plan runs with the masked profile and succeeds.

### Requirement: Read masked plan

When a principal with only the planner role reads the plan output of a finished plan job, the server SHALL answer the masked plan with no secret value.

#### Scenario: Planner reads finished plan
- **WHEN** a principal with only the planner role reads the plan output of a finished plan job
- **THEN** the server answers the masked plan with no secret value.

### Requirement: Author change order

Where the planner role is granted, the server SHALL give the holder changeorder:author through a stored grant.

#### Scenario: Planner authors change order
- **WHEN** a principal with only the planner role creates a change order
- **THEN** the server allows the create through the stored planner grant.

### Requirement: Check grants at start and read

When a planner plan job is queued and its grant is then removed, the server SHALL refuse later plan reads and keep the queued job unchanged.

#### Scenario: Grant removed after queue
- **WHEN** the planner grant is removed after the plan job is queued
- **THEN** later plan reads give 403 and the queued job stays queued.

### Requirement: Audit follow-up

When the audit write after the job commit fails, the server SHALL keep the job decision and not roll back the queued job.

#### Scenario: Audit write fails
- **WHEN** the audit write after the job commit fails
- **THEN** the job stays queued and the decision stands.

### Requirement: Refuse unknown operation

When a principal starts a job with an unknown operation, the server SHALL answer 400 and create no job.

#### Scenario: Unknown operation
- **WHEN** a principal starts a job with an unknown operation
- **THEN** the server answers 400 and creates no job.
