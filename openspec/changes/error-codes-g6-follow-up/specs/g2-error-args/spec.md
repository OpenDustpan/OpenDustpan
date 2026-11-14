## ADDED Requirements

### Requirement: Workspace lock holder

When a workspace write meets a lock held by another holder, the server SHALL carry workspace and holder as identifier arguments.

#### Scenario: Locked workspace
- **WHEN** a workspace write meets a lock held by another holder
- **THEN** the answer carries workspace and holder in errorArgs.

### Requirement: Workspace pending bound

When pending state versions reach the bound, the server SHALL carry pending and limit as quantity arguments with workspace as an identifier argument.

#### Scenario: Pending bound
- **WHEN** pending state versions reach the bound
- **THEN** the answer carries workspace, pending and limit in errorArgs.

### Requirement: State version bound

When a state write exceeds the byte bound, the server SHALL carry limitBytes as a quantity argument.

#### Scenario: Large state
- **WHEN** a state write exceeds the byte bound
- **THEN** the answer carries limitBytes in errorArgs.

#### Scenario: Stale serial
- **WHEN** a state write names a stale serial
- **THEN** the answer carries currentSerial in errorArgs.
