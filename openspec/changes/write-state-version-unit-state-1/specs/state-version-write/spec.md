## ADDED Requirements

### Requirement: Current version on each write

The server SHALL write the current state version of the unit in the fenced transaction of each state write of each tool.

#### Scenario: Pulumi state write sets current version
- **WHEN** the Pulumi CLI saves a checkpoint for a stack
- **THEN** the current state version of the stack equals the new checkpoint version

#### Scenario: OpenTofu state write sets current version
- **WHEN** an OpenTofu state version finalizes for a workspace
- **THEN** the current state version of the workspace equals the finalized version

### Requirement: Final-version observe in same transaction

When a state write is final, the server SHALL apply the final-version observe rule in the same fenced transaction.

#### Scenario: Final write closes write-back job
- **WHEN** a final state write commits for a unit with an open write-back job
- **THEN** the matching write-back job closes in the same commit

#### Scenario: Failed observe rolls back state write
- **WHEN** the final-version observe call fails
- **THEN** the state write commits neither state nor version rows

#### Scenario: Duplicate final write is idempotent
- **WHEN** the same final version commits twice
- **THEN** the second observe closes nothing and changes no version

### Requirement: Versions keyed by stable unit ID

The server SHALL key current and final versions by the stable unit ID of the unit.

#### Scenario: Rename keeps versions
- **WHEN** a unit is renamed and then writes state
- **THEN** the current and final versions stay attached to the same stable unit ID

### Requirement: Observer wiring

When a tool store has no injected observer, the server SHALL refuse a final state write.

#### Scenario: Store without observer refuses final write
- **WHEN** a final state write runs on a store built without an observer
- **THEN** the write fails and writes neither state nor version rows
