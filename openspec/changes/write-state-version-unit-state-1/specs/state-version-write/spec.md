## ADDED Requirements

### Requirement: Current version on each write

The server SHALL write the current state version of the unit in the fenced transaction of each state write of each tool.

#### Scenario: Pulumi checkpoint sets current version
- **WHEN** the Pulumi CLI saves a checkpoint for a stack
- **THEN** the current state version of the stack equals the new checkpoint version

#### Scenario: OpenTofu finalize sets current version
- **WHEN** an OpenTofu state version finalizes for a workspace
- **THEN** the current state version of the workspace equals the finalized version

### Requirement: Final write definition

When a state write is final, the server SHALL apply the final-version observe rule in the same fenced transaction.

#### Scenario: Completed Pulumi update is final
- **WHEN** a Pulumi checkpoint belongs to a completed update
- **THEN** the server applies the final-version observe rule in the same transaction

#### Scenario: Preview is not final
- **WHEN** a Pulumi preview saves events with no checkpoint
- **THEN** the server writes only the current version and applies no final rule

#### Scenario: OpenTofu finalize is final
- **WHEN** an OpenTofu state version finalizes
- **THEN** the server applies the final-version observe rule in the same transaction

#### Scenario: Pending upload is not final
- **WHEN** an OpenTofu state version upload stays pending
- **THEN** the server writes only the current version and applies no final rule

### Requirement: Final observe outcome

If the final-version observe call fails, then the server SHALL roll back the state write.

#### Scenario: Final write closes write-back job
- **WHEN** a final state write commits for a unit with an open write-back job
- **THEN** the matching write-back job closes in the same commit

#### Scenario: Failed observe rolls back state write
- **WHEN** the final-version observe call fails
- **THEN** the state write commits neither state nor version rows

#### Scenario: Duplicate final write is idempotent
- **WHEN** the same final version ID commits twice
- **THEN** the second observe closes nothing and changes no version

### Requirement: Versions keyed by stable unit ID

The server SHALL key current and final versions by the stable unit ID of the unit.

#### Scenario: Rename keeps versions
- **WHEN** a unit is renamed and then writes state
- **THEN** the current and final versions stay attached to the same stable unit ID

#### Scenario: Remade unit starts empty
- **WHEN** a unit is removed and made again with the same name
- **THEN** the unit has no current and no final version until its first state write

### Requirement: Concurrent and forced writes keep order

While concurrent state writes commit, the server SHALL keep the current version at the highest committed version in fenced order.

#### Scenario: Out-of-order commit keeps newest
- **WHEN** two state writes of one unit commit out of order
- **THEN** the current version stays at the highest committed version

#### Scenario: Fence loss aborts write
- **WHEN** the fence check fails during the state write
- **THEN** neither state nor version rows commit

#### Scenario: Forced write and restore are state items
- **WHEN** a forced write or a restore commits for a workspace
- **THEN** each one updates the current version and applies the final rule

### Requirement: Edge cases keep defined versions

While an opaque, redacted, or purged history item is involved, the server SHALL keep the current version defined without exposing sealed content.

#### Scenario: Opaque state keeps no resource rows
- **WHEN** a client-encrypted OpenTofu state finalizes
- **THEN** the current version updates and the item stays opaque

#### Scenario: Checkpoint-less update changes nothing
- **WHEN** a Pulumi update ends with no checkpoint
- **THEN** the current version stays unchanged

#### Scenario: Purged unit starts empty on remake
- **WHEN** a purged unit is made again
- **THEN** the remade unit has no versions until its first state write

### Requirement: Observer wiring

When a tool store has no injected observer, the server SHALL refuse a final state write.

#### Scenario: Store without observer refuses final write
- **WHEN** a final state write runs on a store built without an observer
- **THEN** the write fails with status 500 and writes neither state nor version rows
