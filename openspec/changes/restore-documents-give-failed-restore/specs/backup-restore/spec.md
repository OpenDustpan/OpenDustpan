## ADDED Requirements

### Requirement: Coded failed restore

When a restore ends failed with a catalog failure, the server SHALL return its catalog code in errorCode.

#### Scenario: Blobs missing failure
- **WHEN** a restore fails because the backup misses blobs
- **THEN** the document includes error text and errorCode restore.blobs-missing.

#### Scenario: Old row without code
- **WHEN** a restore row stored before this change has text with no code
- **THEN** the document returns the stored text with empty errorCode.

#### Scenario: Uncoded failure
- **WHEN** sanitizeRestoreError handles a restore failure with no catalog code
- **THEN** the document returns the sanitized text with empty errorCode.

### Requirement: Unknown backup refused

When a restore names an unknown backup ID, the server SHALL refuse it with the NEW error code `backup.not-found`.

#### Scenario: Unknown backup ID
- **WHEN** a restore names a backup ID that the store has no record for
- **THEN** the start answers 404 with the NEW error code `backup.not-found` and stores no restore row.

#### Scenario: Malformed backup ID
- **WHEN** a restore names a backup ID that breaks the backup ID shape allowlist
- **THEN** the start answers 400 with code backup.corrupt.

#### Scenario: Backup vanished mid-restore
- **WHEN** a backup is removed after the start check and before the read
- **THEN** the worker ends the restore failed with the NEW error code `backup.not-found`.

### Requirement: Grant check first

While handling a restore start or read, the server SHALL check the restore grant before the backup existence check.

#### Scenario: Caller without grant
- **WHEN** a caller without the restore grant calls the restores routes
- **THEN** the server denies the call without revealing whether the backup ID exists.
