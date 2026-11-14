## ADDED Requirements

### Requirement: Attempt-scoped target writes

The server SHALL write each backup attempt only under `backups/<backup id>/<attempt>/`.

#### Scenario: Stale delete after survivor finish
- **WHEN** a stale holder issues a late delete after the survivor committed finish
- **THEN** the survivor files remain readable and Check reports complete

### Requirement: Manifest names the attempt

When a backup attempt writes its manifest, the server SHALL record the claim fencing token as the attempt in the backups row and in the manifest.

#### Scenario: Attempt mismatch refuses finish
- **WHEN** the manifest attempt differs from the claim fencing token
- **THEN** the finish step fails without marking the backup succeeded

### Requirement: Fenced manifest write

When a backup attempt writes state, the server SHALL refuse the write unless the caller still holds the claim fencing token.

#### Scenario: Stale manifest refused
- **WHEN** a stale holder tries to write the manifest with an old fencing token
- **THEN** the manifest write fails and the live attempt is unchanged

### Requirement: Finish verification

When the finish step runs, the server SHALL check sizes and blob copies of the attempt prefix with Check before the fenced finish write.

#### Scenario: Truncated file fails finish
- **WHEN** a listed file of the attempt prefix is missing or its size differs from the manifest
- **THEN** the finish step fails with `backup.corrupt` and the backup does not read succeeded

### Requirement: Fenced finish commit

When the finish step commits, the server SHALL compare the manifest attempt with the live claim fencing token in the same transaction.

#### Scenario: Stale finish refused
- **WHEN** the claim fencing token moved before the finish commit
- **THEN** the commit is refused and the backup stays running

### Requirement: Takeover uses a new prefix

When a backup is reclaimed after lease expiry, the server SHALL run the next attempt under a new attempt prefix.

#### Scenario: Takeover writes new prefix
- **WHEN** another worker reclaims the backup after lease expiry
- **THEN** the new attempt writes only under its own prefix and the old prefix stays until cleanup

### Requirement: Orphan prefix cleanup

When a backup finishes, the server SHALL delete each non-live attempt prefix of that backup under the live claim.

#### Scenario: Orphan prefix deleted
- **WHEN** a backup finished on a new attempt while an older attempt prefix remains
- **THEN** the non-live prefix is deleted and the finished backup stays complete

#### Scenario: Recreated stale prefix removed again
- **WHEN** a stale writer recreates its prefix after cleanup deleted it
- **THEN** the next list removes it and the live prefix stays complete

### Requirement: Restore reads the live prefix

When a restore reads a backup, the server SHALL read only the attempt prefix named by the finished manifest.

#### Scenario: Restore with two prefixes
- **WHEN** a backup target holds two attempt prefixes
- **THEN** the restore reads only the attempt named by the finished manifest

#### Scenario: Old unscoped layout refused
- **WHEN** the target holds only the old unscoped layout with no attempt prefix
- **THEN** the restore fails without reading files outside an attempt prefix
