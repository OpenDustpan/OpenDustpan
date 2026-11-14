## ADDED Requirements

### Requirement: Start job

The server SHALL make a queued job for one unit when a principal with the run action posts a start.

#### Scenario: Start a plan job
- **WHEN** a principal with the run action posts a start for one unit
- **THEN** the server makes a queued job and writes an audit entry with actor and reason for the start

### Requirement: Freeze refusal

When a principal posts a start while an active freeze blocks the start, the server SHALL refuse the start with 403 job.frozen.

#### Scenario: Start under an active freeze
- **WHEN** a principal posts a start while the namespace has an active freeze that blocks the start
- **THEN** the server refuses the start with 403 job.frozen

### Requirement: Break-glass start

When a start carries the break-glass flag and reason while a freeze is active, the server SHALL apply the namespace break-freeze rule.

#### Scenario: Break-glass start blocked by rule
- **WHEN** a start carries the break-glass flag and reason while a freeze blocks the start
- **THEN** the server applies the namespace break-freeze rule and refuses the start with 403 job.frozen

### Requirement: List jobs

The server SHALL return the jobs of a namespace or of one unit in reverse start order.

#### Scenario: List jobs of a unit
- **WHEN** a principal with the read action lists jobs with a unit filter
- **THEN** the server returns only jobs of that unit in reverse start order

#### Scenario: List jobs of a namespace
- **WHEN** a principal with the read action lists jobs with no unit filter
- **THEN** the server returns only jobs of that namespace in reverse start order

#### Scenario: List with unreadable unit filter
- **WHEN** a principal without the read action on the unit lists jobs with that unit filter
- **THEN** the server refuses the list with 403 job.forbidden

### Requirement: Get job

The server SHALL return one job with its state and timeline to a principal with the read action.

#### Scenario: Read one job
- **WHEN** a principal with the read action reads one job
- **THEN** the server returns the job with its state, timeline, and masked plan diff

### Requirement: Cancel job

When a principal with the run action cancels a queued job, the server SHALL move the job to canceled.

#### Scenario: Cancel a queued job
- **WHEN** a principal with the run action cancels a queued job
- **THEN** the server moves the job to canceled and writes an audit entry for the cancel

#### Scenario: Cancel a finished job
- **WHEN** a principal cancels a job that already reached a final state
- **THEN** the server refuses the cancel with 409 job.state and names the current state

### Requirement: Approve job

When a principal with the approve action approves a job that waits for approval, the server SHALL record the approval with actor and time.

#### Scenario: Approve a waiting job
- **WHEN** a principal with the approve action approves a job that waits for approval
- **THEN** the server records the approval with actor and time and writes an audit entry

#### Scenario: Approve without the action
- **WHEN** a principal without the approve action approves a job that waits for approval
- **THEN** the server refuses the approval with 403 job.forbidden

### Requirement: Stale plan refusal

When the shown plan revision differs from the current plan revision, the server SHALL refuse the approval.

#### Scenario: Approve after a new plan
- **WHEN** the shown plan revision differs from the current plan revision of the job
- **THEN** the server refuses the approval with 409 job.state and names the current plan revision

### Requirement: Reject job

When a principal with the approve action rejects a job that waits for approval, the server SHALL record the rejection with actor and time.

#### Scenario: Reject a waiting job
- **WHEN** a principal with the approve action rejects a job that waits for approval
- **THEN** the server records the rejection with actor, time, and reason and writes an audit entry

### Requirement: Confirm job

When a principal with the run action confirms a job that waits for confirmation, the server SHALL record the confirmation.

#### Scenario: Confirm a job step
- **WHEN** a principal with the run action confirms a job that waits for confirmation
- **THEN** the server records the confirmation and releases the wait

### Requirement: Redact job log

When a principal with the manage action posts a redact log request, the server SHALL replace each match in the named log version with the marker.

#### Scenario: Redact a secret from logs
- **WHEN** a principal with the manage action posts a redact log request with substitutions
- **THEN** the server replaces each match in the named log version with the marker and writes an audit entry for the redaction

### Requirement: Redact validation

When a redact log request carries an empty match, the server SHALL refuse the request.

#### Scenario: Redact with an empty match
- **WHEN** a redact log request carries a substitution with an empty match or a hostile pattern
- **THEN** the server refuses the request with 400 and keeps the log version unchanged

### Requirement: Repeat start

When a principal repeats a start with the same Idempotency-Key, the server SHALL return the first job.

#### Scenario: Repeat start with the same key
- **WHEN** a principal repeats a start with the same Idempotency-Key
- **THEN** the server returns the first job without making a second job
