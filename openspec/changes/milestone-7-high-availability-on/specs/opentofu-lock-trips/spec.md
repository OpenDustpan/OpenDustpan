## ADDED Requirements

### Requirement: Lock trip budget

When a principal locks an OpenTofu workspace, the server SHALL use 42 or fewer sequential database round trips.

#### Scenario: Lock reads unit ID once
- **WHEN** a principal locks an OpenTofu workspace
- **THEN** the lock succeeds with 42 or fewer sequential round trips

#### Scenario: Stale unit ID
- **WHEN** the unit is recreated between the handler read and the gate transactions
- **THEN** the server answers 404 with error code workspace.not-found and writes no gate row

### Requirement: Unlock trip budget

When a principal unlocks an OpenTofu workspace, the server SHALL use 10 or fewer sequential database round trips.

#### Scenario: Unlock folds last release
- **WHEN** a principal unlocks a workspace
- **THEN** the unlock succeeds with 10 or fewer round trips and the workspace row keeps the last release

#### Scenario: Repeated unlock
- **WHEN** the same principal repeats the unlock within 24 hours
- **THEN** the repeated unlock succeeds with no extra last-release write
