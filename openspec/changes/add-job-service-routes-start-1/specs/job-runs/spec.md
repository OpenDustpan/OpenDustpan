## ADDED Requirements

### Requirement: Start job

When a principal with the run action posts a start, the server SHALL make a queued job for one unit.

#### Scenario: Start a plan job
- **WHEN** a principal with the run action posts a start for one unit
- **THEN** the server makes a queued job and writes an audit entry with actor and reason for the start

### Requirement: Empty revision start

When a start gives no revision, the server SHALL leave Location.Revision empty.

#### Scenario: Start with no revision
- **WHEN** a start gives no revision
- **THEN** the server leaves Location.Revision empty and makes the queued job

### Requirement: Freeze refusal

When a principal posts a start while an active freeze blocks the start, the server SHALL refuse the start with 403 job.frozen.

#### Scenario: Start under an active freeze
- **WHEN** a principal posts a start while the namespace has an active freeze that blocks the start
- **THEN** the server refuses the start with 403 job.frozen

### Requirement: Break-glass start

When a start carries the break-glass flag and reason, the server SHALL apply the namespace break-freeze rule.

#### Scenario: Permitted break-glass start
- **WHEN** a start carries the break-glass flag and reason and the namespace break-freeze rule allows the start
- **THEN** the server starts the job and marks the audit entry as break-glass with actor and reason

#### Scenario: Break-glass start blocked by rule
- **WHEN** a start carries the break-glass flag and reason while a freeze blocks the start
- **THEN** the server refuses the start with 403 job.frozen

### Requirement: Idempotent start

When a principal repeats a start with the same Idempotency-Key and the same body, the server SHALL return the first job.

#### Scenario: Repeat start with the same key
- **WHEN** a principal repeats a start with the same Idempotency-Key and the same body
- **THEN** the server returns the first job without making a second job

#### Scenario: Reused key with a different body
- **WHEN** a principal repeats an Idempotency-Key with a different body
- **THEN** the server refuses the repeat with 409

#### Scenario: Key scoped per namespace and principal
- **WHEN** the same key value arrives in another namespace or from another principal
- **THEN** the server treats the key as new in the other namespace or for the other principal

#### Scenario: Concurrent first calls with one key
- **WHEN** two first calls with one key race
- **THEN** the server makes one job row for the key

### Requirement: List jobs

When a principal with the read action lists jobs, the server SHALL return the jobs of a namespace or of one unit in reverse start order.

#### Scenario: List jobs of a namespace
- **WHEN** a principal with the read action lists jobs with no unit filter
- **THEN** the server returns only jobs of that namespace in reverse start order with a next cursor

#### Scenario: List jobs of a unit
- **WHEN** a principal with the read action lists jobs with a unit filter
- **THEN** the server returns only jobs of that unit in reverse start order

#### Scenario: List with unreadable unit filter
- **WHEN** a principal without the read action on the unit lists jobs with that unit filter
- **THEN** the server refuses the list with 403 job.forbidden

#### Scenario: List without auth
- **WHEN** a caller lists jobs without a bearer access token
- **THEN** the server refuses the list with 401

### Requirement: Get job

When a principal with the read action reads one job, the server SHALL return the job with its state.

#### Scenario: Read one job
- **WHEN** a principal with the read action reads one job
- **THEN** the server returns the job with its state

#### Scenario: Read an unknown job
- **WHEN** a principal reads a job id that has no row
- **THEN** the server refuses the read with 404 job.not-found

### Requirement: Job timeline route

When a principal with the read action reads the timeline route of one job, the server SHALL return the ordered timeline.

#### Scenario: Read the timeline
- **WHEN** a principal with the read action reads the timeline route of one job
- **THEN** the server returns the ordered phases with masked secret values

### Requirement: Masked plan diff

When a principal reads the plan diff of a job, the server SHALL mask each secret value in the plan diff with the marker.

#### Scenario: Read a plan diff with a secret
- **WHEN** a principal with the read action reads the plan diff of a job whose plan holds a secret
- **THEN** the server replaces each secret value with the marker and keeps the shape

