## ADDED Requirements

### Requirement: Current version on each write

When a tool writes state for a unit, the server SHALL set the current state version of the unit in the fenced transaction of the write.

#### Scenario: Pulumi checkpoint
- **WHEN** the Pulumi CLI saves a checkpoint
- **THEN** the current state version of the stack is the new checkpoint

#### Scenario: OpenTofu finalize
- **WHEN** an OpenTofu state version finalizes
- **THEN** the current state version of the workspace is the new version

### Requirement: Final version closes awaiting jobs

When a final state write commits, the server SHALL close the awaiting write-back job in the same fenced transaction.

#### Scenario: Final write closes a job
- **WHEN** a final state write commits for a unit with an awaiting job
- **THEN** the job closes in the same commit

#### Scenario: Duplicate final write
- **WHEN** the same final version commits twice
- **THEN** the second write closes nothing and changes no version

### Requirement: Rollback

If the write of a version row fails, then the server SHALL roll back the state write.

#### Scenario: Failed version write
- **WHEN** the version write fails
- **THEN** neither state nor version rows commit
