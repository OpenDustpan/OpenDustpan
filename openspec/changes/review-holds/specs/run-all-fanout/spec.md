## ADDED Requirements

### Requirement: Block fan-out apply before approval

When a fan-out change order lacks a stored apply request for the current plan revision, the server SHALL refuse to start each apply job.

#### Scenario: Apply start before apply request
- **WHEN** a runner tries to start a fan-out apply job before the stored apply request
- **THEN** the server refuses to start each apply job with 403 and the NEW error code change-order.apply-gated and records no provider apply call

#### Scenario: Failed plan blocks request
- **WHEN** one plan job of the revision failed
- **THEN** the server refuses the apply request and starts no apply job

### Requirement: Start fan-out apply after approval

When a stored apply request names the current plan revision, the server SHALL start each fan-out apply job for that revision.

#### Scenario: Apply start after apply request
- **WHEN** the stored apply request names the current plan revision
- **THEN** the server starts each apply job only for the named plan revision and each unit writes its own state version

#### Scenario: Closed change order
- **WHEN** the change order is closed or cancelled
- **THEN** the server refuses the apply request and starts no apply job

### Requirement: Require authorized second approval

When the caller lacks the approve action on the change order or lacks a distinct second approval, the server SHALL refuse the apply request.

#### Scenario: Missing grant
- **WHEN** the caller lacks the approve action on the change order
- **THEN** the server refuses the apply request with 403 and writes an audit entry

#### Scenario: Self approval
- **WHEN** the person who wrote the change requests the apply without a distinct second principal that holds the approve action and meets the signature rules
- **THEN** the server refuses the apply request with 403

### Requirement: Void pending starts on new revision

When a new plan revision lands after a stored apply request, the server SHALL void that apply request for pending apply job starts.

#### Scenario: Stale approval
- **WHEN** a new plan revision lands after the stored apply request
- **THEN** the server voids the stored apply request for pending jobs and refuses new apply job starts for the old revision

#### Scenario: Running jobs at void time
- **WHEN** a new plan revision lands while apply jobs of the old revision run
- **THEN** already running apply jobs continue with the old revision mark and no new unit starts

### Requirement: Start each unit once

When the job start path fails to load the stored approval state, the server SHALL refuse to start the apply job.

#### Scenario: Duplicate apply request
- **WHEN** the same apply request arrives twice for one plan revision
- **THEN** the server returns the stored apply request result and starts each unit once

#### Scenario: Retry after rejection
- **WHEN** a runner retries an apply job start after a gated rejection
- **THEN** the server refuses again with 403 and starts the unit zero times

#### Scenario: Runner restart mid fan-out
- **WHEN** a runner restarts in the middle of a fan-out with some units already started
- **THEN** the server starts only units with no started apply job for the named revision

### Requirement: Show gated state and helper

Where fan-out apply jobs are gated, the server SHALL show the pending apply request and its effective value in the change order read API.

#### Scenario: Exact gated message
- **WHEN** a runner tries to start a gated apply job
- **THEN** the 403 response carries the NEW error code change-order.apply-gated and names the NEW command opendustpan change-order apply-request --id <id>

#### Scenario: Empty plan unit
- **WHEN** one unit of the approved revision has an empty plan and other units have changes
- **THEN** the unit skips its apply job with no new state version while other units start
