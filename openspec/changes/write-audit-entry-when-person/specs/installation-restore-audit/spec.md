## ADDED Requirements

### Requirement: End entry always attempted

When an installation restore ends by calling FinishRestore with events appended by appendEvents, the server SHALL attempt its end audit entry also when its stored events cannot be read.

#### Scenario: Events read fails
- **WHEN** RestoreEvents fails at the end of a restore finished by FinishRestore and appended by appendEvents.
- **THEN** The audit log holds one end entry with the restore id and the cause.

### Requirement: Decode and unknown-op failures write entry

If restore input cannot be decoded or the operation is missing and FailEndedRestores marks the row, then the server SHALL write a failed end audit entry with the cause.

#### Scenario: Decode failure
- **WHEN** readRestoreInput fails or the op is missing and FailEndedRestores marks the row.
- **THEN** The audit log holds one failed end entry with the cause before the row is marked failed by FailEndedRestores.

### Requirement: Superseding failure entry

When a later step of a restore fails after a success entry, the server SHALL append a failure entry that states that it supersedes the earlier success.

#### Scenario: Later failure
- **WHEN** A step after the success entry of appendEvents fails.
- **THEN** The earlier entry stays unchanged and the failure entry names it.

### Requirement: Log lines keep cause

When a restore audit append fails, the server SHALL log the restore id with the error class and the cause.

#### Scenario: Append failure logs cause
- **WHEN** An audit append of appendEvents for a restore fails.
- **THEN** The log line holds the restore id, the error class and the cause.
