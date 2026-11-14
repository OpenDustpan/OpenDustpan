## ADDED Requirements

### Requirement: Read last completed outputs during update

When a consumer reads the outputs of a stack with a running journaled update, the server SHALL return the outputs of the last completed checkpoint.

#### Scenario: Read during journaled update
- **WHEN** a consumer reads the outputs of a stack with a running journaled update
- **THEN** the server returns the outputs and the state version id of the last completed checkpoint, not the outputs of the running batches

### Requirement: Name the source checkpoint

When an output read serves the last completed checkpoint during a running update, the server SHALL return the state version id of that checkpoint with a flag that an update runs.

#### Scenario: Read names completed version
- **WHEN** an output read serves the last completed checkpoint during a running update
- **THEN** the answer carries the state version id of the completed checkpoint and a flag that an update runs

### Requirement: Refuse read with no completed checkpoint

When a consumer reads the outputs of a stack with no completed checkpoint, the server SHALL refuse the read with a 404 unknown-state error.

#### Scenario: Read before first checkpoint
- **WHEN** a consumer reads the outputs of a stack with no completed checkpoint
- **THEN** the server refuses the read with a 404 unknown-state error

#### Scenario: Read after producer delete
- **WHEN** a consumer reads the outputs of a deleted producer stack
- **THEN** the server refuses the read with a 404 unknown-state error against the stable producer ID

### Requirement: Refuse read without sharing rule

When a consumer without a sharing rule reads the outputs of a producer stack, the server SHALL refuse the read with a 403 denied error.

#### Scenario: Read without rule
- **WHEN** a consumer without a sharing rule reads the outputs of a producer stack
- **THEN** the server refuses the read with a 403 denied error

#### Scenario: Rule removed in flight
- **WHEN** the sharing rule is removed before a running output read completes
- **THEN** the server refuses the read with a 403 denied error

### Requirement: Stable read during completion race

When an output read races the completion of a running update, the server SHALL serve one completed checkpoint and return its state version id.

#### Scenario: Checkpoint completes during read
- **WHEN** a checkpoint completes between the running-update check and the checkpoint load
- **THEN** the server serves one completed checkpoint and returns its state version id

#### Scenario: Update cancelled or failed mid-journal
- **WHEN** a consumer reads after a journaled update was cancelled or failed
- **THEN** the server serves the last completed checkpoint before the failed update

#### Scenario: Restart mid-journal
- **WHEN** the server restarts during a journaled update and a consumer reads before the update completes
- **THEN** the server serves the last completed checkpoint and never the partial journal
