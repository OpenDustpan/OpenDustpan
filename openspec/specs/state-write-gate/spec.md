# state-write-gate Specification

## Purpose
The state-write gate decides if a principal may write the state of a unit. This capability covers how the server reads the deleted-unit facts for that decision on SQLite. It does not cover the gate rules or the facts of a unit that exists.

## Requirements

### Requirement: Snapshot deleted-unit facts

When the server reads deleted-unit facts for state.write on SQLite, the server SHALL read all rows inside one read-only transaction.

#### Scenario: Concurrent commit during fact read
- **WHEN** a commit lands while the server reads deleted-unit facts for state.write on SQLite
- **THEN** the gate uses facts from one snapshot and never mixes rows from before and after the commit.

#### Scenario: Failed read transaction
- **WHEN** the read-only transaction begin, select, or rollback fails or the context is canceled
- **THEN** the gate returns the store error, uses no partial facts, and refuses the write that needed the facts.

#### Scenario: Select and rollback both fail
- **WHEN** a select fails and the rollback also fails
- **THEN** the method closes the transaction and returns the select error.

### Requirement: Start without gate service

When the server starts with no gate service, the server SHALL write one WARN log line with the fixed message "state.write gates: off" and the attribute `reason`, as recordStateWriteGates does on main.

#### Scenario: Server starts with no gate service
- **WHEN** the server starts with no gate service configured
- **THEN** the log holds one WARN line "state.write gates: off" with its reason, and a server with a gate service logs the INFO line "state.write gates: on" and no WARN line.

#### Scenario: Repeat start with no gate service
- **WHEN** the process starts twice with no gate service
- **THEN** each start logs the line once and no start logs it twice.

#### Scenario: Log write failure at start
- **WHEN** the start-up log write fails
- **THEN** the server start continues on the current no-gate path.

### Requirement: Clean rollback path

Where the touched fact-read path rolls back, the server SHALL branch on the rollback error without the blank-assign pattern.

#### Scenario: Rollback then close
- **WHEN** rollback runs on the touched fact-read path and close then runs
- **THEN** the method closes the transaction and returns the first error with the close behavior unchanged.

### Requirement: Decision record retention setting

The server SHALL declare the installation setting `decisionRecords.retentionHours` in the settings resource. Its default SHALL be `0`, which keeps decision records forever. A value SHALL be `0` or a canonical whole number of hours from 24 to 87600. The server SHALL refuse every other value with a 400 answer that names the key, the value and the allowed range.

#### Scenario: Default keeps forever
- **WHEN** an administrator reads GET /api/dustpan/v1/settings on a new installation
- **THEN** the item `decisionRecords.retentionHours` has value `0` and source `default`

#### Scenario: Valid period is stored
- **WHEN** an administrator patches `decisionRecords.retentionHours` to the JSON number `8760` with the current revision
- **THEN** the item shows value `8760` and source `database`

#### Scenario: Hostile value is refused
- **WHEN** an administrator patches the key to `23`, `87601`, `-1`, `+24`, `024`, `1e3`, ` 24`, `24.0`, `abc` or an empty text
- **THEN** the server answers 400 and the stored value does not change

### Requirement: Shorter period needs a fresh authentication

When a change makes the retention period end records sooner than the period in force, including a change from `0` to a number of hours, the server SHALL require a fresh authentication, and SHALL treat a value that does not parse as such a change.

#### Scenario: Shorten without fresh step
- **WHEN** a caller with no fresh authentication changes `8760` to `720`
- **THEN** the server refuses the change and the stored value does not change

#### Scenario: Lengthen without fresh step
- **WHEN** a caller with no fresh authentication changes `720` to `8760`
- **THEN** the server stores the value

### Requirement: Change is recorded

When the value of the setting changes, the server SHALL write one audit entry in the transaction of the change. The setting SHALL NOT delete or change any audit entry.

#### Scenario: Audit entry of a change
- **WHEN** an administrator changes the value
- **THEN** the server writes one audit entry that names the key, the old value and the new value

#### Scenario: Refused stored value
- **WHEN** the database holds a value that the declaration refuses
- **THEN** the resource shows the default `0` and no record is deleted
