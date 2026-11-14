## ADDED Requirements

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

When the server starts with no gate service, the server SHALL write one INFO log line that names the absent gate service.

#### Scenario: Server starts with no gate service
- **WHEN** the server starts with no gate service configured
- **THEN** the log holds one INFO line with fixed text, and a server with a gate service logs no such line.

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
