## ADDED Requirements

### Requirement: store-encrypted-bit-at-finalize

When an OpenTofu state version finalizes, the server SHALL store the client-encrypted bit from the upload stream in the state version row.

#### Scenario: finalize stores bit
- **WHEN** an OpenTofu state upload finalizes
- **THEN** the state version row holds the same client-encrypted bit as the uploaded document

#### Scenario: concurrent finalizes
- **WHEN** two state versions of one unit finalize at the same time
- **THEN** the exposed latest row holds the bit of the winning version

### Requirement: latest avoids blob read

When a caller reads the latest state version whose stored bit is present, the server SHALL answer the stored client-encrypted bit without reading the state blob.

#### Scenario: outputs read uses row
- **WHEN** an outputs read asks for the latest state version
- **THEN** the answer uses the stored bit and performs no blob read

#### Scenario: missing blob with present bit
- **WHEN** the latest row holds a present bit and the blob is missing or unreadable
- **THEN** the read answers from the stored row bit and performs no blob check

### Requirement: refuse-both-keys

If an uploaded state document holds both encrypted_data and version, then the server SHALL refuse the document as invalid state.

#### Scenario: both keys refused
- **WHEN** an uploaded document holds both encrypted_data and version
- **THEN** the upload fails with invalid state and stores no version

### Requirement: legacy-null-bit-fallback

When the latest state version row holds a null client-encrypted bit, the server SHALL determine the bit by streaming the state blob.

#### Scenario: legacy row streams once
- **WHEN** the latest row holds a null client-encrypted bit from before the change
- **THEN** Latest determines the bit by streaming the blob and the outputs answer stays correct

#### Scenario: legacy document with both keys
- **WHEN** a stored pre-change blob holds both encrypted_data and version
- **THEN** the read fails with invalid state through the fallback stream

### Requirement: no-write-back-on-fallback

When the null-bit fallback streams the state blob, the server SHALL leave the stored bit null.

#### Scenario: fallback leaves row null
- **WHEN** the null-bit fallback streams the blob
- **THEN** the row still holds null after the read

### Requirement: atomic-finalize

While a state version finalize has not committed its row, the server SHALL not expose that version as latest.

#### Scenario: crash before row commit
- **WHEN** finalize fails or stops after the blob write and before the row commit
- **THEN** no new latest version is visible and no row without a bit is exposed

### Requirement: no-finalized-version

If no finalized state version exists, then the server SHALL answer not found without reading a state blob.

#### Scenario: no state yet
- **WHEN** no finalized state version exists
- **THEN** the read answers not found and performs no blob read

#### Scenario: pending version only
- **WHEN** only a pending upload exists
- **THEN** the read skips the pending version and answers not found or the last finalized version
