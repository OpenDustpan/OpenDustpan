## ADDED Requirements

### Requirement: Pulumi approval wait

When an approval default requires approval, the server SHALL hold the Pulumi update in approval wait until the required approvals arrive.

#### Scenario: Pulumi apply waits then continues
- **WHEN** a Pulumi apply starts while an approval default requires approval
- **THEN** the update stays in approval wait and continues after the required approvals meet the default

### Requirement: OpenTofu approval wait

When an approval default requires approval, the server SHALL hold the OpenTofu run in approval wait until the required approvals arrive.

#### Scenario: OpenTofu apply waits then continues
- **WHEN** an OpenTofu apply starts while an approval default requires approval
- **THEN** the run stays in approval wait and continues after the required approvals meet the default

### Requirement: No default needs no approval

When no approval default and no approval gate applies, the server SHALL start the apply without approval wait.

#### Scenario: Apply without default proceeds
- **WHEN** an apply starts with no approval default and no approval gate
- **THEN** the job proceeds without approval wait

### Requirement: Recheck at apply

When a new requirement appears at change.apply, the server SHALL put the change back into approval wait.

#### Scenario: New default after start blocks apply
- **WHEN** a default is saved after a job starts and before the apply dispatch
- **THEN** the change returns to approval wait with the new requirement named

### Requirement: Recheck only adds

When a requirement is removed while a job is in approval wait, the server SHALL keep the stored requirement set until the recheck adds no requirement.

#### Scenario: Removed default does not release wait
- **WHEN** an approval default is removed while a job is in approval wait
- **THEN** the job stays in approval wait until the stored requirement set is met

### Requirement: Serialized recheck

When the server rechecks at change.apply, the server SHALL hold the apply dispatch until the evaluation and signature check complete as one step.

#### Scenario: Concurrent approval and dispatch
- **WHEN** two approvals or a default save arrive during the apply recheck
- **THEN** only one wins and the other sees the latest requirement set or the dispatched state

### Requirement: Deny and error fail closed

When a deny matches or an enforce-mode gate errors, the server SHALL stop the change with the gate message in the decision record.

#### Scenario: Deny refuses with message
- **WHEN** a deny gate matches at a job decision point
- **THEN** the job stops with the message of each deny

#### Scenario: Engine error stops enforce
- **WHEN** the engine reports a diagnostic error for a gate in enforce mode
- **THEN** the change stops with the error result and the Cedar message

### Requirement: Unavailable facts fail closed

When a needed fact is unavailable, the server SHALL fail closed instead of skipping the approval wait.

#### Scenario: Missing fact blocks apply
- **WHEN** a needed fact is unavailable under an approval default
- **THEN** the job stays in approval wait or stops per the built-in gate instead of proceeding

### Requirement: Evaluate mode records no block

When the only matching approval default is in evaluate mode, the server SHALL record the outcome without approval wait.

#### Scenario: Evaluate default does not wait
- **WHEN** the only matching default is in evaluate mode
- **THEN** the job proceeds and the record shows the evaluated outcome with no effect

### Requirement: Wait survives restart

When the server restarts during approval wait, the server SHALL restore the wait with its stored signatures.

#### Scenario: Restart keeps wait
- **WHEN** the server restarts while a job is in approval wait
- **THEN** the job resumes approval wait with stored signatures intact

### Requirement: Plan rights refused

When a principal without the apply action starts an apply job, the server SHALL refuse with 403.

#### Scenario: Planner apply refused
- **WHEN** a principal with only plan rights starts an apply job
- **THEN** the server returns 403 with the required action

### Requirement: Decision record names gate

When the server evaluates a job decision point, the server SHALL write a decision record with each gate version and the fact schema version.

#### Scenario: Record holds versions
- **WHEN** a job is evaluated at a decision point
- **THEN** the record holds each evaluated gate version and the fact schema version