#### Scenario: Read a job with no plan
- **WHEN** a principal reads the plan diff of a job with no stored plan
- **THEN** the server returns an empty diff and the job state

### Requirement: Cancel job

When a principal with the run action cancels a job that has not reached a final state, the server SHALL move the job to canceled.

#### Scenario: Cancel a queued job
- **WHEN** a principal with the run action cancels a queued job
- **THEN** the server moves the job to canceled and writes an audit entry for the cancel

#### Scenario: Cancel a running job
- **WHEN** a principal with the run action cancels a running job
- **THEN** the server moves the job to canceled, asks the runner to stop the claim, and writes an audit entry

#### Scenario: Cancel race
- **WHEN** two cancels race on one queued job
- **THEN** the first cancel moves the job to canceled and the second cancel returns the canceled job with no second audit entry

#### Scenario: Cancel a finished job
- **WHEN** a principal cancels a job that already reached a final state
- **THEN** the server refuses the cancel with 409 job.state and names the current state

### Requirement: Approve job

When a principal with the approve action approves a job that waits for approval, the server SHALL record the approval with actor and time.

#### Scenario: Approve a waiting job
- **WHEN** a principal with the approve action approves a job that waits for approval
- **THEN** the server records the approval with actor, time, and plan revision and writes an audit entry

#### Scenario: Approve without the action
- **WHEN** a principal without the approve action approves a job that waits for approval
- **THEN** the server refuses the approval with 403 job.forbidden

#### Scenario: Approve a job that does not wait
- **WHEN** a principal approves a job that does not wait for approval
- **THEN** the server refuses the approval with 409 job.state

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

#### Scenario: Reject without the action
- **WHEN** a principal without the approve action rejects a job that waits for approval
- **THEN** the server refuses the rejection with 403 job.forbidden

### Requirement: Confirm job

When a principal with the run action confirms a job that waits for confirmation, the server SHALL record the confirmation.

#### Scenario: Confirm a job step
- **WHEN** a principal with the run action confirms a job that waits for confirmation
- **THEN** the server records the confirmation and releases the wait

#### Scenario: Confirm a job that does not wait
- **WHEN** a principal confirms a job that does not wait for confirmation
- **THEN** the server refuses the confirmation with 409 job.state

#### Scenario: Confirm without the action
- **WHEN** a principal without the run action confirms a job that waits for confirmation
- **THEN** the server refuses the confirmation with 403 job.forbidden

### Requirement: Redact job log

When a principal with the manage action posts a redact log request, the server SHALL replace each literal match in the named log version with the marker.

#### Scenario: Redact a secret from logs
- **WHEN** a principal with the manage action posts a redact log request with substitutions
- **THEN** the server replaces each literal match in the named log version with the marker and writes an audit entry for the redaction

#### Scenario: Redact without the action
- **WHEN** a principal without the manage action posts a redact log request
- **THEN** the server refuses the redaction with 403 job.forbidden

#### Scenario: Redact an unknown version
- **WHEN** a principal posts a redact log request for a log version that has no row
- **THEN** the server refuses the redaction with 404 job.version-not-found

#### Scenario: Redact a growing log
- **WHEN** a principal redacts a running job whose log still grows
- **THEN** the server redacts the stored chunks of the named version and marks later chunks of that version for the same substitutions

#### Scenario: Redact a secret split across chunks
- **WHEN** a secret value spans a log chunk boundary
- **THEN** the server replaces the match that spans the chunk boundary with the marker

### Requirement: Redact validation

When a redact log request carries an empty match, the server SHALL refuse the request.

#### Scenario: Redact with an empty match
- **WHEN** a redact log request carries a substitution with an empty match
- **THEN** the server refuses the request with 400 and keeps the log version unchanged

#### Scenario: Redact with an oversize list
- **WHEN** a redact log request carries more substitutions or longer matches than the limit allows
- **THEN** the server refuses the request with 400 and keeps the log version unchanged
