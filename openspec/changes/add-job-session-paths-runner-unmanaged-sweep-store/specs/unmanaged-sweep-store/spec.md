## ADDED Requirements

### Requirement: Fenced writes

When a write to the sweep store carries a fence that differs from the live fence of its namespace, the server SHALL refuse the write with namespace.ErrFenced and change nothing.

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
- **THEN** the write either commits with the old version before the change or is refused, and no partial row stays

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

#### Scenario: Operator sweep pool
- **WHEN** a target names an operator pool with the sweep purpose
- **THEN** the store accepts the write

### Requirement: Pool of the namespace

If a sweep target names a pool that does not belong to the namespace of the target, then the server SHALL refuse the write.

#### Scenario: Foreign pool
- **WHEN** a target names a pool of another namespace
- **THEN** the store refuses the write and changes nothing

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

#### Scenario: Record and list
- **WHEN** a caller records findings and lists them
- **THEN** the store answers the recorded findings within the page bound

### Requirement: One open sweep

If a sweep starts on a target that has a sweep not in a final status, then the server SHALL refuse the start with ErrScanBusy.

#### Scenario: Busy target
- **WHEN** a sweep starts on a target with a running sweep
- **THEN** the store refuses the second start with ErrScanBusy

#### Scenario: Ended sweep
- **WHEN** the last sweep of the target has the status succeeded, failed, canceled or expired
- **THEN** the store accepts a new start

### Requirement: Stable identity

While a row of the sweep store exists, the server SHALL key the row by the namespace ID.

#### Scenario: Rename
- **WHEN** a namespace is renamed
- **THEN** the rows stay reachable by namespace ID

### Requirement: Backup coverage

When a backup runs, the server SHALL include the rows of the sweep tables.

#### Scenario: Round trip
- **WHEN** a backup and a restore run on a store with targets, sweeps, findings and rules
- **THEN** the restored store holds the same rows
