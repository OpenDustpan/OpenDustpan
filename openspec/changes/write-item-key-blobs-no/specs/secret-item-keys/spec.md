## ADDED Requirements

### Requirement: Seal through blobs

When the server seals history content, the server SHALL write the wrapped item key through ItemKeyBlobs under the namespace ID and the v<version> segment.

#### Scenario: Round trip
- **WHEN** the server seals content with a new item key
- **THEN** the blob key holds the namespace ID and the version segment and an open returns the plain bytes

### Requirement: Shred deletes blob

When an item key shred commits, the server SHALL delete the wrapped key blob through ItemKeyBlobs.Delete.

#### Scenario: Blob gone
- **WHEN** the shred commits
- **THEN** ItemKeyBlobs.Versions lists no version and an open fails with the shred error

### Requirement: Retry delete

If a shred is repeated for a shredded item key, then the server SHALL delete each blob version that remains.

#### Scenario: Retry
- **WHEN** the store refused the first delete and the caller shreds again
- **THEN** the blob is removed and the row stays shredded

### Requirement: Missing record

If the item key record is missing, then the server SHALL refuse the shred with a not-found error.

#### Scenario: Missing owner
- **WHEN** the record is missing
- **THEN** the shred fails and deletes no blob

### Requirement: No seal after shred

If the item key is shredded, then the server SHALL refuse to write a blob for it.

#### Scenario: Seal after shred
- **WHEN** a seal runs for a shredded key
- **THEN** the seal fails and no blob exists

### Requirement: Missing blob

If the key blob is missing for a key that is not shredded, then the server SHALL refuse the open with an error.

#### Scenario: Open without blob
- **WHEN** the blob is missing and shredded_at_ms is 0
- **THEN** the open fails and the error names the item key

### Requirement: Failed delete audit

If the blob delete fails, then the server SHALL write an audit entry through the out-of-scope recorder.

#### Scenario: Audit on failure
- **WHEN** the blob store refuses the delete
- **THEN** the shred returns an error and the recorder holds one entry
