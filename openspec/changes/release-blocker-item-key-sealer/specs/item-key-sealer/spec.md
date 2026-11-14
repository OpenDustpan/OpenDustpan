## ADDED Requirements

### Requirement: CheckWrapper

If an item key record names an unknown wrapper ID, then the server SHALL refuse to start.

#### Scenario: CheckWrapper unknown wrapper blocks start
- **WHEN** an item key record names a wrapper ID outside the known set
- **THEN** the server refuses to start and names the unknown wrapper ID

#### Scenario: CheckWrapper unopenable blob blocks start
- **WHEN** an item key blob with a known wrapper does not open under the current key-encryption key
- **THEN** the server refuses to start and names the blob key

#### Scenario: CheckWrapper missing blob blocks start
- **WHEN** an item key record has no blob
- **THEN** the server refuses to start and names the blob key

#### Scenario: CheckWrapper shredded records do not block start
- **WHEN** each item key record with the unknown wrapper is shredded
- **THEN** the server starts

### Requirement: RewrapItemKeys

When the operator runs the key rewrap command, the server SHALL rewrap each live item key blob.

#### Scenario: RewrapItemKeys rewrites live blobs
- **WHEN** the operator runs the rewrap command with the server stopped
- **THEN** each live item key blob opens under the new key-encryption key

#### Scenario: RewrapItemKeys repeat run is safe
- **WHEN** the prior RewrapItemKeys run stopped after some blobs
- **THEN** a repeat run leaves blobs already under the new key unchanged

#### Scenario: RewrapItemKeys skips shredded records
- **WHEN** some item key records are shredded
- **THEN** the command skips their blobs and completes

### Requirement: ItemKeyBlobKey

When the sealer writes an item key blob, the server SHALL use the version token v0 in the blob key.

#### Scenario: ItemKeyBlobKey writes v0
- **WHEN** the sealer writes an item key blob
- **THEN** the blob key uses v0 under the namespace prefix and never v1
