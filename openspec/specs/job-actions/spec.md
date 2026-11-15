# job-actions Specification

## Purpose
TBD - created by archiving change add-job-service-routes-start-1. Update Purpose after archive.

## Requirements

### Requirement: Cancel job

When a principal with the run action cancels a job that has not reached a final state, the server SHALL move the job to canceled.

#### Scenario: Cancel a queued job
- **WHEN** a principal with the run action cancels a queued job
- **THEN** the job is canceled and an audit entry is written

#### Scenario: Cancel a finished job
- **WHEN** a principal cancels a finished job
- **THEN** the server answers 409 job.wrong-state

### Requirement: Approve job

When a principal with the approve action approves a job that waits for approval, the server SHALL record the approval with actor, time and plan revision.

#### Scenario: Approve a waiting job
- **WHEN** a principal with the approve action approves a waiting job
- **THEN** the approval is recorded with an audit entry

#### Scenario: Approve without the action
- **WHEN** a principal without the action approves
- **THEN** the server answers 403 access.denied

### Requirement: Stale plan refusal

If the shown plan revision differs from the current plan revision, then the server SHALL refuse the approval.

#### Scenario: Approve after a new plan
- **WHEN** the shown revision is old
- **THEN** the server answers 409 job.wrong-state

### Requirement: Reject job

When a principal with the approve action rejects a job that waits for approval, the server SHALL record the rejection with actor and time.

#### Scenario: Reject a waiting job
- **WHEN** a principal with the approve action rejects
- **THEN** the rejection and an audit entry are recorded

### Requirement: Confirm job

When a principal with the run action confirms a job that waits for confirmation, the server SHALL record the confirmation.

#### Scenario: Confirm a step
- **WHEN** a principal confirms a waiting job
- **THEN** the confirmation is recorded

#### Scenario: Confirm a job that does not wait
- **WHEN** a principal confirms such a job
- **THEN** the server answers 409 job.wrong-state

### Requirement: Redact job log

When a principal with the manage action posts a redact request, the server SHALL replace each literal match in the named log version with the marker.

#### Scenario: Redact a secret
- **WHEN** a principal with the manage action redacts
- **THEN** each match is replaced and an audit entry is written

#### Scenario: Unknown version
- **WHEN** the log version has no row
- **THEN** the server answers 404 job.version-not-found

### Requirement: Redact validation

If a redact request carries an empty match, then the server SHALL refuse the request.

#### Scenario: Empty match
- **WHEN** a substitution has an empty match
- **THEN** the server answers 400 and keeps the log unchanged

### Requirement: Break-glass start

When a start carries the break-glass flag and a reason, the server SHALL apply the namespace break-freeze rule.

#### Scenario: Blocked by rule
- **WHEN** a freeze blocks the start
- **THEN** the server answers 403 job.frozen
