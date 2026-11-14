## ADDED Requirements

### Requirement: ReadOutputs

When a consumer reads the outputs of a stack with a running journaled update, the server SHALL return the outputs of the last completed checkpoint.

#### Scenario: Read during journaled update
- **WHEN** a consumer reads the outputs of a stack with a running journaled update
- **THEN** the server returns the outputs and the state version id of the last completed checkpoint, not the outputs of the running batches

### Requirement: NameSourceCheckpoint

When an output read serves the last completed checkpoint, the server SHALL return the state version id of that checkpoint with field updateRunning.

#### Scenario: Read names completed version during update
- **WHEN** an output read serves the last completed checkpoint during a running update
- **THEN** the answer carries the state version id of the completed checkpoint with field updateRunning set to true

#### Scenario: ServePinnedCheckpointWithoutUpdate
- **WHEN** an output read serves a completed checkpoint with no running update
- **THEN** the answer carries the state version id of that checkpoint with field updateRunning set to false

### Requirement: RefuseReadWithNoCheckpoint

When a consumer reads the outputs of a stack with no completed checkpoint, the server SHALL refuse the read with a 404 unknown-state error.

#### Scenario: Read before first checkpoint
- **WHEN** a consumer reads the outputs of a stack with no completed checkpoint
- **THEN** the server refuses the read with a 404 unknown-state error

#### Scenario: Read after producer delete
- **WHEN** a consumer reads the outputs of a deleted producer stack
- **THEN** the server refuses the read with a 404 unknown-state error against the stable producer id

#### Scenario: Producer removed and remade by name
- **WHEN** a producer is removed and remade with the same name after a consumer read
- **THEN** the server refuses the read against the old stable producer id with a 404 unknown-state error

### Requirement: RefuseReadWithoutSharingRule

When a consumer without a sharing rule reads the outputs of a producer stack, the server SHALL refuse the read with a 403 denied error.

#### Scenario: Read without rule
- **WHEN** a consumer without a sharing rule reads the outputs of a producer stack
- **THEN** the server refuses the read with a 403 denied error

#### Scenario: Rule removed in flight
- **WHEN** the sharing rule is removed before a running output read completes
- **THEN** the server refuses the read with a 403 denied error

### Requirement: RefuseReadWithoutGrant

When a caller without unit:read-outputs reads the outputs of a producer stack, the server SHALL refuse the read with a 403 denied error.

#### Scenario: Read without unit read-outputs grant
- **WHEN** a caller without unit:read-outputs reads the outputs of a producer stack
- **THEN** the server refuses the read with a 403 denied error

### Requirement: DenyBeforeNotFound

When a read lacks the grant or the sharing rule and the checkpoint is also missing, the server SHALL return 403 denied before any 404 check.

#### Scenario: Missing grant and missing checkpoint
- **WHEN** a read lacks the grant and the checkpoint is also missing
- **THEN** the server returns 403 denied and never reveals checkpoint existence

### Requirement: StableReadDuringCompletionRace

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
