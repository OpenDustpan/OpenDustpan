## MODIFIED Requirements

### Requirement: Cancel job

When a principal with the action `job:cancel` cancels a job that has not reached a final state, the server SHALL move the job to canceled.

#### Scenario: Cancel a queued job
- **WHEN** a principal with the action `job:cancel` cancels a queued job
- **THEN** the job is canceled and an audit entry is written

#### Scenario: Cancel a finished job
- **WHEN** a principal cancels a finished job
- **THEN** the server answers 409 job.wrong-state

### Requirement: Approve job

When a principal with the action `job:approve` approves a job that waits for approval, the server SHALL record the approval with actor, time and plan revision.

#### Scenario: Approve a waiting job
- **WHEN** a principal with the action `job:approve` approves a waiting job
- **THEN** the approval is recorded with an audit entry

#### Scenario: Approve without the action
- **WHEN** a principal without the action approves
- **THEN** the server answers 403 access.denied

### Requirement: Confirm job

When a principal with the start action of the job on its unit confirms a job that waits for confirmation, the server SHALL record the confirmation. The start action is `unit:run-plan`, `unit:run-apply`, `unit:run-destroy` or `unit:write-back`, as the kind of the job says.

#### Scenario: Confirm a step
- **WHEN** a principal with the start action of the job confirms a waiting job
- **THEN** the confirmation is recorded

#### Scenario: Confirm a job that does not wait
- **WHEN** a principal confirms such a job
- **THEN** the server answers 409 job.wrong-state

### Requirement: Redact job log

When a principal with the action `unit:redact` posts a redact request, the server SHALL replace each literal match in the named log version with the marker.

#### Scenario: Redact a secret
- **WHEN** a principal with the action `unit:redact` redacts
- **THEN** each match is replaced and an audit entry is written

#### Scenario: Unknown version
- **WHEN** the log version has no row
- **THEN** the server answers 404 job.not-found
