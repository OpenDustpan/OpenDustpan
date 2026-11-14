## ADDED Requirements

### Requirement: Cancel timing

When the operator cancels a restore before the swap begins, the server SHALL end the restore as failed with no swap.

#### Scenario: Cancel during check
- **WHEN** the operator cancels a restore in the check step
- **THEN** the restore stays checking until the running step ends, then moves to failed, and staging schemas stay until the next restore drops them

#### Scenario: Cancel during apply
- **WHEN** the operator cancels a restore in the apply step
- **THEN** the restore finishes the running batch, moves to failed, and never swaps

#### Scenario: Cancel at swap start
- **WHEN** the operator cancels a restore after the swap began
- **THEN** the swap completes once and the restore ends as succeeded with no second swap

#### Scenario: Double cancel
- **WHEN** the operator cancels a restore twice
- **THEN** the second cancel answers that the restore already ended with no state change

#### Scenario: Cancel authorization
- **WHEN** a token without server:restore calls the cancel route
- **THEN** the server denies the cancel and writes an audit entry

### Requirement: Damaged live blobs ID

When a restore takes a pre-restore backup, the server SHALL include its preBackupID in the restoreLiveError restore result.

#### Scenario: Damaged live blobs with backup
- **WHEN** a restore fails with live blobs damaged after the pre-restore backup was taken
- **THEN** the restoreLiveError result names the pre-restore backup ID

#### Scenario: No pre backup
- **WHEN** a restore fails before the pre-restore backup was taken
- **THEN** the restoreLiveError result omits preBackupID
