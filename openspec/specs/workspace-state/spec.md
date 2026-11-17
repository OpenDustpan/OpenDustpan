# workspace-state Specification

## Purpose
This capability states how the server records and reads the client-encryption bit of a state version. The server writes the bit when it finalizes a state version and refuses a finalize without a known bit. A read of the latest version answers the bit from the row with no blob read, and streams the blob only for a version stored before the server kept the bit. The server refuses an uploaded state document that holds both encrypted_data and version.

## Requirements

### Requirement: store-encrypted-bit-at-finalize

When an OpenTofu state version finalizes, including by forced write or restore, the server SHALL store the client-encrypted bit from the upload stream in the state version row.

#### Scenario: finalize stores bit
- **WHEN** an OpenTofu state upload finalizes
- **THEN** the row holds the same bit as the uploaded document

#### Scenario: forced write and restore
- **WHEN** a forced write or a restore creates a state version
- **THEN** the new row holds the bit from its stream

### Requirement: latest-avoids-blob-read

When a caller reads the latest state version whose stored bit is present, the server SHALL answer the bit without reading the state blob.

#### Scenario: row read only
- **WHEN** the latest row holds a present bit
- **THEN** the answer uses the stored bit and performs no blob read

### Requirement: legacy-null-bit-fallback

When the latest state version row holds a null bit, the server SHALL determine the bit by streaming the state blob.

#### Scenario: legacy row
- **WHEN** the latest row holds a null bit
- **THEN** the bit is correct and the row still holds null after the read

### Requirement: refuse-both-keys

If an uploaded state document holds both encrypted_data and version, then the server SHALL refuse the document as invalid state.

#### Scenario: both keys refused
- **WHEN** an uploaded document holds both keys
- **THEN** the upload fails and no version is stored
