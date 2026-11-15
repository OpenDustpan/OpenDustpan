## ADDED Requirements

### Requirement: Fenced writes

If a write to the sweep store carries a fence that differs from the live fence of its namespace, then the server SHALL refuse the write with namespace.ErrFenced.

#### Scenario: Stale fence
- **WHEN** a write carries a stale failover version
- **THEN** the store returns namespace.ErrFenced and changes nothing

#### Scenario: Equal fence
- **WHEN** a write carries the live failover version
- **THEN** the store accepts the write

#### Scenario: Missing or deleted namespace
- **WHEN** a write names a namespace with no row or a soft-deleted namespace
- **THEN** the store refuses the write with namespace.ErrFenced and changes nothing

### Requirement: Atomic fence check

While a write runs, the server SHALL read the fence and change the rows in one transaction.

#### Scenario: Concurrent fence bump
- **WHEN** the failover version changes while a write is in flight
- **THEN** the store refuses the write with namespace.ErrFenced and no partial row stays

#### Scenario: Forced error
- **WHEN** a write fails after the fence check
- **THEN** the transaction rolls back and no row changes

### Requirement: Operator sweep pool

If a sweep target names a pool that is not an operator pool with the sweep purpose, then the server SHALL refuse the write.

#### Scenario: Jobs pool
- **WHEN** a target names a pool with the jobs purpose
- **THEN** the store refuses the write with a pool-purpose error

#### Scenario: Namespace sweep pool
- **WHEN** a target names a namespace pool with the sweep purpose
- **THEN** the store refuses the write with a pool-purpose error

#### Scenario: Operator sweep pool accepted
- **WHEN** a target names an operator pool with the sweep purpose
- **THEN** the store accepts the write

### Requirement: Pool checked at start

If the pool of a target is deleted or is no longer an operator pool with the sweep purpose, then the server SHALL refuse the start of a sweep.

#### Scenario: Deleted pool
- **WHEN** a sweep starts on a target whose pool is deleted
- **THEN** the store refuses the start and creates no sweep

#### Scenario: Changed purpose
- **WHEN** a sweep starts on a target whose pool now has the jobs purpose
- **THEN** the store refuses the start with a pool-purpose error

### Requirement: Finding bounds

If a call to Record holds more than 1,000 observations, then the server SHALL refuse the call.

#### Scenario: Oversize batch
- **WHEN** a caller sends 1,001 observations to Record
- **THEN** the store refuses the call and records nothing

### Requirement: Page bound

If a list call asks for more than 500 findings in one page, then the server SHALL refuse the call.

#### Scenario: Oversize page
- **WHEN** a caller asks for a page of 501 findings
- **THEN** the store refuses the call

### Requirement: One open sweep

If a sweep starts on a target that has a sweep not in a final status, then the server SHALL refuse the start with ErrScanBusy.

#### Scenario: Busy target
- **WHEN** a sweep starts on a target with a running sweep
- **THEN** the store refuses the second start with ErrScanBusy

#### Scenario: Concurrent starts
- **WHEN** two sweeps start at the same time on one target
- **THEN** exactly one start succeeds

#### Scenario: Ended sweep
- **WHEN** the last sweep of the target has the status succeeded, failed, canceled or expired
- **THEN** the store accepts a new start

### Requirement: Sweep transitions

If AdvanceScan receives an event that the transition table does not allow for the status, then the server SHALL refuse the call with ErrTransition.

#### Scenario: Illegal event
- **WHEN** a succeeded sweep receives the event of a start
- **THEN** the store returns ErrTransition and keeps the status

#### Scenario: Expiry
- **WHEN** a dispatched sweep receives the expire event
- **THEN** the sweep has the status expired

### Requirement: Finding action

If an action names a status that differs from the stored status of the finding, then the server SHALL refuse the action with ErrStatusChanged.

#### Scenario: Changed status
- **WHEN** a finding changed to ignored after a person read it as open
- **THEN** the store returns ErrStatusChanged and writes no audit entry

#### Scenario: Audit in one transaction
- **WHEN** an action succeeds
- **THEN** the status and the audit entry are stored together

### Requirement: Target delete

If a target has a sweep not in a final status, then the server SHALL refuse the delete of the target.

#### Scenario: Open sweep
- **WHEN** a caller deletes a target with a running sweep
- **THEN** the store refuses the delete and keeps the target

#### Scenario: Duplicate name
- **WHEN** a caller creates a second target with the same name in a namespace
- **THEN** the store refuses the write with the exists error

### Requirement: Stable identity

While a row of the sweep store exists, the server SHALL key the row by the namespace ID and store a pool by its pool ID.

#### Scenario: Rename
- **WHEN** a namespace is renamed
- **THEN** the rows stay reachable by namespace ID

### Requirement: Backup coverage

When a backup runs, the server SHALL include the rows of the sweep tables.

#### Scenario: Round trip
- **WHEN** a backup and a restore run on a store with targets, sweeps, findings and rules
- **THEN** the restored store holds the same rows
